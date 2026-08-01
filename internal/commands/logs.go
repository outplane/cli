package commands

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/outplane/cli/internal/api"
	"github.com/outplane/cli/internal/clierr"
	"github.com/outplane/cli/internal/core"
	"github.com/outplane/cli/internal/output"
)

func init() {
	register("logs", logs)
}

// followInterval is how often --follow asks for new lines.
//
// Two seconds rather than the console's one: a terminal reader is not watching
// a live chart, and halving the request rate on a command people leave running
// all day is worth the delay nobody notices.
const followInterval = 2 * time.Second

// logs prints an application's own output.
//
// Everything it does is a query: there is no local filtering, because pulling a
// busy application's full output back and discarding most of it would fail at
// exactly the moment somebody needs to read it.
func logs(ctx context.Context, req Request) (output.Table, error) {
	cli := req.CLI

	if cli.Config.TeamError != nil {
		return output.Table{}, cli.SignInError()
	}

	// The gateway addresses streams by team slug. A token that carries only a
	// team id therefore cannot read logs at all, and saying which token would
	// work is more useful than an empty result.
	teamSlug := cli.Config.TeamSlug.Value
	if teamSlug == "" {
		return output.Table{}, clierr.New(clierr.KindUsage, "this credential does not name its team").
			WithCode("logs.no_team_slug").
			WithHint("Logs are addressed by team name, which older tokens do not carry.").
			WithStep("create a replacement token", "outplane", "login")
	}

	client, err := cli.APIClient()
	if err != nil {
		return output.Table{}, err
	}

	filter, err := buildFilter(ctx, req)
	if err != nil {
		return output.Table{}, err
	}
	window, err := buildWindow(req)
	if err != nil {
		return output.Table{}, err
	}

	query := core.BuildRuntimeQuery(teamSlug, filter)
	base := cli.Config.LogURL.Value

	lines, err := core.QueryLogs(ctx, client, base, teamSlug, query, window)
	if err != nil {
		return output.Table{}, err
	}

	showTimes := req.Flags.Bool("timestamps")
	// Names are prefixed only when the query spans the team, where a line with
	// no name is unattributable. For one application it would repeat the name
	// the reader just typed on every line.
	showApp := len(filter.Apps) != 1

	printLines(req, lines, showTimes, showApp)

	if !req.Flags.Bool("follow") {
		if len(lines) == 0 {
			req.CLI.Out.Note("Nothing in the last %s.", req.Flags.String("since"))
		}
		return streamed(), nil
	}

	return followLogs(ctx, req, client, base, teamSlug, query, window, lines, showTimes, showApp)
}

// followLogs keeps asking for lines newer than the last one printed.
//
// It ends only when interrupted. There is no state that means "this
// application has finished writing", so a follow that stopped on its own would
// be inventing one.
func followLogs(
	ctx context.Context,
	req Request,
	client *api.Client,
	base, teamSlug, query string,
	window core.LogWindow,
	seen []core.LogLine,
	showTimes, showApp bool,
) (output.Table, error) {
	cursor := core.Cursor(seen)

	for {
		select {
		case <-ctx.Done():
			return output.Table{}, clierr.New(clierr.KindInterrupted, "stopped following")
		case <-time.After(followInterval):
		}

		w := window
		if cursor != "" {
			w = window.After(cursor)
		}

		lines, err := core.QueryLogs(ctx, client, base, teamSlug, query, w)
		if err != nil {
			// One failed poll is not the end of the log. The reader can see
			// the gap and interrupt; giving up on a blip would be worse.
			req.CLI.Out.Note("Could not read logs, retrying: %v", err)
			continue
		}
		if len(lines) == 0 {
			continue
		}

		printLines(req, lines, showTimes, showApp)
		cursor = core.Cursor(lines)
	}
}

// printLines writes to stdout, because here the lines are the result rather
// than progress.
func printLines(req Request, lines []core.LogLine, showTimes, showApp bool) {
	for _, l := range lines {
		var prefix string
		if showTimes {
			prefix = l.At.Format(time.RFC3339) + " "
		}
		if showApp && l.App != "" {
			prefix += l.App + "  "
		}
		fmt.Fprintln(req.CLI.Out.Out, prefix+l.Text)
	}
}

// buildFilter turns the flags into a query filter, resolving the application
// argument to a name.
func buildFilter(ctx context.Context, req Request) (core.LogFilter, error) {
	f := core.LogFilter{Search: req.Flags.String("search")}

	if raw := req.Flags.String("level"); raw != "" {
		level, err := core.ParseLevel(raw)
		if err != nil {
			return f, clierr.New(clierr.KindUsage, "%v", err).WithCode("usage.bad_level")
		}
		f.Levels = []core.LogLevel{level}
	}

	// The gateway matches on the application's name, so an id has to be
	// resolved even though every other command would take it as-is.
	ref := ""
	if len(req.Args) > 0 {
		ref = req.Args[0]
	} else if id := req.CLI.Config.AppID.Value; id != "" {
		ref = id
	}
	if ref == "" {
		return f, nil
	}

	app, err := resolveApp(ctx, req, ref)
	if err != nil {
		return f, err
	}
	f.Apps = []string{app.Name}
	return f, nil
}

func buildWindow(req Request) (core.LogWindow, error) {
	since, err := time.ParseDuration(orDefault(req.Flags.String("since"), "1h"))
	if err != nil {
		return core.LogWindow{}, clierr.New(clierr.KindUsage, "--since is not a duration: %q", req.Flags.String("since")).
			WithCode("usage.bad_duration").
			WithHint("Use a Go duration such as 30m, 6h or 2h45m.")
	}

	limit, err := strconv.Atoi(orDefault(req.Flags.String("lines"), "200"))
	if err != nil || limit <= 0 {
		return core.LogWindow{}, clierr.New(clierr.KindUsage, "--lines is not a positive number: %q", req.Flags.String("lines")).
			WithCode("usage.bad_lines")
	}

	return core.LogWindow{Since: since, Limit: limit}, nil
}

func orDefault(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}
