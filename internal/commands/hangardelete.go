package commands

import (
	"context"

	"github.com/outplane/cli/internal/clierr"
	"github.com/outplane/cli/internal/core"
	"github.com/outplane/cli/internal/output"
)

func init() {
	register("hangar delete", hangarDelete)
}

// hangarDelete removes a Hangar and its disk, under app delete's protocol.
//
// The protocol is the same because the loss is the same shape or worse: what
// agents wrote on the disk and never pushed anywhere exists nowhere else, and
// nothing recreates it. So the command never prompts, an unconfirmed call exits
// 4 carrying the command that would proceed, and an agent harness is refused
// whatever flags it passes.
func hangarDelete(ctx context.Context, req Request) (output.Table, error) {
	hangar, err := targetHangar(ctx, req, "delete")
	if err != nil {
		return output.Table{}, err
	}

	client, err := req.CLI.APIClient()
	if err != nil {
		return output.Table{}, err
	}

	if req.CLI.DryRun {
		req.CLI.Out.Note("%s (%s) would be deleted, along with its disk and everything on it.", hangar.Name, hangar.ID)
		return hangarDeleteTable(hangar, false), nil
	}

	if err := checkHangarDeleteConfirmed(req, hangar); err != nil {
		return output.Table{}, err
	}

	if err := core.DeleteHangar(ctx, client, hangar.ID); err != nil {
		return output.Table{}, err
	}

	req.CLI.Out.Note("Deleted %s.", hangar.Name)
	return hangarDeleteTable(hangar, true), nil
}

// checkHangarDeleteConfirmed is checkDeleteConfirmed, for a Hangar. See there
// for why an agent harness is refused even with both flags.
func checkHangarDeleteConfirmed(req Request, hangar core.Hangar) error {
	if harness := req.CLI.Exec.AgentHarness; harness != "" {
		return hangarConfirmation(req, hangar,
			"This is running under %s, where the CLI cannot be the thing that approves "+
				"a deletion. Hand the command below to whoever is accountable for it.", harness)
	}

	if !req.Flags.Bool("yes") || req.Flags.String("confirm-name") == "" {
		return hangarConfirmation(req, hangar,
			"Deleting %s destroys its disk and everything on it, and nothing restores it. Both "+
				"--yes and --confirm-name are required, because one flag is too easy to add by accident.",
			hangar.Name)
	}

	if given := req.Flags.String("confirm-name"); given != hangar.Name {
		return clierr.New(clierr.KindUsage,
			"--confirm-name says %q and the Hangar is called %q", given, hangar.Name).
			WithCode("hangar.confirm_name_mismatch").
			WithHint("The name is the immutable one, not the display name. `outplane hangar get` "+
				"reports both.").
			WithDetail("expected", hangar.Name).
			WithDetail("given", given)
	}

	return nil
}

func hangarConfirmation(req Request, hangar core.Hangar, hint string, args ...any) error {
	return clierr.New(clierr.KindConfirmation, "deleting %s needs confirmation", hangar.Name).
		WithCode("confirmation.required").
		WithHint(hint, args...).
		WithConfirmCommand(withTeam(req, "outplane", "hangar", "delete", hangar.Name,
			"--yes", "--confirm-name", hangar.Name)...)
}

func hangarDeleteTable(hangar core.Hangar, deleted bool) output.Table {
	return output.Table{
		Single:  true,
		Columns: []string{"deleted", "hangar"},
		Total:   1,
		Rows: []map[string]any{{
			"deleted":  deleted,
			"hangar":   hangar.Name,
			"hangarId": hangar.ID,
		}},
	}
}
