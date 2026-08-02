package commands

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/outplane/cli/internal/api"

	"github.com/outplane/cli/internal/clierr"
	"github.com/outplane/cli/internal/core"
	"github.com/outplane/cli/internal/output"
)

func init() {
	register("env list", envList)
	register("env get", envGet)
	register("env set", envSet)
	register("env unset", envUnset)
}

// The application is named by --app in this group, not by a positional
// argument, and that is the one place the CLI departs from its own convention.
//
// It has to. `env set` and `env unset` take a list of names, so a leading
// positional would be guesswork: `env unset FOO BAR` cannot be read as either
// "two variables on the linked app" or "one variable on the app FOO" without
// inventing a rule about which words look like application names. A flag says
// which is which, and the same flag on all four commands keeps the group
// consistent with itself.

// envApp resolves which application this command acts on.
func envApp(ctx context.Context, req Request) (core.App, error) {
	if ref := req.Flags.String("app"); strings.TrimSpace(ref) != "" {
		return resolveApp(ctx, req, ref)
	}
	if id := req.CLI.Config.AppID.Value; id != "" {
		return resolveApp(ctx, req, id)
	}

	return core.App{}, clierr.New(clierr.KindUsage, "no application given, and this directory is not linked to one").
		WithCode("context.no_app").
		WithHint("Name the application with --app, or link the directory once and omit it.").
		WithStep("name an application", "outplane", "env", "list", "--app", "<APP_NAME>").
		WithStep("or link this directory", "outplane", "link", "<APP_NAME>").
		WithStep("see what this team has", "outplane", "app", "list")
}

// envList reports which variables are set, and by default not what they are.
//
// The masking is the whole design of this command. A reader almost always wants
// to know whether a key exists and roughly what it looks like; putting thirty
// credentials on a screen to answer that is a leak in a screen share, a
// terminal recording and a CI log at once. --reveal is one word away, and `env
// get` prints a single value with no ceremony, so nothing is hidden, only
// unasked-for.
func envList(ctx context.Context, req Request) (output.Table, error) {
	app, err := envApp(ctx, req)
	if err != nil {
		return output.Table{}, err
	}

	client, err := req.CLI.APIClient()
	if err != nil {
		return output.Table{}, err
	}

	vars, err := core.ListEnv(ctx, client, app.ID)
	if err != nil {
		return output.Table{}, err
	}

	reveal := req.Flags.Bool("reveal")

	table := output.Table{
		Columns: []string{"key", "value"},
		Total:   len(vars),
	}
	for _, v := range vars {
		value := any(mask(v.Value))
		if reveal {
			value = v.Value
		}
		table.Rows = append(table.Rows, map[string]any{
			"key":      v.Key,
			"value":    value,
			"revealed": reveal,
			"length":   len(v.Value),
		})
	}

	if len(vars) > 0 && !reveal {
		table.Footer = "Values are hidden. Add --reveal to print them, or read one with `outplane env get`."
	}
	return table, nil
}

// envGet prints one value and nothing else.
//
// Nothing else is the point: `DATABASE_URL=$(outplane env get DATABASE_URL)`
// has to work, so there is no table, no label and no trailing commentary on
// stdout when the format is text.
func envGet(ctx context.Context, req Request) (output.Table, error) {
	if len(req.Args) == 0 {
		return output.Table{}, clierr.New(clierr.KindUsage, "no variable name given").
			WithCode("usage.missing_argument").
			WithStep("see which variables are set", "outplane", "env", "list")
	}

	app, err := envApp(ctx, req)
	if err != nil {
		return output.Table{}, err
	}

	client, err := req.CLI.APIClient()
	if err != nil {
		return output.Table{}, err
	}

	vars, err := core.ListEnv(ctx, client, app.ID)
	if err != nil {
		return output.Table{}, err
	}

	key := req.Args[0]
	found, ok := core.FindEnv(vars, key)
	if !ok {
		e := clierr.New(clierr.KindNotFound, "%s has no variable called %s", app.Name, key).
			WithCode("env.not_found").
			WithStep("see which variables are set", "outplane", "env", "list")
		if names := keysOf(vars); len(names) > 0 {
			e = e.WithHint("It has: %s.", strings.Join(names, ", ")).WithDetail("availableKeys", names)
		} else {
			e = e.WithHint("It has no variables at all.")
		}
		return output.Table{}, e
	}

	// Text mode writes the bare value, so that command substitution gets the
	// value and not a table. Machine formats still get the object.
	if !req.CLI.Out.Ctx.Machine() {
		fmt.Fprintln(req.CLI.Out.Out, found.Value)
		return streamed(), nil
	}

	return output.Table{
		Single:  true,
		Columns: []string{"key", "value"},
		Total:   1,
		Rows:    []map[string]any{{"key": found.Key, "value": found.Value}},
	}, nil
}

// envSet adds or replaces variables and leaves the rest alone.
//
// It never sends the whole set. The API would accept a full replacement, and
// the console sends one, but a replacement means reading every variable and
// writing them all back: two callers doing that at once each save what they
// read, and whichever finishes second silently drops the other's key. Adding
// merges on the server, so a caller can only ever change what it named.
func envSet(ctx context.Context, req Request) (output.Table, error) {
	values, err := parseAssignments(req.Args)
	if err != nil {
		return output.Table{}, err
	}

	app, err := envApp(ctx, req)
	if err != nil {
		return output.Table{}, err
	}

	client, err := req.CLI.APIClient()
	if err != nil {
		return output.Table{}, err
	}

	keys := sortedKeys(values)

	if req.CLI.DryRun {
		req.CLI.Out.Note("Would set %s on %s. Nothing was sent.", strings.Join(keys, ", "), app.Name)
		return envChangeTable(app, keys, "set", false, 0), nil
	}

	if err := core.SetEnv(ctx, client, app.ID, values); err != nil {
		return output.Table{}, err
	}
	req.CLI.Out.Note("Set %s on %s.", strings.Join(keys, ", "), app.Name)

	id, err := applyChange(ctx, req, client, app, "values")
	if err != nil {
		return output.Table{}, err
	}
	return envChangeTable(app, keys, "set", true, id), nil
}

// envUnset removes variables by name.
//
// Removal is by the variable's own id rather than by writing back a set without
// it, for the same reason set does not: the caller must not be able to drop a
// key it never mentioned.
func envUnset(ctx context.Context, req Request) (output.Table, error) {
	if len(req.Args) == 0 {
		return output.Table{}, clierr.New(clierr.KindUsage, "no variable name given").
			WithCode("usage.missing_argument").
			WithStep("see which variables are set", "outplane", "env", "list")
	}

	app, err := envApp(ctx, req)
	if err != nil {
		return output.Table{}, err
	}

	client, err := req.CLI.APIClient()
	if err != nil {
		return output.Table{}, err
	}

	vars, err := core.ListEnv(ctx, client, app.ID)
	if err != nil {
		return output.Table{}, err
	}

	// Every name is resolved before anything is removed. A half-applied removal
	// is worse than a refused one: the reader has to work out which of the
	// names they typed took effect.
	targets := make([]core.EnvVar, 0, len(req.Args))
	for _, key := range req.Args {
		found, ok := core.FindEnv(vars, key)
		if !ok {
			e := clierr.New(clierr.KindNotFound, "%s has no variable called %s", app.Name, key).
				WithCode("env.not_found").
				WithStep("see which variables are set", "outplane", "env", "list")
			if names := keysOf(vars); len(names) > 0 {
				e = e.WithHint("It has: %s.", strings.Join(names, ", ")).WithDetail("availableKeys", names)
			}
			return output.Table{}, e
		}
		targets = append(targets, found)
	}

	keys := make([]string, 0, len(targets))
	for _, t := range targets {
		keys = append(keys, t.Key)
	}

	if req.CLI.DryRun {
		req.CLI.Out.Note("Would remove %s from %s. Nothing was sent.", strings.Join(keys, ", "), app.Name)
		return envChangeTable(app, keys, "unset", false, 0), nil
	}

	for _, t := range targets {
		if err := core.UnsetEnv(ctx, client, app.ID, t.ID); err != nil {
			return output.Table{}, err
		}
	}
	req.CLI.Out.Note("Removed %s from %s.", strings.Join(keys, ", "), app.Name)

	id, err := applyChange(ctx, req, client, app, "values")
	if err != nil {
		return output.Table{}, err
	}
	return envChangeTable(app, keys, "unset", true, id), nil
}

// applyChange deploys when asked, and says what happens if it is not.
//
// A change that was saved and did nothing is the failure this exists to
// prevent. The platform refreshes a running workload when it is scaled or
// paused and at no other time, so a variable, a group or a mount only reaches
// the process at the next deployment.
//
// Every command with that property offers --deploy and shares this, so the
// affordance and the wording cannot drift between them.
func applyChange(ctx context.Context, req Request, client *api.Client, app core.App, what string) (int, error) {
	if !req.Flags.Bool("deploy") {
		req.CLI.Out.Note("The running app still has the old %s. Deploy to apply the change:", what)
		req.CLI.Out.Note("  outplane deploy create %s", app.Name)
		return 0, nil
	}

	id, err := core.CreateDeployment(ctx, client, app.ID, "")
	if err != nil {
		// The change is already saved, so this is not a failed change. Say so
		// plainly rather than returning an error that reads as one.
		req.CLI.Out.Note("The change is saved, but the deployment could not be started: %v", err)
		req.CLI.Out.Note("Start it with: outplane deploy create %s", app.Name)
		return 0, nil
	}

	req.CLI.Out.Note("Deployment %d queued for %s. It is not finished.", id, app.Name)
	return id, nil
}

func envChangeTable(app core.App, keys []string, action string, changed bool, deploymentID int) output.Table {
	return output.Table{
		Single:  true,
		Columns: []string{"action", "keys", "app", "changed", "deploymentId"},
		Total:   1,
		Rows: []map[string]any{{
			"action":       action,
			"keys":         keys,
			"app":          app.Name,
			"changed":      changed,
			"deploymentId": nilIfZero(deploymentID),
		}},
	}
}

// parseAssignments reads KEY=VALUE arguments.
//
// A value containing an equals sign is kept whole, because a connection string
// or a base64 key routinely has one and splitting on the last sign instead of
// the first would mangle exactly the values people care most about.
//
// An empty list is an empty set, not an error. Whether one is required is the
// caller's rule: `env set` needs at least one and says so through its argument
// declaration, while `app create --env` is optional. Enforcing it here made the
// optional case fail with a message about setting variables.
func parseAssignments(args []string) (map[string]string, error) {
	values := make(map[string]string, len(args))
	for _, arg := range args {
		key, value, ok := strings.Cut(arg, "=")
		if !ok {
			return nil, clierr.New(clierr.KindUsage, "%q is not a KEY=VALUE pair", arg).
				WithCode("usage.bad_assignment").
				WithHint("Write it as KEY=value. To remove a variable use `outplane env unset`.")
		}

		key = strings.TrimSpace(key)
		if err := core.CheckEnvKey(key); err != nil {
			return nil, err
		}
		if err := core.CheckEnvValue(key, value); err != nil {
			return nil, err
		}
		if _, exists := values[key]; exists {
			return nil, clierr.New(clierr.KindUsage, "%s was given twice", key).
				WithCode("usage.duplicate_key").
				WithHint("The server would keep one of them and there is no rule saying which.")
		}
		values[key] = value
	}

	if len(values) > core.MaxEnvVars {
		return nil, clierr.New(clierr.KindUsage,
			"%d variables at once, and the limit is %d", len(values), core.MaxEnvVars).
			WithCode("env.too_many")
	}
	return values, nil
}

// mask shows that a value exists and how long it is, without showing it.
//
// The length is deliberate: it is what tells somebody they pasted a truncated
// key, and it reveals nothing a determined reader could use.
func mask(value string) string {
	if value == "" {
		return "(empty)"
	}
	return fmt.Sprintf("•••••••• (%d chars)", len(value))
}

func keysOf(vars []core.EnvVar) []string {
	out := make([]string, 0, len(vars))
	for _, v := range vars {
		out = append(out, v.Key)
	}
	return out
}

func sortedKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
