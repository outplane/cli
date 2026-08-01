package commands

import (
	"context"
	"fmt"
	"time"

	"github.com/outplane/cli/internal/api"
	"github.com/outplane/cli/internal/clierr"
	"github.com/outplane/cli/internal/core"
	"github.com/outplane/cli/internal/output"
)

func init() {
	register("deploy create", deployCreate)
}

// pollInterval is how often --wait and --follow ask the server what happened.
//
// Three seconds is a compromise nobody will notice either way: a build takes
// minutes, so a faster poll only adds load, and a slower one makes the command
// feel stuck at the moment it finishes.
const pollInterval = 3 * time.Second

// deployCreate starts a build and, optionally, waits for it.
//
// The important behaviour is what it does NOT do by default: it returns as soon
// as the build is queued. A queued build is not a finished deploy, and the
// registry's automation notes say so, because "queued" is the value most likely
// to be mistaken for success by something that only checks the exit code.
func deployCreate(ctx context.Context, req Request) (output.Table, error) {
	client, err := req.CLI.APIClient()
	if err != nil {
		return output.Table{}, err
	}

	app, err := resolveDeployTarget(ctx, req)
	if err != nil {
		return output.Table{}, err
	}

	imageRef := req.Flags.String("image")
	if err := checkImageAllowed(app, imageRef); err != nil {
		return output.Table{}, err
	}

	if req.CLI.DryRun {
		// Nothing is sent. The point of --dry-run here is to show which app the
		// reference resolved to, which is the part that surprises people.
		req.CLI.Out.Note("Would deploy %s. Nothing was sent.", app.Name)
		return deployTable(app, core.Deployment{Status: "not started", ImageRef: imageRef}, false), nil
	}

	id, err := core.CreateDeployment(ctx, client, app.ID, imageRef)
	if err != nil {
		return output.Table{}, err
	}

	deployment, err := core.GetDeployment(ctx, client, app.ID, id)
	if err != nil {
		// The build has started; failing to read its state afterwards must not
		// look like the deploy failed to start. Report what is certain.
		req.CLI.Out.Note("Deployment %d started, but its status could not be read: %v", id, err)
		return deployTable(app, core.Deployment{ID: id, Status: "unknown", ImageRef: imageRef}, true), nil
	}

	follow := req.Flags.Bool("follow")
	wait := req.Flags.Bool("wait") || follow

	if !wait {
		req.CLI.Out.Note("Deployment %d queued for %s. It is not finished.", id, app.Name)
		req.CLI.Out.Note("Check it with: outplane deploy get %d", id)
		return deployTable(app, deployment, true), nil
	}

	deployment, err = awaitDeployment(ctx, req, client, app, deployment, follow)
	if err != nil {
		return output.Table{}, err
	}
	return deployTable(app, deployment, true), nil
}

// resolveDeployTarget picks the application to deploy: the argument if given,
// otherwise the directory's linked app.
func resolveDeployTarget(ctx context.Context, req Request) (core.App, error) {
	if len(req.Args) > 0 {
		return resolveApp(ctx, req, req.Args[0])
	}

	if id := req.CLI.Config.AppID.Value; id != "" {
		return resolveApp(ctx, req, id)
	}

	return core.App{}, clierr.New(clierr.KindUsage, "no application given, and this directory is not linked to one").
		WithCode("context.no_app").
		WithHint("Name the application, or link the directory once and omit it afterwards.").
		WithStep("deploy a named application", "outplane", "deploy", "create", "<APP_NAME>").
		WithStep("or link this directory", "outplane", "link", "<APP_NAME>").
		WithStep("see what this team has", "outplane", "app", "list")
}

// checkImageAllowed refuses an image reference for an app that is not built
// from one.
//
// The server would refuse it too. Refusing here is worth the duplication
// because the message can name the app and say what its source actually is,
// which a round trip returning "ImageName override is only supported for
// container registry sources" cannot.
func checkImageAllowed(app core.App, imageRef string) error {
	if imageRef == "" || app.Source == "container-registry" {
		return nil
	}
	return clierr.New(clierr.KindUsage,
		"--image cannot be used with %s, which builds from %s", app.Name, app.Source).
		WithCode("deploy.image_on_git_app").
		WithHint("An image reference only means something for an app whose source is a "+
			"container registry. This one deploys whatever its repository builds.").
		WithStep("deploy the repository's current state", "outplane", "deploy", "create", app.Name)
}

// awaitDeployment blocks until the deployment reaches a state it will not
// leave, streaming build output when asked.
//
// It stops on states it recognises as final and on nothing else. A status this
// release has never seen keeps it waiting, which ends in a timeout rather than
// in a guess; see core.Finished.
func awaitDeployment(
	ctx context.Context,
	req Request,
	client *api.Client,
	app core.App,
	current core.Deployment,
	follow bool,
) (core.Deployment, error) {
	deadline, err := waitDeadline(req)
	if err != nil {
		return current, err
	}

	req.CLI.Out.Note("Waiting for deployment %d…", current.ID)

	var offset int64
	for {
		if follow {
			offset = streamLogs(ctx, req, client, current.ID, offset)
		}

		if core.Finished(current.Status) {
			return finish(req, app, current)
		}

		if time.Now().After(deadline) {
			return current, clierr.New(clierr.KindTimeout,
				"deployment %d was still %s when the timeout expired", current.ID, current.Status).
				WithCode("deploy.timeout").
				WithHint("The build is still running on the server; only this command stopped waiting.").
				WithStep("check it later", "outplane", "deploy", "get", fmt.Sprint(current.ID))
		}

		select {
		case <-ctx.Done():
			return current, clierr.New(clierr.KindInterrupted, "interrupted while waiting")
		case <-time.After(pollInterval):
		}

		next, err := core.GetDeployment(ctx, client, app.ID, current.ID)
		if err != nil {
			// A single failed poll is not a failed deploy. Keep waiting; the
			// timeout is what ends this loop when the server is truly gone.
			req.CLI.Out.Note("Could not read the deployment status, retrying: %v", err)
			continue
		}
		if next.Status != current.Status {
			req.CLI.Out.Note("  %s", next.Status)
		}
		current = next
	}
}

// finish reports the outcome and turns a failed deployment into a failed
// command.
//
// Exiting non-zero on a failed build is the whole point of --wait: a pipeline
// that deployed something broken must not go green.
func finish(req Request, app core.App, d core.Deployment) (core.Deployment, error) {
	if core.Succeeded(d.Status) {
		req.CLI.Out.Note("Deployment %d is ready.", d.ID)
		return d, nil
	}

	return d, clierr.New(clierr.KindUpstream, "deployment %d ended as %s", d.ID, d.Status).
		WithCode("deploy.failed").
		WithHint("The build or the release did not complete. Its output says why.").
		WithStep("read the build output", "outplane", "deploy", "logs", fmt.Sprint(d.ID)).
		WithDetail("app", app.Name).
		WithDetail("status", d.Status)
}

// streamLogs prints whatever build output is new and returns the next offset.
//
// Build logs are polled by byte offset rather than streamed, so the offset
// returned by one call is what makes the next return only new bytes. Output
// goes to stderr: it is progress, not the command's result, and stdout has to
// stay parseable.
func streamLogs(ctx context.Context, req Request, client *api.Client, id int, offset int64) int64 {
	chunk, err := core.BuildLogs(ctx, client, id, offset)
	if err != nil {
		// Logs are not available for every deployment, and a build that has not
		// started producing output is not an error worth failing over.
		return offset
	}
	if chunk.Text != "" {
		fmt.Fprint(req.CLI.Out.Err, chunk.Text)
	}
	return chunk.Offset
}

// waitDeadline resolves --timeout into a point in time.
func waitDeadline(req Request) (time.Time, error) {
	raw := req.Flags.String("timeout")
	if raw == "" {
		raw = "20m"
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return time.Time{}, clierr.New(clierr.KindUsage, "--timeout is not a duration: %q", raw).
			WithCode("usage.bad_duration").
			WithHint("Use a Go duration such as 90s, 10m or 1h.")
	}
	return time.Now().Add(d), nil
}

func deployTable(app core.App, d core.Deployment, changed bool) output.Table {
	return output.Table{
		Single: true,
		// app is deliberately absent from the columns: it is a nested object and
		// renders as an unreadable map in a table, while the reader just typed
		// the app's name. It stays in the row, so --json and --fields still see
		// it.
		Columns: []string{
			"deploymentId", "status", "branch",
			"imageRef", "commitMessage", "startedAt", "duration", "changed",
		},
		Total: 1,
		Rows: []map[string]any{{
			"deploymentId":  nilIfZero(d.ID),
			"app":           map[string]any{"id": app.ID, "name": app.Name},
			"status":        d.Status,
			"branch":        nilIfEmpty(d.Branch),
			"imageRef":      nilIfEmpty(d.ImageRef),
			"commitMessage": nilIfEmpty(d.CommitMessage),
			"startedAt":     nilIfEmpty(d.StartedAt),
			"duration":      nilIfEmpty(d.Duration),
			"changed":       changed,
		}},
	}
}

// nilIfZero renders an unassigned deployment id as null rather than 0, so a
// dry run cannot be mistaken for a deployment numbered zero.
func nilIfZero(n int) any {
	if n == 0 {
		return nil
	}
	return n
}
