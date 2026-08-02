package commands

import (
	"context"
	"strconv"

	"github.com/outplane/cli/internal/clierr"
	"github.com/outplane/cli/internal/core"
	"github.com/outplane/cli/internal/output"
)

func init() {
	register("deploy list", deployList)
	register("deploy get", deployGet)
}

// deployList reports deployment history.
//
// With no application it reports the team's, which is the view that answers
// "what has been going on here" after a weekend. With one, it reports that
// application's, which is what somebody wants while they are working on it.
// The two come from different endpoints and are the same rows.
func deployList(ctx context.Context, req Request) (output.Table, error) {
	client, err := req.CLI.APIClient()
	if err != nil {
		return output.Table{}, err
	}

	limit, err := listLimit(req)
	if err != nil {
		return output.Table{}, err
	}

	app, err := optionalApp(ctx, req)
	if err != nil {
		return output.Table{}, err
	}

	// The two sources know different things, and the difference reaches the
	// output. The per-application endpoint returns everything, so the complete
	// count is known and reported. The team-wide one paginates and offers no
	// count, so one extra row is requested: getting it back is the only way to
	// learn that more exist, and it is dropped before printing.
	var deployments []core.Deployment
	total := 0
	if app.ID == "" {
		deployments, err = core.ListTeamDeployments(ctx, client, limit+1)
	} else {
		deployments, err = core.ListDeployments(ctx, client, app.ID, app.Name)
		total = len(deployments)
	}
	if err != nil {
		return output.Table{}, err
	}

	truncated := len(deployments) > limit
	if truncated {
		deployments = deployments[:limit]
	}
	if total == 0 {
		total = len(deployments)
	}

	table := deploymentsTable(deployments, app.Name == "")
	table.Total = total
	table.Truncated = truncated
	return table, nil
}

// deployGet reports one deployment.
//
// It needs the application as well as the id, because the API's path has both.
// That is why the commands which mention this one name the application in the
// step they print: `outplane deploy get 42` alone works in a linked directory
// and nowhere else.
func deployGet(ctx context.Context, req Request) (output.Table, error) {
	if len(req.Args) == 0 {
		return output.Table{}, clierr.New(clierr.KindUsage, "no deployment id given").
			WithCode("usage.missing_argument").
			WithStep("see recent deployments", "outplane", "deploy", "list")
	}

	id, err := deploymentArg(req.Args[0])
	if err != nil {
		return output.Table{}, err
	}

	// The application is the second argument here, because the id is the first.
	app, err := targetAppRef(ctx, req, argAt(req.Args, 1), "deploy", "get", req.Args[0])
	if err != nil {
		return output.Table{}, err
	}

	client, err := req.CLI.APIClient()
	if err != nil {
		return output.Table{}, err
	}

	deployment, err := core.GetDeployment(ctx, client, app.ID, id)
	if err != nil {
		return output.Table{}, err
	}
	deployment.App = app.Name

	return deploymentTable(deployment), nil
}

// optionalApp resolves an application argument that may be absent, where absent
// means the whole team rather than an error.
//
// It differs from targetApp in exactly that: targetApp refuses when there is
// nothing to act on, because a deploy or a delete has to have a target. A list
// does not.
func optionalApp(ctx context.Context, req Request) (core.App, error) {
	if len(req.Args) > 0 {
		return targetApp(ctx, req, "deploy", "list")
	}
	if id := req.CLI.Config.AppID.Value; id != "" {
		return resolveApp(ctx, req, id)
	}
	return core.App{}, nil
}

// listLimit reads --limit.
func listLimit(req Request) (int, error) {
	raw := orDefault(req.Flags.String("limit"), "20")
	limit, err := strconv.Atoi(raw)
	if err != nil || limit <= 0 {
		return 0, clierr.New(clierr.KindUsage, "--limit is not a positive number: %q", raw).
			WithCode("usage.bad_limit")
	}
	return limit, nil
}

func deploymentsTable(deployments []core.Deployment, showApp bool) output.Table {
	columns := []string{"deploymentId", "status", "startedAt", "duration"}
	if showApp {
		columns = []string{"deploymentId", "app", "status", "startedAt", "duration"}
	}

	// branch, imageRef and commitMessage are in the rows and not in the table.
	// A commit message is a paragraph and destroys a table; the other two are
	// each empty for half of all applications, since an app is built from a
	// repository or from an image and never from both.
	table := output.Table{Columns: columns, Total: len(deployments)}
	for _, d := range deployments {
		table.Rows = append(table.Rows, deploymentRow(d))
	}
	return table
}

func deploymentTable(d core.Deployment) output.Table {
	return output.Table{
		Single: true,
		Columns: []string{
			"deploymentId", "app", "status", "branch", "imageRef",
			"startedAt", "duration", "commitMessage",
		},
		Total: 1,
		Rows:  []map[string]any{deploymentRow(d)},
	}
}

func deploymentRow(d core.Deployment) map[string]any {
	return map[string]any{
		"deploymentId":  d.ID,
		"app":           nilIfEmpty(d.App),
		"appId":         nilIfEmpty(d.AppID),
		"status":        d.Status,
		"branch":        nilIfEmpty(d.Branch),
		"imageRef":      nilIfEmpty(d.ImageRef),
		"commitMessage": nilIfEmpty(d.CommitMessage),
		"startedAt":     nilIfEmpty(d.StartedAt),
		"duration":      nilIfEmpty(d.Duration),
	}
}
