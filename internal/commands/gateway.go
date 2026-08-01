package commands

import (
	"context"
	"strconv"
	"time"

	"github.com/outplane/cli/internal/api"
	"github.com/outplane/cli/internal/clierr"
	"github.com/outplane/cli/internal/core"
)

// Shared machinery for the two commands that read the log gateway.
//
// `logs` and `requests` ask the same host for the same kind of window and
// resume a follow the same way. They differ only in the query they send and in
// what they do with what comes back, so everything either side of that lives
// here rather than twice.

// followInterval is how often a follow asks for new records.
//
// Two seconds rather than the console's one: a terminal reader is not watching
// a live chart, and halving the request rate on a command people leave running
// all day is worth the delay nobody notices.
const followInterval = 2 * time.Second

// gateway is a resolved connection to the log gateway.
type gateway struct {
	client   *api.Client
	base     string
	teamSlug string
}

// openGateway resolves everything a gateway query needs, or says what is
// missing.
func openGateway(req Request) (gateway, error) {
	cli := req.CLI

	if cli.Config.TeamError != nil {
		return gateway{}, cli.SignInError()
	}

	// The gateway addresses streams by team slug. A token that carries only a
	// team id therefore cannot read anything here at all, and saying which
	// token would work is more useful than an empty result.
	teamSlug := cli.Config.TeamSlug.Value
	if teamSlug == "" {
		return gateway{}, clierr.New(clierr.KindUsage, "this credential does not name its team").
			WithCode("logs.no_team_slug").
			WithHint("Logs are addressed by team name, which older tokens do not carry.").
			WithStep("create a replacement token", "outplane", "login")
	}

	client, err := cli.APIClient()
	if err != nil {
		return gateway{}, err
	}

	return gateway{client: client, base: cli.Config.LogURL.Value, teamSlug: teamSlug}, nil
}

// gatewayApp resolves which application a gateway query is about, by name.
//
// Empty means the whole team, which both commands accept and which is what
// somebody wants before they know where a problem is. The gateway matches on
// the name rather than the id, so a reference has to be resolved even though
// every other command would pass it straight through.
func gatewayApp(ctx context.Context, req Request) (string, error) {
	ref := ""
	if len(req.Args) > 0 {
		ref = req.Args[0]
	} else if id := req.CLI.Config.AppID.Value; id != "" {
		ref = id
	}
	if ref == "" {
		return "", nil
	}

	app, err := resolveApp(ctx, req, ref)
	if err != nil {
		return "", err
	}
	return app.Name, nil
}

// buildWindow reads --since and --lines.
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

// stream is what a follow does with one kind of record.
type stream[T any] struct {
	fetch  func(ctx context.Context, w core.LogWindow) ([]T, error)
	print  func(items []T)
	cursor func(items []T) string
}

// follow keeps asking for records newer than the last one printed.
//
// It ends only when interrupted. There is no state that means "this
// application has finished serving", so a follow that stopped on its own would
// be inventing one.
func follow[T any](ctx context.Context, req Request, window core.LogWindow, cursor string, s stream[T]) error {
	for {
		select {
		case <-ctx.Done():
			return clierr.New(clierr.KindInterrupted, "stopped following")
		case <-time.After(followInterval):
		}

		w := window
		if cursor != "" {
			w = window.After(cursor)
		}

		items, err := s.fetch(ctx, w)
		if err != nil {
			// One failed poll is not the end of the stream. The reader can see
			// the gap and interrupt; giving up on a blip would be worse.
			req.CLI.Out.Note("Could not read from the log gateway, retrying: %v", err)
			continue
		}
		if len(items) == 0 {
			continue
		}

		s.print(items)
		cursor = s.cursor(items)
	}
}

func orDefault(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}
