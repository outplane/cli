package commands

import (
	"context"
	"strconv"

	"github.com/outplane/cli/internal/clierr"
	"github.com/outplane/cli/internal/core"
	"github.com/outplane/cli/internal/output"
)

func init() {
	register("app pause", appPause)
	register("app resume", appResume)
	register("app scale", appScale)
	register("app instances", appInstances)
}

// appPause and appResume are the same operation with the flag flipped, so they
// share everything below. Two commands rather than one with a flag, because
// `app pause checkout` says what it does and `app running checkout --false`
// does not.
func appPause(ctx context.Context, req Request) (output.Table, error) {
	return setPaused(ctx, req, true)
}

func appResume(ctx context.Context, req Request) (output.Table, error) {
	return setPaused(ctx, req, false)
}

// setPaused stops or starts an application.
//
// Unlike most settings on this platform, it takes effect at once: the workload's
// replica count is derived from the flag, so the instances go away or come back
// without a deployment. The configured scale is untouched, which is what makes
// resuming return to three instances rather than to one.
func setPaused(ctx context.Context, req Request, paused bool) (output.Table, error) {
	app, err := targetApp(ctx, req, "app", verbFor(paused))
	if err != nil {
		return output.Table{}, err
	}

	client, err := req.CLI.APIClient()
	if err != nil {
		return output.Table{}, err
	}

	// The server returns early when the state already matches, so this is only
	// about telling the truth in the output: changed says whether anything
	// actually happened.
	changed := app.Paused != paused

	if req.CLI.DryRun {
		req.CLI.Out.Note("Would %s %s. Nothing was sent.", verbFor(paused), app.Name)
		return pauseTable(app, paused, false), nil
	}

	if !changed {
		req.CLI.Out.Note("%s is already %s.", app.Name, stateFor(paused))
		return pauseTable(app, paused, false), nil
	}

	if err := core.Pause(ctx, client, app.ID, paused); err != nil {
		return output.Table{}, err
	}

	if paused {
		req.CLI.Out.Note("Paused %s. Its instances are stopping; the configured scale is kept.", app.Name)
	} else {
		req.CLI.Out.Note("Resumed %s. It is starting %d instance(s).", app.Name, app.Instances)
	}
	return pauseTable(app, paused, true), nil
}

func verbFor(paused bool) string {
	if paused {
		return "pause"
	}
	return "resume"
}

func stateFor(paused bool) string {
	if paused {
		return "paused"
	}
	return "running"
}

func pauseTable(app core.App, paused, changed bool) output.Table {
	return output.Table{
		Single:  true,
		Columns: []string{"app", "paused", "instances", "changed"},
		Total:   1,
		Rows: []map[string]any{{
			"app":       app.Name,
			"appId":     app.ID,
			"paused":    paused,
			"instances": app.Instances,
			"changed":   changed,
		}},
	}
}

// appScale changes the replica count, the instance size, or both.
//
// The endpoint replaces both together and defaults the replica count to one, so
// sending only a size would quietly scale an application down. Whichever flag
// is not given is therefore filled in from the application's current setting,
// which the list already reported: this is a read-modify-write, and it is the
// only shape the API offers.
func appScale(ctx context.Context, req Request) (output.Table, error) {
	app, err := targetApp(ctx, req, "app", "scale")
	if err != nil {
		return output.Table{}, err
	}

	instances, size, err := scaleTarget(req, app)
	if err != nil {
		return output.Table{}, err
	}
	if err := core.CheckScale(instances, size); err != nil {
		return output.Table{}, err
	}

	client, err := req.CLI.APIClient()
	if err != nil {
		return output.Table{}, err
	}

	changed := instances != app.Instances || size != app.Size

	if req.CLI.DryRun {
		req.CLI.Out.Note("Would run %s as %d × %s. Nothing was sent.", app.Name, instances, size)
		return scaleTable(app, instances, size, false), nil
	}
	if !changed {
		req.CLI.Out.Note("%s already runs %d × %s.", app.Name, instances, size)
		return scaleTable(app, instances, size, false), nil
	}

	if err := core.Scale(ctx, client, app.ID, instances, size); err != nil {
		return output.Table{}, err
	}

	req.CLI.Out.Note("%s now runs %d × %s. The change is applied without a deployment.",
		app.Name, instances, size)
	return scaleTable(app, instances, size, true), nil
}

// scaleTarget resolves what the application should end up as, filling in from
// what it is now.
func scaleTarget(req Request, app core.App) (int, string, error) {
	instances, size := app.Instances, app.Size

	if raw := req.Flags.String("instances"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			return 0, "", clierr.New(clierr.KindUsage, "--instances is not a number: %q", raw).
				WithCode("usage.bad_instances")
		}
		instances = n
	}
	if raw := req.Flags.String("size"); raw != "" {
		size = raw
	}

	if instances == app.Instances && size == app.Size &&
		req.Flags.String("instances") == "" && req.Flags.String("size") == "" {
		return 0, "", clierr.New(clierr.KindUsage, "nothing to change").
			WithCode("usage.missing_argument").
			WithHint("Pass --instances, --size, or both.").
			WithStep("run three copies", "outplane", "app", "scale", app.Name, "--instances", "3").
			WithStep("give it more memory", "outplane", "app", "scale", app.Name, "--size", "op-34")
	}

	return instances, size, nil
}

func scaleTable(app core.App, instances int, size string, changed bool) output.Table {
	return output.Table{
		Single:  true,
		Columns: []string{"app", "instances", "size", "changed"},
		Total:   1,
		Rows: []map[string]any{{
			"app":               app.Name,
			"appId":             app.ID,
			"instances":         instances,
			"size":              size,
			"previousInstances": app.Instances,
			"previousSize":      app.Size,
			"changed":           changed,
		}},
	}
}

// appInstances reports what is actually running.
//
// It reads the cluster rather than the database, so it disagrees with the
// configured count exactly when something is worth looking at: mid-rollout,
// while an instance restarts, or when one cannot be scheduled.
func appInstances(ctx context.Context, req Request) (output.Table, error) {
	app, err := targetApp(ctx, req, "app", "instances")
	if err != nil {
		return output.Table{}, err
	}

	client, err := req.CLI.APIClient()
	if err != nil {
		return output.Table{}, err
	}

	instances, err := core.AppInstances(ctx, client, app.ID)
	if err != nil {
		return output.Table{}, err
	}

	table := output.Table{
		Columns: []string{"name", "phase", "ready", "startedAt"},
		Total:   len(instances),
	}
	for _, i := range instances {
		table.Rows = append(table.Rows, map[string]any{
			"name":      i.Name,
			"phase":     i.Phase,
			"ready":     i.Ready,
			"container": i.Container,
			"startedAt": nilIfEmpty(i.StartedAt),
		})
	}

	if len(instances) != app.Instances {
		table.Footer = configuredCountNote(app, len(instances))
	}
	return table, nil
}

// configuredCountNote explains a disagreement between what is running and what
// was asked for, which is the reason to run this command at all.
func configuredCountNote(app core.App, running int) string {
	if app.Paused {
		return "This application is paused, so its configured scale is not running."
	}
	return "Configured for " + strconv.Itoa(app.Instances) + ". A difference means a rollout, " +
		"a restart, or an instance that cannot start."
}
