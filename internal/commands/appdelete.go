package commands

import (
	"context"

	"github.com/outplane/cli/internal/clierr"
	"github.com/outplane/cli/internal/core"
	"github.com/outplane/cli/internal/output"
)

func init() {
	register("app delete", appDelete)
}

// appDelete removes an application, once somebody has said so twice.
//
// The confirmation is not a prompt. This CLI never asks a question, because a
// question has no answer in CI and an agent will answer any question it is
// asked; instead an unconfirmed call exits 4 and returns the exact command that
// would proceed. That pushes the decision to whoever is accountable for it,
// which is the point of the whole protocol.
//
// What the platform refuses to delete is deliberately not enumerated here. It
// refuses while a custom domain, an attached volume or a deployment in flight
// exists, and an earlier version of this command read all three first so it
// could name them. That was three extra requests on every deletion and, worse,
// a second copy of a rule owned by the server: the day a fourth condition is
// added, a CLI that lists three would report "nothing is in the way" and then
// fail. The server decides, this reports what it said.
func appDelete(ctx context.Context, req Request) (output.Table, error) {
	app, err := targetApp(ctx, req, "app", "delete")
	if err != nil {
		return output.Table{}, err
	}

	client, err := req.CLI.APIClient()
	if err != nil {
		return output.Table{}, err
	}

	if req.CLI.DryRun {
		req.CLI.Out.Note("%s (%s) would be deleted, along with its deployment history.", app.Name, app.ID)
		req.CLI.Out.Note("Whether it can be is decided when the deletion is attempted.")
		return deleteTable(app, false), nil
	}

	if err := checkDeleteConfirmed(req, app); err != nil {
		return output.Table{}, err
	}

	if err := core.DeleteApp(ctx, client, app.ID); err != nil {
		return output.Table{}, explainRefusal(err, app)
	}

	req.CLI.Out.Note("Deleted %s.", app.Name)
	return deleteTable(app, true), nil
}

// checkDeleteConfirmed enforces the two-step confirmation.
func checkDeleteConfirmed(req Request, app core.App) error {
	// An agent harness is refused even with both flags, and this is deliberate
	// rather than distrust. A flag is not a safety boundary: an agent that read
	// this documentation can emit any flag defined in it, so the only gate that
	// means anything is one the CLI cannot satisfy on its own behalf. The
	// harness's own approval step is that gate.
	if harness := req.CLI.Exec.AgentHarness; harness != "" {
		return confirmationRequired(app,
			"This is running under %s, where the CLI cannot be the thing that approves "+
				"a deletion. Hand the command below to whoever is accountable for it.", harness)
	}

	if !req.Flags.Bool("yes") || req.Flags.String("confirm-name") == "" {
		return confirmationRequired(app,
			"Deleting %s cannot be undone. Both --yes and --confirm-name are required, "+
				"because one flag is too easy to add by accident.", app.Name)
	}

	if given := req.Flags.String("confirm-name"); given != app.Name {
		return clierr.New(clierr.KindUsage,
			"--confirm-name says %q and the application is called %q", given, app.Name).
			WithCode("app.confirm_name_mismatch").
			WithHint("The name is the immutable one, not the display name. `outplane app get` "+
				"reports both.").
			WithDetail("expected", app.Name).
			WithDetail("given", given)
	}

	return nil
}

// confirmationRequired builds the exit-4 refusal, carrying the command that
// would proceed.
func confirmationRequired(app core.App, hint string, args ...any) error {
	return clierr.New(clierr.KindConfirmation, "deleting %s needs confirmation", app.Name).
		WithCode("confirmation.required").
		WithHint(hint, args...).
		WithConfirmCommand("outplane", "app", "delete", app.Name, "--yes", "--confirm-name", app.Name)
}

// explainRefusal adds a way forward to the server's own refusal.
//
// The message is passed through untouched, because the server names which of
// its rules was broken and is the only thing that knows the full list. What the
// CLI adds is where to look: the domains and the volumes are both reported by
// commands the reader already has.
func explainRefusal(err error, app core.App) error {
	e := clierr.AsError(err)
	if e == nil || e.Kind != clierr.KindUsage {
		return err
	}

	return e.
		WithCode("app.delete_blocked").
		WithStep("see its domains and ports", "outplane", "app", "get", app.Name).
		WithStep("see deployments still in flight", "outplane", "deploy", "list", app.Name)
}

func deleteTable(app core.App, deleted bool) output.Table {
	return output.Table{
		Single:  true,
		Columns: []string{"deleted", "app"},
		Total:   1,
		Rows: []map[string]any{{
			"deleted": deleted,
			"app":     app.Name,
			"appId":   app.ID,
		}},
	}
}
