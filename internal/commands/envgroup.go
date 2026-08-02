package commands

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/outplane/cli/internal/clierr"
	"github.com/outplane/cli/internal/core"
	"github.com/outplane/cli/internal/output"
)

func init() {
	register("env group list", envGroupList)
	register("env group get", envGroupGet)
	register("env group create", envGroupCreate)
	register("env group set", envGroupSet)
	register("env group delete", envGroupDelete)
	register("env group assign", envGroupAssign)
	register("env group unassign", envGroupUnassign)
}

// envGroupList reports the team's shared variable sets.
func envGroupList(ctx context.Context, req Request) (output.Table, error) {
	client, err := req.CLI.APIClient()
	if err != nil {
		return output.Table{}, err
	}

	groups, err := core.ListEnvGroups(ctx, client)
	if err != nil {
		return output.Table{}, err
	}

	table := output.Table{
		Columns: []string{"name", "variables", "assignments", "scope"},
		Total:   len(groups),
	}
	for _, g := range groups {
		table.Rows = append(table.Rows, groupRow(g))
	}
	return table, nil
}

// envGroupGet reports one group and, by default, only the names of what is in
// it.
//
// The masking rule is `env list`'s, for the same reason: a group exists to hold
// the same credential for several applications, so printing one prints it once
// for every reader of the screen.
func envGroupGet(ctx context.Context, req Request) (output.Table, error) {
	group, err := targetEnvGroup(ctx, req, "get")
	if err != nil {
		return output.Table{}, err
	}

	client, err := req.CLI.APIClient()
	if err != nil {
		return output.Table{}, err
	}

	// The list endpoint does not carry the entries, so a group resolved by name
	// has to be read again by id.
	group, err = core.GetEnvGroup(ctx, client, group.ID)
	if err != nil {
		return output.Table{}, err
	}

	// The applications are the answer to "what do I deploy now", which nothing
	// else reports and which the group's own commands deliberately do not do
	// for the reader: a group can have dozens of users and deploying them all
	// from here would be a bigger action than the one they asked for.
	if req.Flags.Bool("apps") {
		return assignedAppsTable(group), nil
	}

	reveal := req.Flags.Bool("reveal")

	table := output.Table{
		Columns: []string{"key", "value"},
		Total:   len(group.Entries),
	}
	for _, e := range group.Entries {
		value := any(mask(e.Value))
		if reveal {
			value = e.Value
		}
		table.Rows = append(table.Rows, map[string]any{
			"key":      e.Key,
			"value":    value,
			"revealed": reveal,
			"length":   len(e.Value),
		})
	}

	table.Footer = groupSummary(group, reveal)
	return table, nil
}

// assignedAppsTable lists the applications using a group.
func assignedAppsTable(g core.EnvGroup) output.Table {
	table := output.Table{
		Columns: []string{"app", "appId"},
		Total:   len(g.AssignedApps),
	}
	for _, a := range g.AssignedApps {
		table.Rows = append(table.Rows, map[string]any{
			"app":          a.AppName,
			"appId":        a.AppID,
			"assignmentId": a.ID,
		})
	}
	if len(g.AssignedApps) > 0 {
		table.Footer = "Each of these picks up a change to " + g.Name +
			" at its next deployment. Nothing deploys them for you."
	}
	return table
}

// groupSummary is the sentence that says what the table of variables belongs
// to, since the rows themselves carry no group.
func groupSummary(g core.EnvGroup, reveal bool) string {
	summary := g.Name + " is used " + scopeOf(g) + ", by " + plural(g.Assignments, "application") + "."
	if names := appNamesOf(g); names != "" {
		summary = g.Name + " is used " + scopeOf(g) + ", by " + names + "."
	}
	if g.Description != "" {
		summary = g.Description + ". " + summary
	}
	if len(g.Entries) > 0 && !reveal {
		summary += " Values are hidden; add --reveal to print them."
	}
	return summary
}

// envGroupCreate makes a group.
func envGroupCreate(ctx context.Context, req Request) (output.Table, error) {
	if len(req.Args) == 0 {
		return output.Table{}, clierr.New(clierr.KindUsage, "no name given").
			WithCode("usage.missing_argument").
			WithStep("create a group", "outplane", "env", "group", "create", "<NAME>", "--var", "KEY=value")
	}

	name := req.Args[0]
	if err := core.CheckEnvGroupName(name); err != nil {
		return output.Table{}, err
	}

	values, err := parseAssignments(req.Flags.Strings("var"))
	if err != nil {
		return output.Table{}, err
	}

	group := core.EnvGroup{
		Name:         name,
		Description:  req.Flags.String("description"),
		UseInBuild:   req.Flags.Bool("build"),
		UseInRuntime: !req.Flags.Bool("build-only"),
	}
	if err := checkScope(group); err != nil {
		return output.Table{}, err
	}

	client, err := req.CLI.APIClient()
	if err != nil {
		return output.Table{}, err
	}

	if req.CLI.DryRun {
		req.CLI.Out.Note("Would create %s with %s, used %s. Nothing was sent.",
			name, plural(len(values), "variable"), scopeOf(group))
		return groupSingle(group, false), nil
	}

	created, err := core.SaveEnvGroup(ctx, client, "", group, values)
	if err != nil {
		return output.Table{}, err
	}

	req.CLI.Out.Note("Created %s with %s, used %s.",
		created.Name, plural(len(values), "variable"), scopeOf(created))
	req.CLI.Out.Note("Assign it to an application with: outplane env group assign %s --app <APP_NAME>", created.Name)
	return groupSingle(created, true), nil
}

// envGroupSet changes a group's variables, and its settings.
//
// It reads the group first and sends everything back, because the API replaces
// a group whole: name, description, scope and every variable in one request.
// That is a read-modify-write with no version check, and unlike the
// per-application case there is no merging endpoint to use instead. The note
// says so; two people editing the same group at the same time will lose one of
// the edits.
func envGroupSet(ctx context.Context, req Request) (output.Table, error) {
	group, err := targetEnvGroup(ctx, req, "set")
	if err != nil {
		return output.Table{}, err
	}

	client, err := req.CLI.APIClient()
	if err != nil {
		return output.Table{}, err
	}

	group, err = core.GetEnvGroup(ctx, client, group.ID)
	if err != nil {
		return output.Table{}, err
	}

	values := core.EntryValues(group.Entries)

	added, err := parseAssignments(req.Flags.Strings("var"))
	if err != nil {
		return output.Table{}, err
	}
	for k, v := range added {
		values[k] = v
	}

	removed, err := removeKeys(req.Flags.Strings("unset"), values)
	if err != nil {
		return output.Table{}, err
	}

	if desc := req.Flags.String("description"); desc != "" {
		group.Description = desc
	}
	if req.Flags.Bool("build") {
		group.UseInBuild = true
	}
	if req.Flags.Bool("no-build") {
		group.UseInBuild = false
	}
	if req.Flags.Bool("build-only") {
		group.UseInRuntime = false
	}
	if req.Flags.Bool("runtime") {
		group.UseInRuntime = true
	}
	if err := checkScope(group); err != nil {
		return output.Table{}, err
	}

	if len(added) == 0 && len(removed) == 0 && !scopeOrTextChanged(req) {
		return output.Table{}, clierr.New(clierr.KindUsage, "nothing to change").
			WithCode("usage.missing_argument").
			WithHint("Pass --var, --unset, --description, or a scope flag.").
			WithStep("add a variable", "outplane", "env", "group", "set", group.Name, "--var", "KEY=value")
	}

	if req.CLI.DryRun {
		req.CLI.Out.Note("Would leave %s with %s. Nothing was sent.",
			group.Name, plural(len(values), "variable"))
		return groupSingle(group, false), nil
	}

	updated, err := core.SaveEnvGroup(ctx, client, group.ID, group, values)
	if err != nil {
		return output.Table{}, err
	}

	req.CLI.Out.Note("%s now has %s.", updated.Name, plural(len(values), "variable"))
	if updated.Assignments > 0 {
		// Phrased around the count rather than agreeing with it: "the 1
		// application ... pick this up" is the sort of sentence that makes
		// output read as machine-written.
		req.CLI.Out.Note("The change reaches %s at the next deployment.",
			plural(updated.Assignments, "application"))
	}
	return groupSingle(updated, true), nil
}

// removeKeys applies --unset, refusing a key that is not there rather than
// pretending to remove it.
func removeKeys(keys []string, values map[string]string) ([]string, error) {
	var removed []string
	for _, key := range keys {
		key = strings.TrimSpace(key)
		found := ""
		for existing := range values {
			if strings.EqualFold(existing, key) {
				found = existing
				break
			}
		}
		if found == "" {
			return nil, clierr.New(clierr.KindNotFound, "the group has no variable called %s", key).
				WithCode("env.not_found")
		}
		delete(values, found)
		removed = append(removed, found)
	}
	return removed, nil
}

// envGroupDelete destroys a group.
//
// Destructive rather than a write: the variables go with it, and every
// application assigned to it loses them at its next deployment. Nothing here
// says which applications those are, so the command reports the count before it
// asks for confirmation.
func envGroupDelete(ctx context.Context, req Request) (output.Table, error) {
	group, err := targetEnvGroup(ctx, req, "delete")
	if err != nil {
		return output.Table{}, err
	}

	client, err := req.CLI.APIClient()
	if err != nil {
		return output.Table{}, err
	}

	if req.CLI.DryRun {
		req.CLI.Out.Note("%s and its %s would be destroyed.", group.Name, plural(group.Variables, "variable"))
		if group.Assignments > 0 {
			// Phrased around the count rather than agreeing with it, as elsewhere.
			req.CLI.Out.Note("The platform will refuse: it is still used by %s. Unassign it first.",
				plural(group.Assignments, "application"))
		}
		return groupSingle(group, false), nil
	}

	if err := checkGroupConfirmed(req, group); err != nil {
		return output.Table{}, err
	}

	if err := core.DeleteEnvGroup(ctx, client, group.ID); err != nil {
		return output.Table{}, err
	}

	req.CLI.Out.Note("Deleted %s.", group.Name)
	return groupSingle(group, true), nil
}

func checkGroupConfirmed(req Request, group core.EnvGroup) error {
	if harness := req.CLI.Exec.AgentHarness; harness != "" {
		return groupConfirmation(group,
			"This is running under %s, where the CLI cannot be the thing that approves "+
				"destroying shared configuration. Hand the command below to whoever is "+
				"accountable for it.", harness)
	}

	if !req.Flags.Bool("yes") || req.Flags.String("confirm-name") == "" {
		return groupConfirmation(group,
			"Deleting %s destroys its %s, and nothing restores them. Both --yes and "+
				"--confirm-name are required.", group.Name, plural(group.Variables, "variable"))
	}

	if given := req.Flags.String("confirm-name"); given != group.Name {
		return clierr.New(clierr.KindUsage,
			"--confirm-name says %q and the group is called %q", given, group.Name).
			WithCode("envgroup.confirm_name_mismatch").
			WithDetail("expected", group.Name).
			WithDetail("given", given)
	}
	return nil
}

func groupConfirmation(group core.EnvGroup, hint string, args ...any) error {
	return clierr.New(clierr.KindConfirmation, "deleting %s needs confirmation", group.Name).
		WithCode("confirmation.required").
		WithHint(hint, args...).
		WithConfirmCommand("outplane", "env", "group", "delete", group.Name,
			"--yes", "--confirm-name", group.Name)
}

// envGroupAssign attaches a group to an application.
func envGroupAssign(ctx context.Context, req Request) (output.Table, error) {
	group, app, err := groupAndApp(ctx, req, "assign")
	if err != nil {
		return output.Table{}, err
	}

	client, err := req.CLI.APIClient()
	if err != nil {
		return output.Table{}, err
	}

	assignments, err := core.AppEnvGroups(ctx, client, app.ID)
	if err != nil {
		return output.Table{}, err
	}
	for _, a := range assignments {
		if a.GroupID == group.ID {
			req.CLI.Out.Note("%s is already assigned to %s.", group.Name, app.Name)
			return assignmentTable(group, app.Name, false), nil
		}
	}

	if req.CLI.DryRun {
		req.CLI.Out.Note("Would assign %s to %s. Nothing was sent.", group.Name, app.Name)
		return assignmentTable(group, app.Name, false), nil
	}

	if err := core.AssignEnvGroup(ctx, client, group.ID, app.ID); err != nil {
		return output.Table{}, err
	}

	req.CLI.Out.Note("Assigned %s to %s. It reaches the application on its next deployment.",
		group.Name, app.Name)
	return assignmentTable(group, app.Name, true), nil
}

// envGroupUnassign detaches a group from an application.
//
// The API removes an assignment by the assignment's own id, which is not the
// pair a reader is holding, so the assignment is looked up first. That lookup
// is also what turns "it was not assigned" into a statement rather than a 404.
func envGroupUnassign(ctx context.Context, req Request) (output.Table, error) {
	group, app, err := groupAndApp(ctx, req, "unassign")
	if err != nil {
		return output.Table{}, err
	}

	client, err := req.CLI.APIClient()
	if err != nil {
		return output.Table{}, err
	}

	assignments, err := core.AppEnvGroups(ctx, client, app.ID)
	if err != nil {
		return output.Table{}, err
	}

	assignmentID := ""
	for _, a := range assignments {
		if a.GroupID == group.ID {
			assignmentID = a.ID
			break
		}
	}
	if assignmentID == "" {
		req.CLI.Out.Note("%s is not assigned to %s.", group.Name, app.Name)
		return assignmentTable(group, app.Name, false), nil
	}

	if req.CLI.DryRun {
		req.CLI.Out.Note("Would unassign %s from %s. Nothing was sent.", group.Name, app.Name)
		return assignmentTable(group, app.Name, false), nil
	}

	if err := core.UnassignEnvGroup(ctx, client, assignmentID); err != nil {
		return output.Table{}, err
	}

	req.CLI.Out.Note("Unassigned %s from %s. The application loses those variables on its "+
		"next deployment.", group.Name, app.Name)
	return assignmentTable(group, app.Name, true), nil
}

// groupAndApp resolves the two things an assignment joins.
func groupAndApp(ctx context.Context, req Request, verb string) (core.EnvGroup, core.App, error) {
	group, err := targetEnvGroup(ctx, req, verb)
	if err != nil {
		return core.EnvGroup{}, core.App{}, err
	}

	ref := strings.TrimSpace(req.Flags.String("app"))
	if ref == "" {
		if id := req.CLI.Config.AppID.Value; id != "" {
			ref = id
		}
	}
	if ref == "" {
		return core.EnvGroup{}, core.App{}, clierr.New(clierr.KindUsage, "no application given").
			WithCode("context.no_app").
			WithHint("Name it with --app, or link the directory once and omit it.").
			WithStep(verb+" it", "outplane", "env", "group", verb, group.Name, "--app", "<APP_NAME>")
	}

	app, err := resolveApp(ctx, req, ref)
	return group, app, err
}

// targetEnvGroup resolves the group argument.
func targetEnvGroup(ctx context.Context, req Request, verb string) (core.EnvGroup, error) {
	if len(req.Args) == 0 {
		return core.EnvGroup{}, clierr.New(clierr.KindUsage, "no group given").
			WithCode("usage.missing_argument").
			WithStep("see the team's groups", "outplane", "env", "group", "list").
			WithStep(verb+" one", "outplane", "env", "group", verb, "<GROUP_NAME>")
	}
	if strings.TrimSpace(req.Args[0]) == "" {
		return core.EnvGroup{}, clierr.New(clierr.KindUsage, "the group argument is empty").
			WithCode("usage.empty_argument").
			WithHint("This is what an unset variable looks like.")
	}

	client, err := req.CLI.APIClient()
	if err != nil {
		return core.EnvGroup{}, err
	}

	group, err := core.FindEnvGroup(ctx, client, req.Args[0])
	if err == nil {
		return group, nil
	}

	var notFound *core.EnvGroupNotFoundError
	if errors.As(err, &notFound) {
		e := clierr.New(clierr.KindNotFound, "%v", notFound).
			WithCode("envgroup.not_found").
			WithStep("see the team's groups", "outplane", "env", "group", "list")
		if len(notFound.Available) > 0 {
			e = e.WithHint("This team has: %s.", strings.Join(notFound.Available, ", ")).
				WithDetail("availableGroups", notFound.Available)
		} else {
			e = e.WithHint("This team has no variable groups yet.")
		}
		return core.EnvGroup{}, e
	}

	var ambiguous *core.AmbiguousEnvGroupError
	if errors.As(err, &ambiguous) {
		return core.EnvGroup{}, clierr.New(clierr.KindUsage, "%v", ambiguous).
			WithCode("envgroup.ambiguous").
			WithHint("Use the id, which `outplane env group list --json` reports.")
	}

	return core.EnvGroup{}, err
}

// checkScope refuses a group that reaches nothing.
//
// The server accepts it and the result is a set of variables no application
// ever sees, which is a configuration mistake that looks exactly like working
// configuration.
func checkScope(g core.EnvGroup) error {
	if g.UseInBuild || g.UseInRuntime {
		return nil
	}
	return clierr.New(clierr.KindUsage, "a group has to be used somewhere").
		WithCode("envgroup.scope_empty").
		WithHint("--build-only excludes the runtime, so pass --build with it. A group used " +
			"neither at build nor at runtime reaches nothing.")
}

// scopeOrTextChanged reports whether anything other than the variables was
// asked to change, so that `env group set` with no effect is an error rather
// than a silent write.
func scopeOrTextChanged(req Request) bool {
	return req.Flags.String("description") != "" ||
		req.Flags.Bool("build") || req.Flags.Bool("no-build") ||
		req.Flags.Bool("build-only") || req.Flags.Bool("runtime")
}

// appNamesOf lists the applications when there are few enough to read, and
// says nothing when there are not: twenty names in a closing line is a wall,
// and `--apps` is the command for that.
func appNamesOf(g core.EnvGroup) string {
	if len(g.AssignedApps) == 0 || len(g.AssignedApps) > 5 {
		return ""
	}
	names := make([]string, 0, len(g.AssignedApps))
	for _, a := range g.AssignedApps {
		names = append(names, a.AppName)
	}
	return strings.Join(names, ", ")
}

func scopeOf(g core.EnvGroup) string {
	switch {
	case g.UseInBuild && g.UseInRuntime:
		return "at build and at runtime"
	case g.UseInBuild:
		return "at build only"
	case g.UseInRuntime:
		return "at runtime"
	default:
		return "nowhere"
	}
}

func groupRow(g core.EnvGroup) map[string]any {
	return map[string]any{
		"id":           g.ID,
		"name":         g.Name,
		"description":  nilIfEmpty(g.Description),
		"variables":    g.Variables,
		"assignments":  g.Assignments,
		"scope":        scopeOf(g),
		"useInBuild":   g.UseInBuild,
		"useInRuntime": g.UseInRuntime,
	}
}

func groupSingle(g core.EnvGroup, changed bool) output.Table {
	row := groupRow(g)
	row["changed"] = changed
	if g.Variables == 0 {
		row["variables"] = len(g.Entries)
	}
	return output.Table{
		Single:  true,
		Columns: []string{"name", "variables", "assignments", "scope", "changed"},
		Total:   1,
		Rows:    []map[string]any{row},
	}
}

// assignmentTable deliberately reports no deployment.
//
// A group is shared, and the commands over it do not deploy: assigning is one
// application but the group's other users are one command away, and a CLI that
// deploys some of them and not others is worse than one that deploys none. The
// reader is told when the change takes effect and left to decide what to deploy.
func assignmentTable(g core.EnvGroup, app string, changed bool) output.Table {
	return output.Table{
		Single:  true,
		Columns: []string{"group", "app", "changed"},
		Total:   1,
		Rows: []map[string]any{{
			"group":   g.Name,
			"groupId": g.ID,
			"app":     app,
			"changed": changed,
		}},
	}
}

// plural writes a count with its noun, because "1 applications" is the kind of
// detail that makes output look machine-written.
func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(n) + " " + noun + "s"
}
