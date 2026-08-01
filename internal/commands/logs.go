package commands

import (
	"context"
	"fmt"
	"time"

	"github.com/outplane/cli/internal/clierr"
	"github.com/outplane/cli/internal/core"
	"github.com/outplane/cli/internal/output"
)

func init() {
	register("logs", logs)
}

// logs prints an application's own output.
//
// Everything it does is a query: there is no local filtering, because pulling a
// busy application's full output back and discarding most of it would fail at
// exactly the moment somebody needs to read it.
func logs(ctx context.Context, req Request) (output.Table, error) {
	gw, err := openGateway(req)
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

	query := core.BuildRuntimeQuery(gw.teamSlug, filter)
	fetch := func(ctx context.Context, w core.LogWindow) ([]core.LogLine, error) {
		return core.QueryLogs(ctx, gw.client, gw.base, gw.teamSlug, query, w)
	}

	lines, err := fetch(ctx, window)
	if err != nil {
		return output.Table{}, err
	}

	showTimes := req.Flags.Bool("timestamps")
	// Names are prefixed only when the query spans the team, where a line with
	// no name is unattributable. For one application it would repeat the name
	// the reader just typed on every line.
	showApp := len(filter.Apps) != 1
	print := func(items []core.LogLine) { printLines(req, items, showTimes, showApp) }

	print(lines)

	if !req.Flags.Bool("follow") {
		if len(lines) == 0 {
			req.CLI.Out.Note("Nothing in the last %s.", req.Flags.String("since"))
		}
		return streamed(), nil
	}

	return streamed(), follow(ctx, req, window, core.Cursor(lines), stream[core.LogLine]{
		fetch:  fetch,
		print:  print,
		cursor: core.Cursor[core.LogLine],
	})
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
	app, err := gatewayApp(ctx, req)
	if err != nil || app == "" {
		return f, err
	}
	f.Apps = []string{app}
	return f, nil
}
