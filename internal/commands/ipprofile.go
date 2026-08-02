package commands

import (
	"context"
	"errors"
	"strings"

	"github.com/outplane/cli/internal/api"
	"github.com/outplane/cli/internal/clierr"
	"github.com/outplane/cli/internal/core"
	"github.com/outplane/cli/internal/output"
)

func init() {
	register("ip-profile list", ipProfileList)
	register("ip-profile get", ipProfileGet)
	register("ip-profile create", ipProfileCreate)
	register("ip-profile set", ipProfileSet)
	register("ip-profile delete", ipProfileDelete)
	register("ip-profile assign", ipProfileAssign)
	register("ip-profile unassign", ipProfileUnassign)
}

// Who can reach an application, by address.
//
// The same seven commands as env group, in the same order, taking the same
// --app flag on the two that attach and detach. That is not imitation for its
// own sake: the thing has the same shape, a team-level object attached to
// applications and detached by the attachment's own id, so a reader who knows
// one already knows this.
//
// Every rule is an allow rule. The platform declares a Deny mode and leaves it
// unimplemented, so a profile is an allowlist and attaching one means
// everything not listed stops reaching the application. Nothing here offers a
// choice the server would refuse.
//
// Updating replaces the whole rule list, so `ip-profile set` reads first and
// sends everything, exactly as ports and build settings do.

func ipProfileList(ctx context.Context, req Request) (output.Table, error) {
	client, err := req.CLI.APIClient()
	if err != nil {
		return output.Table{}, err
	}

	profiles, err := core.ListIPProfiles(ctx, client)
	if err != nil {
		return output.Table{}, err
	}

	table := output.Table{
		Columns: []string{"name", "rules", "assignments", "description"},
		Total:   len(profiles),
	}
	for _, p := range profiles {
		table.Rows = append(table.Rows, map[string]any{
			"id":          p.ID,
			"name":        p.Name,
			"description": nilIfEmpty(p.Description),
			"rules":       len(p.Rules),
			"assignments": p.Assignments,
			"createdAt":   nilIfEmpty(p.CreatedAt),
		})
	}

	if len(profiles) == 0 {
		table.Footer = "This team has none, so every application is reachable from anywhere."
	}
	return table, nil
}

func ipProfileGet(ctx context.Context, req Request) (output.Table, error) {
	profile, _, err := targetIPProfile(ctx, req, "get")
	if err != nil {
		return output.Table{}, err
	}
	return ipProfileTable(profile, false), nil
}

// ipProfileCreate makes a profile, which reaches nothing until it is assigned.
func ipProfileCreate(ctx context.Context, req Request) (output.Table, error) {
	if len(req.Args) == 0 || strings.TrimSpace(req.Args[0]) == "" {
		return output.Table{}, clierr.New(clierr.KindUsage, "no name given").
			WithCode("usage.missing_argument").
			WithStep("create one", "outplane", "ip-profile", "create", "<NAME>",
				"--rule", "203.0.113.0/24=office")
	}

	rules, err := parseIPRules(req.Flags.Strings("rule"))
	if err != nil {
		return output.Table{}, err
	}

	profile := core.IPProfile{
		Name:        strings.TrimSpace(req.Args[0]),
		Description: strings.TrimSpace(req.Flags.String("description")),
		Rules:       rules,
	}
	if err := core.CheckIPProfile(profile); err != nil {
		return output.Table{}, err
	}

	client, err := req.CLI.APIClient()
	if err != nil {
		return output.Table{}, err
	}

	if req.CLI.DryRun {
		req.CLI.Out.Note("Would create %s with %s. Nothing was sent.",
			profile.Name, plural(len(profile.Rules), "rule"))
		return ipProfileTable(profile, false), nil
	}

	created, err := core.SaveIPProfile(ctx, client, "", profile)
	if err != nil {
		return output.Table{}, err
	}

	req.CLI.Out.Note("Created %s with %s.", created.Name, plural(len(created.Rules), "rule"))
	req.CLI.Out.Note("It reaches nothing until it is assigned: outplane ip-profile assign %s --app <APP_NAME>",
		created.Name)
	return ipProfileTable(created, true), nil
}

// ipProfileSet replaces a profile's rules, its name or its description.
//
// The endpoint replaces everything it is given, so what is not named is read
// first and sent back. Rules are the exception to that within this command:
// --rule given at all replaces the whole list, because a profile is an
// allowlist and adding to one silently is how somebody ends up with a rule they
// did not mean to keep.
func ipProfileSet(ctx context.Context, req Request) (output.Table, error) {
	profile, client, err := targetIPProfile(ctx, req, "set")
	if err != nil {
		return output.Table{}, err
	}

	changed := []string{}
	if req.Given("name") {
		profile.Name = strings.TrimSpace(req.Flags.String("name"))
		changed = append(changed, "the name")
	}
	if req.Given("description") {
		profile.Description = strings.TrimSpace(req.Flags.String("description"))
		changed = append(changed, "the description")
	}
	if req.Given("rule") {
		rules, err := parseIPRules(req.Flags.Strings("rule"))
		if err != nil {
			return output.Table{}, err
		}
		profile.Rules = rules
		changed = append(changed, "the rules")
	}

	if len(changed) == 0 {
		return output.Table{}, clierr.New(clierr.KindUsage, "nothing to change").
			WithCode("usage.no_change").
			WithHint("Name at least one of --rule, --name or --description. --rule replaces "+
				"the whole list rather than adding to it.").
			WithStep("see what it holds now", "outplane", "ip-profile", "get", profile.Name)
	}

	if err := core.CheckIPProfile(profile); err != nil {
		return output.Table{}, err
	}

	if req.CLI.DryRun {
		req.CLI.Out.Note("Would change %s on %s. Nothing was sent.",
			strings.Join(changed, " and "), profile.Name)
		noteIPRules(req, profile)
		return ipProfileTable(profile, false), nil
	}

	saved, err := core.SaveIPProfile(ctx, client, profile.ID, profile)
	if err != nil {
		return output.Table{}, err
	}

	// The update endpoint answers without the applications the profile is
	// attached to, and they are the most important thing about this change:
	// changing a rule on an attached profile changes who can reach something
	// that is running. They are carried over from the read, which had them.
	saved.AssignedApps = profile.AssignedApps
	saved.Assignments = profile.Assignments

	req.CLI.Out.Note("Changed %s on %s.", strings.Join(changed, " and "), saved.Name)
	noteAffectedApps(req, saved)
	return ipProfileTable(saved, true), nil
}

// ipProfileDelete removes a profile.
//
// Destructive, and the server refuses while anything still uses it. That
// refusal is relayed rather than predicted: it is the server's rule and it is
// the only thing that knows the whole list.
func ipProfileDelete(ctx context.Context, req Request) (output.Table, error) {
	profile, client, err := targetIPProfile(ctx, req, "delete")
	if err != nil {
		return output.Table{}, err
	}

	if req.CLI.DryRun {
		req.CLI.Out.Note("The profile %s and its %s would be removed.",
			profile.Name, plural(len(profile.Rules), "rule"))
		return ipProfileTable(profile, false), nil
	}

	if err := checkIPProfileConfirmed(req, profile); err != nil {
		return output.Table{}, err
	}

	if err := core.DeleteIPProfile(ctx, client, profile.ID); err != nil {
		return output.Table{}, err
	}

	req.CLI.Out.Note("Deleted %s.", profile.Name)
	return ipProfileTable(profile, true), nil
}

// ipProfileAssign attaches a profile to an application, which is the moment it
// starts turning traffic away.
func ipProfileAssign(ctx context.Context, req Request) (output.Table, error) {
	profile, client, err := targetIPProfile(ctx, req, "assign")
	if err != nil {
		return output.Table{}, err
	}

	app, err := flagApp(ctx, req, "ip-profile", "assign")
	if err != nil {
		return output.Table{}, err
	}

	if _, already := core.FindAssignment(profile, app.Name, app.ID); already {
		req.CLI.Out.Note("%s already uses %s.", app.Name, profile.Name)
		return ipAssignTable(profile, app, "assign", false), nil
	}

	if req.CLI.DryRun {
		req.CLI.Out.Note("Would give %s the %s in %s. Nothing was sent.",
			app.Name, plural(len(profile.Rules), "rule"), profile.Name)
		noteIPRules(req, profile)
		return ipAssignTable(profile, app, "assign", false), nil
	}

	if err := core.AssignIPProfile(ctx, client, profile.ID, app.ID); err != nil {
		return output.Table{}, err
	}

	req.CLI.Out.Note("Assigned %s to %s.", profile.Name, app.Name)
	req.CLI.Out.Note("Everything outside its %s can no longer reach %s.",
		plural(len(profile.Rules), "rule"), app.Name)
	return ipAssignTable(profile, app, "assign", true), nil
}

// ipProfileUnassign detaches one, which opens the application again.
func ipProfileUnassign(ctx context.Context, req Request) (output.Table, error) {
	profile, client, err := targetIPProfile(ctx, req, "unassign")
	if err != nil {
		return output.Table{}, err
	}

	app, err := flagApp(ctx, req, "ip-profile", "unassign")
	if err != nil {
		return output.Table{}, err
	}

	assignment, ok := core.FindAssignment(profile, app.Name, app.ID)
	if !ok {
		req.CLI.Out.Note("%s does not use %s.", app.Name, profile.Name)
		return ipAssignTable(profile, app, "unassign", false), nil
	}

	if req.CLI.DryRun {
		req.CLI.Out.Note("Would stop %s using %s. Nothing was sent.", app.Name, profile.Name)
		return ipAssignTable(profile, app, "unassign", false), nil
	}

	if err := core.UnassignIPProfile(ctx, client, assignment.ID); err != nil {
		return output.Table{}, err
	}

	req.CLI.Out.Note("Removed %s from %s.", profile.Name, app.Name)
	req.CLI.Out.Note("It is reachable from anywhere again, unless another profile still covers it.")
	return ipAssignTable(profile, app, "unassign", true), nil
}

// targetIPProfile resolves the profile every command here acts on.
func targetIPProfile(ctx context.Context, req Request, verb string) (core.IPProfile, *api.Client, error) {
	if len(req.Args) == 0 || strings.TrimSpace(req.Args[0]) == "" {
		return core.IPProfile{}, nil, clierr.New(clierr.KindUsage, "no profile given").
			WithCode("usage.missing_argument").
			WithStep("see what this team has", "outplane", "ip-profile", "list").
			WithStep(verb+" one", "outplane", "ip-profile", verb, "<PROFILE_NAME>")
	}

	client, err := req.CLI.APIClient()
	if err != nil {
		return core.IPProfile{}, nil, err
	}

	profile, err := core.FindIPProfile(ctx, client, req.Args[0])
	if err != nil {
		return core.IPProfile{}, nil, explainIPProfileNotFound(err)
	}
	return profile, client, nil
}

// parseIPRules reads CIDR[=description] arguments.
//
// The same shape as `env set KEY=VALUE` and `env group --var`, because it is
// the same idea: a value with an optional label. Splitting on the first equals
// sign keeps a description containing one intact.
func parseIPRules(values []string) ([]core.IPRule, error) {
	rules := make([]core.IPRule, 0, len(values))
	for _, raw := range values {
		cidr, description, _ := strings.Cut(raw, "=")
		cidr = strings.TrimSpace(cidr)
		if err := core.CheckCIDR(cidr); err != nil {
			return nil, err
		}
		rules = append(rules, core.IPRule{CIDR: cidr, Description: strings.TrimSpace(description)})
	}
	return rules, nil
}

func checkIPProfileConfirmed(req Request, profile core.IPProfile) error {
	confirm := func(hint string, args ...any) error {
		return clierr.New(clierr.KindConfirmation,
			"deleting %s needs confirmation", profile.Name).
			WithCode("confirmation.required").
			WithHint(hint, args...).
			WithConfirmCommand("outplane", "ip-profile", "delete", profile.Name,
				"--yes", "--confirm-name", profile.Name)
	}

	if harness := req.CLI.Exec.AgentHarness; harness != "" {
		return confirm("This is running under %s, where the CLI cannot be the thing that "+
			"approves removing an access rule. Hand the command below to whoever is "+
			"accountable for it.", harness)
	}

	if !req.Flags.Bool("yes") || req.Flags.String("confirm-name") == "" {
		return confirm("The rules go with it and nothing restores them. Both --yes and " +
			"--confirm-name are required.")
	}

	if given := req.Flags.String("confirm-name"); given != profile.Name {
		return clierr.New(clierr.KindUsage,
			"--confirm-name says %q and the profile is called %q", given, profile.Name).
			WithCode("ipprofile.confirm_name_mismatch").
			WithDetail("expected", profile.Name).
			WithDetail("given", given)
	}
	return nil
}

// noteAffectedApps names what a change reaches, because changing a rule on an
// assigned profile changes who can reach a running application.
func noteAffectedApps(req Request, profile core.IPProfile) {
	if len(profile.AssignedApps) == 0 {
		req.CLI.Out.Note("It is assigned to nothing, so nothing changed for any application.")
		return
	}
	names := make([]string, 0, len(profile.AssignedApps))
	for _, a := range profile.AssignedApps {
		names = append(names, a.AppName)
	}
	req.CLI.Out.Note("This applies now to %s.", strings.Join(names, ", "))
}

func noteIPRules(req Request, profile core.IPProfile) {
	if len(profile.Rules) == 0 {
		req.CLI.Out.Note("  no rules: nothing would be allowed through")
		return
	}
	for _, r := range profile.Rules {
		if r.Description != "" {
			req.CLI.Out.Note("  %s  %s", r.CIDR, r.Description)
			continue
		}
		req.CLI.Out.Note("  %s", r.CIDR)
	}
}

func explainIPProfileNotFound(err error) error {
	var notFound *core.IPProfileNotFoundError
	if !errors.As(err, &notFound) {
		return err
	}

	e := clierr.New(clierr.KindNotFound, "%v", notFound).
		WithCode("ipprofile.not_found").
		WithStep("see what this team has", "outplane", "ip-profile", "list")
	if len(notFound.Available) > 0 {
		return e.WithHint("It has: %s.", strings.Join(notFound.Available, ", ")).
			WithDetail("availableProfiles", notFound.Available)
	}
	return e.WithHint("It has none at all.")
}

func ipProfileTable(p core.IPProfile, changed bool) output.Table {
	rules := make([]map[string]any, 0, len(p.Rules))
	for _, r := range p.Rules {
		rules = append(rules, map[string]any{"cidr": r.CIDR, "description": nilIfEmpty(r.Description)})
	}

	apps := make([]map[string]any, 0, len(p.AssignedApps))
	for _, a := range p.AssignedApps {
		apps = append(apps, map[string]any{"id": a.ID, "app": a.AppName, "appId": a.AppID})
	}

	return output.Table{
		Single: true,
		// rules and assignedApps are JSON-only: a list of objects renders as an
		// unreadable map in a table, and the counts answer what a reader scans
		// for. The footer names the rules in text mode instead.
		Columns: []string{"name", "description", "rules", "assignments", "changed"},
		Total:   1,
		Footer:  ipRuleFooter(p),
		Rows: []map[string]any{{
			"id":           nilIfEmpty(p.ID),
			"name":         p.Name,
			"description":  nilIfEmpty(p.Description),
			"rules":        len(p.Rules),
			"assignments":  p.Assignments,
			"ruleList":     rules,
			"assignedApps": apps,
			"createdAt":    nilIfEmpty(p.CreatedAt),
			"changed":      changed,
		}},
	}
}

// ipRuleFooter says what the numbers do not.
func ipRuleFooter(p core.IPProfile) string {
	if len(p.Rules) == 0 {
		return "It has no rules. An application it is assigned to is reachable from nowhere."
	}

	parts := make([]string, 0, len(p.Rules))
	for _, r := range p.Rules {
		parts = append(parts, r.CIDR)
	}
	return "Allows " + strings.Join(parts, ", ") + ". Everything else is turned away."
}

func ipAssignTable(p core.IPProfile, app core.App, action string, changed bool) output.Table {
	return output.Table{
		Single:  true,
		Columns: []string{"action", "profile", "app", "changed"},
		Total:   1,
		Rows: []map[string]any{{
			"action":    action,
			"profile":   p.Name,
			"profileId": p.ID,
			"app":       app.Name,
			"appId":     app.ID,
			"rules":     len(p.Rules),
			"changed":   changed,
		}},
	}
}
