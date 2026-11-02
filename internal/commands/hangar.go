package commands

import (
	"context"
	"errors"
	"io"
	"strings"

	"github.com/outplane/cli/internal/clierr"
	"github.com/outplane/cli/internal/config"
	"github.com/outplane/cli/internal/core"
	"github.com/outplane/cli/internal/output"
)

func init() {
	register("hangar list", hangarList)
	register("hangar get", hangarGet)
	register("hangar connect", hangarConnect)
}

// hangarList reports the team's Hangars. It follows app list: one request, the
// whole set.
func hangarList(ctx context.Context, req Request) (output.Table, error) {
	client, err := req.CLI.APIClient()
	if err != nil {
		return output.Table{}, err
	}

	hangars, err := core.ListHangars(ctx, client)
	if err != nil {
		return output.Table{}, err
	}

	table := output.Table{
		Columns: []string{"name", "status", "agents", "size", "repository", "createdAt"},
		Total:   len(hangars),
	}
	for _, h := range hangars {
		table.Rows = append(table.Rows, hangarRow(h))
	}
	return table, nil
}

// hangarGet reports one Hangar.
//
// One request, not two as for an application: the list already carries every
// field the detail endpoint has, so reading the detail again would fetch the
// same record twice.
func hangarGet(ctx context.Context, req Request) (output.Table, error) {
	hangar, err := targetHangar(ctx, req, "get")
	if err != nil {
		return output.Table{}, err
	}
	return output.Table{
		Single: true,
		Columns: []string{"name", "displayName", "id", "status", "agents", "size", "diskGb",
			"idleTimeoutMinutes", "repository", "branch", "createdAt"},
		Total: 1,
		Rows:  []map[string]any{hangarRow(hangar)},
	}, nil
}

// hangarRow is every field the Hangar commands report, so that a caller reading
// one has read them all.
func hangarRow(h core.Hangar) map[string]any {
	var idle any
	if h.IdleTimeoutMinutes != nil {
		idle = *h.IdleTimeoutMinutes
	}
	return map[string]any{
		"id":                 h.ID,
		"name":               h.Name,
		"displayName":        nilIfEmpty(h.DisplayName),
		"status":             h.Status,
		"paused":             h.Paused,
		"agents":             h.Agents,
		"size":               h.Size,
		"diskGb":             h.DiskGB,
		"idleTimeoutMinutes": idle,
		"ports":              hangarPortRows(h.Ports),
		"repository":         nilIfEmpty(h.Repository),
		"branch":             nilIfEmpty(h.Branch),
		"publicSource":       h.PublicSource,
		"createdAt":          nilIfEmpty(h.CreatedAt),
	}
}

// terminalReset switches off what a program in the session may have switched on
// in the reader's terminal: mouse reporting in its three forms and their
// encoding, focus events, bracketed paste, extended keys, a hidden cursor, the
// alternate screen, keypad mode and a scroll region. A session left by detaching has switched them off
// already; one that ends any other way leaves them on, and the shell the reader
// returns to receives mouse movements as typed characters. The cursor is saved
// around the steps that move it (leaving an alternate screen that was never
// entered, and resetting the scroll region), so it stays where it is.
const terminalReset = "\x1b[?1000l\x1b[?1002l\x1b[?1003l\x1b[?1006l" +
	"\x1b[?1004l\x1b[?2004l\x1b[>4;0m\x1b[?25h\x1b7\x1b[?1049l" +
	"\x1b[?1l\x1b>\x1b7\x1b[r\x1b8"

// hangarConnect joins the terminal session a Hangar keeps running.
//
// The terminal is handed over exactly as app shell hands it over. What differs
// is whose the session is: it belongs to the Hangar and goes on running after
// the reader leaves, so how to leave is said on the way in and how to come back
// is said on the way out.
func hangarConnect(ctx context.Context, req Request) (output.Table, error) {
	hangar, err := targetHangar(ctx, req, "connect")
	if err != nil {
		return output.Table{}, err
	}

	// Before the terminal check, as in app shell: a dry run opens nothing, so it
	// is the one form of this command that works in a pipe.
	if req.CLI.DryRun {
		req.CLI.Out.Note("Would join the session on %s. Nothing was sent.", hangar.Name)
		return hangarTable(hangar), nil
	}

	if err := requireTerminal(req, clierr.Step{
		Why:  "see what a session would join",
		Argv: append(connectArgv(req, hangar), "--dry-run"),
	}); err != nil {
		return output.Table{}, err
	}

	client, err := req.CLI.APIClient()
	if err != nil {
		return output.Table{}, err
	}

	session, err := core.ConnectHangar(ctx, client, hangar.ID)
	if err != nil {
		return output.Table{}, notRunning(req, err, hangar)
	}
	defer func() { _ = session.Close() }()

	req.CLI.Out.Note("Joined %s. Switch windows with Ctrl-b w. Detach with Ctrl-b d "+
		"(Ctrl-b Ctrl-b d if your own terminal already uses Ctrl-b); what runs there keeps running.", hangar.Name)
	err = runSession(ctx, req, session)
	if req.CLI.Exec.StdoutTTY {
		_, _ = io.WriteString(req.CLI.Out.Out, terminalReset)
	}
	req.CLI.Out.Note("Left %s. Rejoin: %s", hangar.Name, strings.Join(connectArgv(req, hangar), " "))
	if err != nil {
		return output.Table{}, err
	}
	// The session wrote its own bytes, so there is nothing left to render.
	return output.Table{Streamed: true}, nil
}

// targetHangar resolves the Hangar argument.
func targetHangar(ctx context.Context, req Request, verb string) (core.Hangar, error) {
	if len(req.Args) == 0 {
		return core.Hangar{}, clierr.New(clierr.KindUsage, "no Hangar given").
			WithCode("usage.missing_argument").
			WithStep("see the team's Hangars", "outplane", "hangar", "list").
			WithStep(verb+" one", "outplane", "hangar", verb, "<HANGAR_NAME>")
	}
	if strings.TrimSpace(req.Args[0]) == "" {
		return core.Hangar{}, clierr.New(clierr.KindUsage, "the Hangar argument is empty").
			WithCode("usage.empty_argument").
			WithHint("This is what an unset variable looks like.")
	}

	return resolveHangar(ctx, req, req.Args[0])
}

// resolveHangar turns a Hangar's name or id into the Hangar, translating the
// domain errors. It is targetHangar for a reference that arrives in a flag
// rather than as the first argument, as --hangar does on the env commands.
func resolveHangar(ctx context.Context, req Request, ref string) (core.Hangar, error) {
	client, err := req.CLI.APIClient()
	if err != nil {
		return core.Hangar{}, err
	}

	hangar, err := core.FindHangar(ctx, client, ref)
	if err == nil {
		return hangar, nil
	}

	// No step to list them: the names that exist are the whole answer, and the
	// hint carries them.
	var notFound *core.HangarNotFoundError
	if errors.As(err, &notFound) {
		e := clierr.New(clierr.KindNotFound, "%v", notFound).
			WithCode("hangar.not_found")
		if len(notFound.Available) > 0 {
			e = e.WithHint("This team has: %s.", strings.Join(notFound.Available, ", ")).
				WithDetail("availableHangars", notFound.Available)
		} else {
			e = e.WithHint("This team has no Hangars yet.").
				WithStep("create one", "outplane", "hangar", "create", "<NAME>")
		}
		return core.Hangar{}, e
	}

	var ambiguous *core.AmbiguousHangarError
	if errors.As(err, &ambiguous) {
		return core.Hangar{}, clierr.New(clierr.KindUsage, "%v", ambiguous).
			WithCode("hangar.ambiguous").
			WithHint("Display names are editable and need not be unique. Use one of these "+
				"names instead: %s.", strings.Join(ambiguous.Names(), ", ")).
			WithDetail("matchingHangars", ambiguous.Names())
	}

	return core.Hangar{}, err
}

// connectArgv is this command as somebody would run it again later.
func connectArgv(req Request, hangar core.Hangar) []string {
	return withTeam(req, "outplane", "hangar", "connect", hangar.Name)
}

// withTeam is a command handed over to be run later, with the team it needs.
//
// A team named with --team applies to this invocation only, so a command
// printed without it would reach whichever team is in effect next time, and a
// Hangar's name is unique only within its team. A team from anywhere else is
// still in effect then, and repeating it would only add noise.
func withTeam(req Request, argv ...string) []string {
	if team := req.CLI.Config.TeamSlug; team.Source == config.SourceFlag && team.Value != "" {
		argv = append(argv, "--team", team.Value)
	}
	return argv
}

// notRunning names the server's refusal of a Hangar that is not running.
//
// The server's message is kept. What it does not say is that this conflict,
// unlike most, can pass by itself: a Hangar still starting is about to accept.
// A paused, failed or crashed one is not, and the state read moments ago says
// which it is.
func notRunning(req Request, err error, hangar core.Hangar) error {
	e := clierr.AsError(err)
	if e == nil || e.Kind != clierr.KindConflict {
		return err
	}

	var hint string
	switch hangar.Status {
	case "queued", "building", "deploying", "ready":
		hint = "A Hangar that is still starting accepts a connection once it is ready: within a " +
			"few minutes on its first start, while its agents install, and within seconds after that."
		e.Retryable = true
	case "paused":
		hint = "A paused Hangar accepts a connection once it is resumed."
		e.Retryable = false
		e = e.WithStep("resume it", withTeam(req, "outplane", "hangar", "resume", hangar.Name)...)
	default:
		hint = "This Hangar is " + hangar.Status + " and accepts a connection once it runs again."
		e.Retryable = false
	}
	return e.WithCode("hangar.not_running").
		WithHint("%s", hint).
		WithStep("check its state", append(connectArgv(req, hangar), "--dry-run")...)
}

func hangarTable(hangar core.Hangar) output.Table {
	return output.Table{
		Single:  true,
		Columns: []string{"hangar", "status", "connected"},
		Total:   1,
		Rows: []map[string]any{{
			"hangar":    hangar.Name,
			"hangarId":  hangar.ID,
			"status":    hangar.Status,
			"connected": false,
		}},
	}
}
