package commands

import (
	"context"
	"fmt"
	"strings"

	"github.com/outplane/cli/internal/api"
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
// asked; instead an unconfirmed call exits 4 and returns the exact command
// that would proceed. That pushes the decision to whoever is accountable for
// it, which is the point of the whole protocol.
//
// Before any of that it reports what would stop the deletion. The platform
// refuses to delete an application that still has a custom domain, an attached
// volume or a deployment in flight, and finding that out after typing a
// confirmation is a worse experience than being told first.
func appDelete(ctx context.Context, req Request) (output.Table, error) {
	app, err := targetApp(ctx, req, "app", "delete")
	if err != nil {
		return output.Table{}, err
	}

	client, err := req.CLI.APIClient()
	if err != nil {
		return output.Table{}, err
	}

	blockers, err := deletionBlockers(ctx, client, app)
	if err != nil {
		return output.Table{}, err
	}

	if req.CLI.DryRun {
		describeDeletion(req, app, blockers)
		return deleteTable(app, blockers, false), nil
	}

	if err := checkDeleteConfirmed(req, app); err != nil {
		describeDeletion(req, app, blockers)
		return output.Table{}, err
	}

	if err := core.DeleteApp(ctx, client, app.ID); err != nil {
		return output.Table{}, explainFailedDelete(err, app, blockers)
	}

	req.CLI.Out.Note("Deleted %s.", app.Name)
	return deleteTable(app, nil, true), nil
}

// blocker is one reason the platform will refuse the deletion.
type blocker struct {
	Kind   string
	Detail string
}

// deletionBlockers asks the three questions the server will ask.
//
// It costs three requests, and they are worth it on a command that cannot be
// undone: without them the reader learns about an attached volume from a 400
// that names neither the volume nor which of the three rules it broke.
func deletionBlockers(ctx context.Context, client *api.Client, app core.App) ([]blocker, error) {
	var blockers []blocker

	detail, err := core.GetApp(ctx, client, app.ID)
	if err != nil {
		return nil, err
	}
	for _, endpoint := range detail.Endpoints {
		for _, domain := range endpoint.CustomDomains {
			blockers = append(blockers, blocker{Kind: "customDomain", Detail: domain})
		}
	}

	volumes, err := core.AppVolumes(ctx, client, app.ID)
	if err != nil {
		return nil, err
	}
	for _, v := range volumes {
		blockers = append(blockers, blocker{
			Kind:   "volume",
			Detail: fmt.Sprintf("%s at %s", v.Name, v.MountPath),
		})
	}

	deployments, err := core.ListDeployments(ctx, client, app.ID, app.Name)
	if err != nil {
		return nil, err
	}
	for _, d := range deployments {
		if core.Finished(d.Status) {
			continue
		}
		blockers = append(blockers, blocker{
			Kind:   "deployment",
			Detail: fmt.Sprintf("%d is %s", d.ID, d.Status),
		})
	}

	return blockers, nil
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

// describeDeletion prints what is about to go, and what stands in the way.
//
// It writes to stderr, because it is a warning about a result rather than the
// result. A dry run's actual answer is the row, which stays parseable.
func describeDeletion(req Request, app core.App, blockers []blocker) {
	req.CLI.Out.Note("%s (%s) would be deleted, along with its deployment history.", app.Name, app.ID)

	if len(blockers) == 0 {
		req.CLI.Out.Note("Nothing is holding it back.")
		return
	}

	req.CLI.Out.Note("The platform will refuse while these exist:")
	for _, b := range blockers {
		req.CLI.Out.Note("  %-13s %s", b.Kind, b.Detail)
	}
}

// explainFailedDelete turns the server's refusal into one that names the thing.
//
// The API answers all three of its rules with the same shape of 400 and a
// sentence that names no domain, no volume and no deployment. The blockers were
// already read, so the CLI can say which one.
func explainFailedDelete(err error, app core.App, blockers []blocker) error {
	e := clierr.AsError(err)
	if e == nil || e.Kind != clierr.KindUsage || len(blockers) == 0 {
		return err
	}

	details := make([]string, 0, len(blockers))
	for _, b := range blockers {
		details = append(details, b.Kind+" "+b.Detail)
	}

	return e.
		WithCode("app.delete_blocked").
		WithHint("%s still has: %s. Each has to be removed before the application can be.",
			app.Name, strings.Join(details, ", ")).
		WithDetail("blockers", blockerDetails(blockers))
}

func deleteTable(app core.App, blockers []blocker, deleted bool) output.Table {
	return output.Table{
		Single:  true,
		Columns: []string{"deleted", "app", "blockers"},
		Total:   1,
		Rows: []map[string]any{{
			"deleted":  deleted,
			"app":      app.Name,
			"appId":    app.ID,
			"blockers": blockerDetails(blockers),
		}},
	}
}

func blockerDetails(blockers []blocker) []map[string]any {
	out := make([]map[string]any, 0, len(blockers))
	for _, b := range blockers {
		out = append(out, map[string]any{"kind": b.Kind, "detail": b.Detail})
	}
	return out
}
