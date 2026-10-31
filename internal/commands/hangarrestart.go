package commands

import (
	"context"
	"strings"

	"github.com/outplane/cli/internal/clierr"
	"github.com/outplane/cli/internal/core"
	"github.com/outplane/cli/internal/output"
)

func init() {
	register("hangar restart", hangarRestart)
}

// hangarRestart starts a Hangar again on a new machine, with the same disk.
//
// It is hangar delete's shape without the deletion protocol, because nothing
// stored is lost: the disk under /home/hangar is the same one afterwards. What
// is lost is what runs, the session and the agents in it, and anyone joined is
// disconnected. So the dry run says what would stop, and a real restart says
// how to tell when the Hangar is back, since the command returns before it is.
func hangarRestart(ctx context.Context, req Request) (output.Table, error) {
	hangar, err := targetHangar(ctx, req, "restart")
	if err != nil {
		return output.Table{}, err
	}

	client, err := req.CLI.APIClient()
	if err != nil {
		return output.Table{}, err
	}

	if req.CLI.DryRun {
		req.CLI.Out.Note("%s (%s) would restart on a new machine. Everything running in its terminal "+
			"would stop, agents included, and anyone connected would be disconnected. Files under "+
			"/home/hangar stay. Nothing was sent.", hangar.Name, hangar.ID)
		return hangarRestartTable(hangar, false), nil
	}

	if err := core.RestartHangar(ctx, client, hangar.ID); err != nil {
		return output.Table{}, explainHangarRestartRefusal(req, err, hangar)
	}

	req.CLI.Out.Note("Restarted %s. It is coming back on a new machine with the same disk, in under "+
		"a minute: its status reads deploying, then ready.", hangar.Name)
	req.CLI.Out.Note("Check: %s", strings.Join(hangarStatusArgv(req, hangar), " "))
	return hangarRestartTable(hangar, true), nil
}

// explainHangarRestartRefusal names the two refusals only the server can make.
//
// A Hangar deleted since it was resolved is not found. One with nothing
// running is a conflict, and unlike connect's it does not pass by itself: a
// paused Hangar starts again when it is resumed, not by being restarted, and one
// that never started stays that way. The state read moments ago says which, and
// so does the server's message for a paused one, which is kept.
func explainHangarRestartRefusal(req Request, err error, hangar core.Hangar) error {
	e := clierr.AsError(err)
	if e == nil {
		return err
	}

	switch e.Kind {
	case clierr.KindNotFound:
		return e.WithCode("hangar.not_found").
			WithStep("see the team's Hangars", withTeam(req, "outplane", "hangar", "list")...)
	case clierr.KindConflict:
		e.Retryable = false
		e = e.WithCode("hangar.not_running")
		if hangar.Paused || strings.Contains(e.Message, "paused") {
			return e.WithHint("This Hangar is paused, so it has nothing running to restart. Resume it "+
				"instead: that starts it on a new machine with the same disk.").
				WithStep("resume it", withTeam(req, "outplane", "hangar", "resume", hangar.Name)...).
				WithStep("check its state", hangarStatusArgv(req, hangar)...)
		}
		return e.WithHint("Nothing is running in this Hangar, so there is nothing to restart. One that never "+
			"started stays that way, and deleting it and creating it again is the way back.").
			WithStep("check its state", hangarStatusArgv(req, hangar)...)
	}
	return err
}

// hangarStatusArgv reads a Hangar's state, which is how to tell when a restart is
// done.
func hangarStatusArgv(req Request, hangar core.Hangar) []string {
	return withTeam(req, "outplane", "hangar", "get", hangar.Name, "--fields", "status")
}

func hangarRestartTable(hangar core.Hangar, restarted bool) output.Table {
	return output.Table{
		Single:  true,
		Columns: []string{"restarted", "hangar"},
		Total:   1,
		Rows: []map[string]any{{
			"restarted": restarted,
			"hangar":    hangar.Name,
			"hangarId":  hangar.ID,
		}},
	}
}
