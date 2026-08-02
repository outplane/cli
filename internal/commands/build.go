package commands

import (
	"context"
	"strings"

	"github.com/outplane/cli/internal/clierr"
	"github.com/outplane/cli/internal/core"
	"github.com/outplane/cli/internal/output"
)

func init() {
	register("build get", buildGet)
	register("build set", buildSet)
	register("app rename", appRename)
}

// How an application is built, and what it is called.
//
// Two unrelated commands share this file because they share the one thing worth
// knowing about both: the endpoints behind them write every field they are
// given, so a partial change is a read-modify-write. `build set` reads the
// current settings and sends them back with the change applied, exactly as the
// port commands do, and for the same reason. Sending only what changed would
// clear the start command every time somebody changed a filter.
//
// `app rename` is here rather than with the other app commands because it is
// the same endpoint family and the same shape, and because it has one thing to
// say that belongs next to this: it changes the label and not the address.

func buildGet(ctx context.Context, req Request) (output.Table, error) {
	app, err := flagApp(ctx, req, "build", "get")
	if err != nil {
		return output.Table{}, err
	}

	client, err := req.CLI.APIClient()
	if err != nil {
		return output.Table{}, err
	}

	settings, err := core.GetBuildSettings(ctx, client, app.ID)
	if err != nil {
		return output.Table{}, err
	}

	return buildTable(app, settings, false, 0, true), nil
}

// buildSet changes some of the settings and carries the rest through.
func buildSet(ctx context.Context, req Request) (output.Table, error) {
	change := buildChange(req)
	if (change == core.BuildChange{}) {
		return output.Table{}, clierr.New(clierr.KindUsage, "nothing to change").
			WithCode("usage.no_change").
			WithHint("Name at least one setting. An empty value clears a setting that "+
				"can be cleared: --start-command \"\" removes the start command.").
			WithStep("see the current settings", "outplane", "build", "get").
			WithStep("build with buildpacks instead", "outplane", "build", "set", "--method", "buildpack")
	}

	app, err := flagApp(ctx, req, "build", "set")
	if err != nil {
		return output.Table{}, err
	}

	client, err := req.CLI.APIClient()
	if err != nil {
		return output.Table{}, err
	}

	// Read late: what comes back is what will be sent again, so the shorter the
	// gap the smaller the chance of overwriting somebody else's change.
	current, err := core.GetBuildSettings(ctx, client, app.ID)
	if err != nil {
		return output.Table{}, err
	}

	updated := current.Apply(change)
	if err := updated.Check(change); err != nil {
		return output.Table{}, err
	}

	if req.CLI.DryRun {
		req.CLI.Out.Note("Would change %s on %s. Nothing was sent.",
			strings.Join(changedNames(change), ", "), app.Name)
		describeBuild(req, updated)
		return buildTable(app, updated, false, 0, false), nil
	}

	if err := core.SaveBuildSettings(ctx, client, app.ID, updated); err != nil {
		return output.Table{}, err
	}
	req.CLI.Out.Note("Changed %s on %s.", strings.Join(changedNames(change), ", "), app.Name)

	// The wording differs from every other command that says this, because the
	// difference is real: for a repository these settings are read when an image
	// is built, so what is stale is the image rather than the configuration. For
	// a registry application nothing is built at all and the deployment pulls,
	// so saying "the next build" there would describe something that never
	// happens.
	id := 0
	if req.Flags.Bool("deploy") {
		if id, err = applyChange(ctx, req, client, app, "build settings"); err != nil {
			return output.Table{}, err
		}
	} else if updated.FromRegistry {
		req.CLI.Out.Note("The running app still has the old image. Deploy to pull the new one:")
		req.CLI.Out.Note("  outplane deploy create %s", app.Name)
	} else {
		req.CLI.Out.Note("The next build uses them. The running app was built with the old ones:")
		req.CLI.Out.Note("  outplane deploy create %s", app.Name)
	}

	return buildTable(app, updated, true, id, false), nil
}

// buildChange reads the flags into a change, keeping "not written" apart from
// "written as empty".
func buildChange(req Request) core.BuildChange {
	var change core.BuildChange
	if req.Given("method") {
		change.BuildMethod = flagValue(req, "method")
	}
	if req.Given("dir") {
		change.Directory = flagValue(req, "dir")
	}
	if req.Given("start-command") {
		change.StartCommand = flagValue(req, "start-command")
	}
	if req.Given("include-paths") {
		change.IncludePaths = joinedFlag(req, "include-paths")
	}
	if req.Given("ignore-paths") {
		change.IgnorePaths = joinedFlag(req, "ignore-paths")
	}
	if req.Given("image") {
		change.Image = flagValue(req, "image")
	}
	return change
}

func flagValue(req Request, name string) *string {
	v := strings.TrimSpace(req.Flags.String(name))
	return &v
}

// joinedFlag turns a repeatable pattern flag into the one string the API
// stores.
//
// The platform keeps a filter as a single field with one pattern per line, and
// a repeated flag is how a shell writes a list without anybody having to type a
// newline. A single empty value clears the filter, which is the only way to say
// "none" with a repeatable flag.
func joinedFlag(req Request, name string) *string {
	values := req.Flags.Strings(name)
	patterns := make([]string, 0, len(values))
	for _, v := range values {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			patterns = append(patterns, trimmed)
		}
	}
	joined := strings.Join(patterns, "\n")
	return &joined
}

// changedNames is what to call the change in a sentence.
func changedNames(c core.BuildChange) []string {
	named := make([]string, 0, 6)
	for _, f := range []struct {
		name  string
		given bool
	}{
		{"the build method", c.BuildMethod != nil},
		{"the directory", c.Directory != nil},
		{"the start command", c.StartCommand != nil},
		{"the include filter", c.IncludePaths != nil},
		{"the ignore filter", c.IgnorePaths != nil},
		{"the image", c.Image != nil},
	} {
		if f.given {
			named = append(named, f.name)
		}
	}
	return named
}

// describeBuild prints the settings a dry run would write, since "nothing was
// sent" on its own does not say what would have been.
func describeBuild(req Request, s core.BuildSettings) {
	if s.FromRegistry {
		req.CLI.Out.Note("  image          %s", orNone(s.Image))
		req.CLI.Out.Note("  start command  %s", orNone(s.StartCommand))
		return
	}
	req.CLI.Out.Note("  build method   %s", s.BuildMethod)
	req.CLI.Out.Note("  directory      %s", orNone(s.Directory))
	req.CLI.Out.Note("  start command  %s", orNone(s.StartCommand))
	req.CLI.Out.Note("  build only on  %s", orNone(oneLine(s.IncludePaths)))
	req.CLI.Out.Note("  skip builds on %s", orNone(oneLine(s.IgnorePaths)))
}

// appRename changes the label an application is shown under.
func appRename(ctx context.Context, req Request) (output.Table, error) {
	if len(req.Args) == 0 || strings.TrimSpace(req.Args[0]) == "" {
		return output.Table{}, clierr.New(clierr.KindUsage, "no display name given").
			WithCode("usage.missing_argument").
			WithHint("This changes the label only. The name in the address is fixed when "+
				"the application is created and nothing changes it.").
			WithStep("rename the linked app", "outplane", "app", "rename", "<DISPLAY_NAME>").
			WithStep("rename another one", "outplane", "app", "rename", "<DISPLAY_NAME>",
				"--app", "<APP_NAME>")
	}

	displayName := strings.TrimSpace(req.Args[0])
	if err := core.CheckDisplayName(displayName); err != nil {
		return output.Table{}, err
	}

	app, err := flagApp(ctx, req, "app", "rename")
	if err != nil {
		return output.Table{}, err
	}

	client, err := req.CLI.APIClient()
	if err != nil {
		return output.Table{}, err
	}

	if req.CLI.DryRun {
		req.CLI.Out.Note("Would show %s as %q. Nothing was sent.", app.Name, displayName)
		return renameTable(app, displayName, false), nil
	}

	if err := core.SetDisplayName(ctx, client, app.ID, displayName); err != nil {
		return output.Table{}, err
	}

	req.CLI.Out.Note("%s is now shown as %q.", app.Name, displayName)
	// Said every time, not only when it looks like somebody expected otherwise.
	// The address is the single thing people assume a rename changes.
	req.CLI.Out.Note("Its address is unchanged: the name in a URL is fixed at creation.")

	return renameTable(app, displayName, true), nil
}

func buildTable(app core.App, s core.BuildSettings, changed bool, deploymentID int, read bool) output.Table {
	columns := []string{"buildMethod", "directory", "startCommand", "includePaths", "ignorePaths"}
	if s.FromRegistry {
		// Nothing is built, so the fields that describe a build would be noise
		// on every row of every registry application.
		columns = []string{"image", "startCommand"}
	}
	if !read {
		columns = append(columns, "changed")
	}

	return output.Table{
		Single:  true,
		Columns: columns,
		Total:   1,
		Rows: []map[string]any{{
			"app":          app.Name,
			"appId":        app.ID,
			"source":       sourceKind(s),
			"buildMethod":  s.BuildMethod,
			"directory":    nilIfEmpty(s.Directory),
			"startCommand": nilIfEmpty(s.StartCommand),
			"includePaths": nilIfEmpty(s.IncludePaths),
			"ignorePaths":  nilIfEmpty(s.IgnorePaths),
			"image":        nilIfEmpty(s.Image),
			"changed":      changed,
			"deploymentId": nilIfZero(deploymentID),
		}},
	}
}

func renameTable(app core.App, displayName string, changed bool) output.Table {
	return output.Table{
		Single:  true,
		Columns: []string{"app", "displayName", "changed"},
		Total:   1,
		Rows: []map[string]any{{
			// name is repeated as app because the two are the point of this
			// command: one of them changed and the other could not.
			"app":         app.Name,
			"appId":       app.ID,
			"displayName": displayName,
			"changed":     changed,
		}},
	}
}

func sourceKind(s core.BuildSettings) string {
	if s.FromRegistry {
		return "container-registry"
	}
	return "repository"
}

// oneLine renders a filter for a single line of prose, since it is stored with
// one pattern per line and a note is one line.
func oneLine(patterns string) string {
	return strings.Join(strings.Split(patterns, "\n"), ", ")
}

func orNone(s string) string {
	if strings.TrimSpace(s) == "" {
		return "(none)"
	}
	return s
}
