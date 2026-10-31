package commands

import (
	"context"
	"strings"

	"github.com/outplane/cli/internal/clierr"
	"github.com/outplane/cli/internal/core"
	"github.com/outplane/cli/internal/output"
)

func init() {
	register("hangar pause", hangarPause)
	register("hangar resume", hangarResume)
}

// hangarPause and hangarResume are app pause and app resume for a Hangar: one
// request with the flag flipped, sent only when the state would change.
//
// What differs is what pausing costs. An application's instances come back as
// they were; a Hangar's machine is the one its terminal session runs on, so
// pausing ends everything in that session and resuming starts it on a new
// machine. The disk is the same one throughout, and so are its ports and its
// variables, which is what the notes say each way.
func hangarPause(ctx context.Context, req Request) (output.Table, error) {
	return setHangarPaused(ctx, req, true)
}

func hangarResume(ctx context.Context, req Request) (output.Table, error) {
	return setHangarPaused(ctx, req, false)
}

func setHangarPaused(ctx context.Context, req Request, paused bool) (output.Table, error) {
	hangar, err := targetHangar(ctx, req, verbFor(paused))
	if err != nil {
		return output.Table{}, err
	}

	client, err := req.CLI.APIClient()
	if err != nil {
		return output.Table{}, err
	}

	// The server returns early when the state already matches, so nothing is
	// sent then, under --dry-run or not, and changed says nothing happened.
	if hangar.Paused == paused {
		if paused {
			req.CLI.Out.Note("%s is already paused.", hangar.Name)
		} else {
			req.CLI.Out.Note("%s is not paused.", hangar.Name)
		}
		return hangarPauseTable(hangar, paused, false), nil
	}

	if req.CLI.DryRun {
		if paused {
			req.CLI.Out.Note("%s (%s) would pause. Everything running in its terminal would stop, agents "+
				"included, and anyone connected would be disconnected. Files under /home/hangar, its "+
				"ports and its variables stay. Nothing was sent.", hangar.Name, hangar.ID)
		} else {
			req.CLI.Out.Note("%s (%s) would resume on a new machine with the same disk. Nothing was sent.",
				hangar.Name, hangar.ID)
		}
		return hangarPauseTable(hangar, paused, false), nil
	}

	if err := core.SetHangarPaused(ctx, client, hangar.ID, paused); err != nil {
		return output.Table{}, explainHangarPauseRefusal(req, err, hangar)
	}

	if paused {
		req.CLI.Out.Note("Paused %s. Its machine is stopping; files under /home/hangar, its ports and its "+
			"variables stay.", hangar.Name)
		req.CLI.Out.Note("Resume: %s", strings.Join(withTeam(req, "outplane", "hangar", "resume", hangar.Name), " "))
	} else {
		req.CLI.Out.Note("Resumed %s. It is starting on a new machine with the same disk, in under a "+
			"minute. A ready status in the first few seconds can still be from before the pause.", hangar.Name)
		req.CLI.Out.Note("Check: %s", strings.Join(hangarStatusArgv(req, hangar), " "))
	}
	return hangarPauseTable(hangar, paused, true), nil
}

// explainHangarPauseRefusal names the two refusals only the server can make.
//
// A Hangar deleted since it was resolved is not found. One with no machine to
// stop or start is a conflict, and which kind of conflict depends on the state
// read moments ago: a Hangar still starting for the first time is about to have
// one, and one that never started stays that way.
func explainHangarPauseRefusal(req Request, err error, hangar core.Hangar) error {
	e := clierr.AsError(err)
	if e == nil {
		return err
	}

	switch e.Kind {
	case clierr.KindNotFound:
		return e.WithCode("hangar.not_found").
			WithStep("see the team's Hangars", withTeam(req, "outplane", "hangar", "list")...)
	case clierr.KindConflict:
		e = e.WithCode("hangar.never_started")
		switch hangar.Status {
		case "queued", "building", "deploying":
			e.Retryable = true
			return e.WithHint("It is still starting for the first time. Try again once its status reads ready.").
				WithStep("check its state", hangarStatusArgv(req, hangar)...)
		}
		e.Retryable = false
		return e.WithHint("A Hangar that never started has no machine to stop or start, and retrying "+
			"will not help. Deleting it and creating it again is the way back.").
			WithStep("delete it", withTeam(req, "outplane", "hangar", "delete", hangar.Name)...).
			WithStep("create it again", withTeam(req, "outplane", "hangar", "create", hangar.Name)...)
	}
	return err
}

func hangarPauseTable(hangar core.Hangar, paused, changed bool) output.Table {
	return output.Table{
		Single:  true,
		Columns: []string{"hangar", "paused", "changed"},
		Total:   1,
		Rows: []map[string]any{{
			"hangar":   hangar.Name,
			"hangarId": hangar.ID,
			"paused":   paused,
			"changed":  changed,
		}},
	}
}
