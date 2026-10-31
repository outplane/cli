package commands

import (
	"context"
	"strings"

	"github.com/outplane/cli/internal/clierr"
	"github.com/outplane/cli/internal/core"
	"github.com/outplane/cli/internal/output"
)

func init() {
	register("hangar update", hangarUpdate)
}

// hangarUpdate changes a Hangar's idle timeout.
//
// The flag is read and checked before the Hangar is resolved, as build set
// checks that something was named: a mistake in it costs no request. The
// endpoint writes the setting alone, so nothing restarts and nothing pauses
// here; the platform's next idle check reads the new value.
func hangarUpdate(ctx context.Context, req Request) (output.Table, error) {
	if !req.Given("idle-timeout") {
		name := "<HANGAR_NAME>"
		if len(req.Args) > 0 && strings.TrimSpace(req.Args[0]) != "" {
			name = req.Args[0]
		}
		return output.Table{}, clierr.New(clierr.KindUsage, "nothing to change").
			WithCode("usage.no_change").
			WithHint("Name the idle timeout to set: %s.", strings.Join(core.IdleTimeoutChoices(), ", ")).
			WithStep("see the current one", withTeam(req, "outplane", "hangar", "get", name, "--fields", "idleTimeoutMinutes")...).
			WithStep("set one", withTeam(req, "outplane", "hangar", "update", name, "--idle-timeout", "1h")...)
	}

	minutes, err := parseIdleTimeout(req.Flags.String("idle-timeout"))
	if err != nil {
		return output.Table{}, err
	}
	if err := core.CheckHangarIdleTimeout(minutes); err != nil {
		return output.Table{}, err
	}

	hangar, err := targetHangar(ctx, req, "update")
	if err != nil {
		return output.Table{}, err
	}

	client, err := req.CLI.APIClient()
	if err != nil {
		return output.Table{}, err
	}

	label := core.IdleTimeoutLabel(minutes)
	if sameIdleTimeout(hangar.IdleTimeoutMinutes, minutes) {
		if minutes == nil {
			req.CLI.Out.Note("%s already never pauses on its own.", hangar.Name)
		} else {
			req.CLI.Out.Note("%s already pauses after %s idle.", hangar.Name, label)
		}
		return hangarUpdateTable(hangar, minutes, false), nil
	}

	if req.CLI.DryRun {
		req.CLI.Out.Note("Would change the idle timeout on %s from %s to %s. Nothing was sent.",
			hangar.Name, core.IdleTimeoutLabel(hangar.IdleTimeoutMinutes), label)
		return hangarUpdateTable(hangar, minutes, false), nil
	}

	if err := core.SetHangarIdleTimeout(ctx, client, hangar.ID, minutes); err != nil {
		return output.Table{}, explainHangarUpdateRefusal(req, err)
	}

	if minutes == nil {
		req.CLI.Out.Note("%s no longer pauses on its own. Pause it yourself: %s", hangar.Name,
			strings.Join(withTeam(req, "outplane", "hangar", "pause", hangar.Name), " "))
	} else {
		req.CLI.Out.Note("%s now pauses once it has been idle for %s, with no output in its terminal and "+
			"no key from anyone connected. It is checked every 5 minutes.", hangar.Name, label)
	}
	if hangar.Paused {
		req.CLI.Out.Note("It is paused now, and stays paused until it is resumed: %s",
			strings.Join(withTeam(req, "outplane", "hangar", "resume", hangar.Name), " "))
	}
	return hangarUpdateTable(hangar, minutes, true), nil
}

// explainHangarUpdateRefusal names what only the server can refuse. A Hangar
// deleted since it was resolved is not found. A length the server refuses
// although this release allows it means the two disagree about the list, and
// the server's message, kept, names the lengths it takes.
func explainHangarUpdateRefusal(req Request, err error) error {
	e := clierr.AsError(err)
	if e == nil {
		return err
	}

	switch {
	case e.Kind == clierr.KindNotFound:
		return e.WithCode("hangar.not_found").
			WithStep("see the team's Hangars", withTeam(req, "outplane", "hangar", "list")...)
	case e.Kind == clierr.KindUsage && e.Code == "":
		return e.WithCode("hangar.idle_timeout_invalid")
	}
	return err
}

// sameIdleTimeout compares two idle timeouts, where nil is never.
func sameIdleTimeout(a, b *int) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func hangarUpdateTable(hangar core.Hangar, minutes *int, changed bool) output.Table {
	var idle any
	if minutes != nil {
		idle = *minutes
	}
	return output.Table{
		Single:  true,
		Columns: []string{"hangar", "idleTimeoutMinutes", "changed"},
		Total:   1,
		Rows: []map[string]any{{
			"hangar":             hangar.Name,
			"hangarId":           hangar.ID,
			"idleTimeoutMinutes": idle,
			"changed":            changed,
		}},
	}
}
