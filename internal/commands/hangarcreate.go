package commands

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/outplane/cli/internal/clierr"
	"github.com/outplane/cli/internal/core"
	"github.com/outplane/cli/internal/output"
)

func init() {
	register("hangar create", hangarCreate)
}

// hangarCreate makes a Hangar and starts it.
//
// It follows app create: everything the server would refuse is checked before
// anything is sent, which is what makes --dry-run worth running. Three refusals
// only the server can make are given a way forward here: the name being taken,
// by an application as much as by a Hangar, the team's limit on Hangars, and a
// repository the platform cannot see, explained as app create explains it.
//
// Variable groups are named the way every other command names them, by name or
// by id, and resolved before anything is sent. A dry run resolves them too: it
// is a read, and a group that does not exist is exactly what a dry run is for.
func hangarCreate(ctx context.Context, req Request) (output.Table, error) {
	spec, err := buildNewHangar(req)
	if err != nil {
		return output.Table{}, err
	}
	if err := spec.Check(); err != nil {
		return output.Table{}, err
	}

	cli := req.CLI
	if cli.Config.TeamError != nil {
		return output.Table{}, cli.SignInError()
	}

	groups, err := hangarEnvGroups(ctx, req)
	if err != nil {
		return output.Table{}, err
	}
	names := make([]string, 0, len(groups))
	for _, g := range groups {
		spec.EnvGroupIDs = append(spec.EnvGroupIDs, g.ID)
		names = append(names, g.Name)

		// Assigned all the same, because the server accepts it. Said, because
		// it looks exactly like configuration that works.
		if !g.UseInRuntime {
			cli.Out.Note("%s is not used at runtime, and a Hangar never builds, so its variables "+
				"will not reach it.", g.Name)
		}
	}

	if cli.DryRun {
		describeNewHangar(req, spec, names)
		return hangarCreateTable(plannedHangarRow(spec), spec, names, false), nil
	}

	client, err := cli.APIClient()
	if err != nil {
		return output.Table{}, err
	}

	created, err := core.CreateHangar(ctx, client, spec)
	if err != nil {
		return output.Table{}, explainHangarRefusal(err, spec)
	}

	cli.Out.Note("Created %s.", created.Name)
	if created.Status == "failed" {
		// The record exists and holds the name, so creating it again would be
		// refused as taken. Deleting it is the way back.
		cli.Out.Note("It could not start. Delete it and create it again: %s",
			strings.Join(withTeam(req, "outplane", "hangar", "delete", created.Name), " "))
	} else {
		cli.Out.Note("It is starting. Join its session once it is ready: %s",
			strings.Join(connectArgv(req, created), " "))
		if created.Repository != "" {
			cli.Out.Note("On its first start it clones %s@%s into %s, once. To push, sign in to "+
				"GitHub in its terminal with gh auth login.", created.Repository, created.Branch,
				cloneDir(created.Repository))
		}
	}

	return hangarCreateTable(hangarRow(created), spec, names, true), nil
}

// buildNewHangar reads the argument and the flags into a specification.
func buildNewHangar(req Request) (core.NewHangar, error) {
	if len(req.Args) == 0 {
		return core.NewHangar{}, clierr.New(clierr.KindUsage, "no name given").
			WithCode("usage.missing_argument").
			WithHint("A Hangar's name follows the rule for application names, and the two share one "+
				"set of names in a team.").
			WithStep("create one", "outplane", "hangar", "create", "<NAME>")
	}
	if strings.TrimSpace(req.Args[0]) == "" {
		return core.NewHangar{}, clierr.New(clierr.KindUsage, "the name argument is empty").
			WithCode("usage.empty_argument").
			WithHint("This is what an unset variable looks like. A new Hangar has to be named; " +
				"there is nothing to fall back to.")
	}

	agents, err := hangarAgents(req)
	if err != nil {
		return core.NewHangar{}, err
	}

	env, err := parseAssignments(req.Flags.Strings("env"))
	if err != nil {
		return core.NewHangar{}, err
	}

	idle, err := parseIdleTimeout(orDefault(req.Flags.String("idle-timeout"), "30m"))
	if err != nil {
		return core.NewHangar{}, err
	}

	spec := core.NewHangar{
		Name:               strings.TrimSpace(req.Args[0]),
		Agents:             agents,
		Env:                env,
		IdleTimeoutMinutes: idle,
		Repository:         strings.TrimSpace(req.Flags.String("repo")),
		Branch:             strings.TrimSpace(req.Flags.String("branch")),
		PublicRepo:         req.Flags.Bool("public-repo"),
	}
	if err := checkRepositoryFlags(spec); err != nil {
		return core.NewHangar{}, err
	}
	return spec, nil
}

// checkRepositoryFlags refuses --branch or --public-repo without --repo.
//
// The rules for a repository are app create's and are checked with the rest of
// the request. What only this command has to say is that both flags describe
// the repository --repo names: without one, a Hangar has no repository and
// either flag would be dropped without a word.
func checkRepositoryFlags(spec core.NewHangar) error {
	if spec.Repository != "" {
		return nil
	}

	var flag string
	switch {
	case spec.PublicRepo:
		flag = "--public-repo"
	case spec.Branch != "":
		flag = "--branch"
	default:
		return nil
	}
	return clierr.New(clierr.KindUsage, "%s needs --repo", flag).
		WithCode("hangar.repository_required").
		WithHint("It describes the repository --repo names. Pass --repo owner/name with it, or "+
			"leave it out for a Hangar with no repository.").
		WithStep("create one with a repository", "outplane", "hangar", "create", "<NAME>",
			"--repo", "<OWNER>/<REPO>", "--branch", "main")
}

// cloneDir is where a Hangar clones its repository: a directory of the
// repository's name in its home.
func cloneDir(repo string) string {
	_, name, _ := strings.Cut(repo, "/")
	return "~/" + name
}

// hangarAgents reads --agent and --terminal-only.
//
// Naming no agent at all installs the console's preselection, so the two ways of
// creating a Hangar start from the same place. A Hangar with no agents has to be
// asked for, with --terminal-only, and asking for that and for an agent at once
// is a contradiction rather than a preference.
func hangarAgents(req Request) ([]string, error) {
	given := req.Flags.Strings("agent")
	agents := make([]string, 0, len(given))
	for _, a := range given {
		agents = append(agents, strings.TrimSpace(a))
	}

	if req.Flags.Bool("terminal-only") {
		if len(agents) > 0 {
			return nil, clierr.New(clierr.KindUsage, "--terminal-only and --agent contradict each other").
				WithCode("hangar.agent_conflict").
				WithHint("A terminal-only Hangar installs no agents. Pass one or the other.")
		}
		return agents, nil
	}

	if len(agents) == 0 {
		return append(agents, core.DefaultHangarAgents...), nil
	}
	return agents, nil
}

// parseIdleTimeout reads --idle-timeout: a duration, or never.
//
// A duration rather than a bare number of minutes, because a bare 30 does not
// say what it counts. Nil is never, which is also what the server reads it as.
// Whether the length is one a Hangar can have is decided by Check, which knows
// the list.
func parseIdleTimeout(raw string) (*int, error) {
	raw = strings.TrimSpace(raw)
	if strings.EqualFold(raw, "never") {
		return nil, nil
	}

	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 || d%time.Minute != 0 {
		return nil, clierr.New(clierr.KindUsage, "--idle-timeout is not a whole number of minutes or never: %q", raw).
			WithCode("hangar.idle_timeout_invalid").
			WithHint("Use one of: %s.", strings.Join(core.IdleTimeoutChoices(), ", "))
	}
	minutes := int(d / time.Minute)
	return &minutes, nil
}

// hangarEnvGroups resolves --env-group, in the order given.
//
// The order is kept because the server keeps it: when two groups set the same
// key, it decides which value the Hangar sees. The same group named twice, by
// its name once and by its id once for instance, is refused rather than sent,
// because the server refuses it too and its message would not say which.
func hangarEnvGroups(ctx context.Context, req Request) ([]core.EnvGroup, error) {
	refs := req.Flags.Strings("env-group")
	groups := make([]core.EnvGroup, 0, len(refs))
	for _, ref := range refs {
		if strings.TrimSpace(ref) == "" {
			return nil, clierr.New(clierr.KindUsage, "--env-group is empty").
				WithCode("usage.empty_argument").
				WithHint("This is what an unset variable looks like.")
		}

		group, err := resolveEnvGroup(ctx, req, strings.TrimSpace(ref))
		if err != nil {
			return nil, err
		}
		for _, earlier := range groups {
			if earlier.ID == group.ID {
				return nil, clierr.New(clierr.KindUsage, "variable group %s was given twice", group.Name).
					WithCode("hangar.env_group_duplicate").
					WithHint("A group is assigned once. Its name and its id are the same group.")
			}
		}
		groups = append(groups, group)
	}
	return groups, nil
}

// explainHangarRefusal adds a way forward to the refusals only the server can
// make.
//
// The server's own message is kept in every case. A taken name is a conflict
// whatever took it, and the reader needs to know that an application counts,
// because `hangar list` will not show it. The limit arrives as a plan limit,
// whose generic advice points at billing; a team's number of Hangars is a fixed
// maximum rather than a plan setting, so that advice is replaced. A repository
// the platform cannot see is the server's app create message, explained the
// same way.
func explainHangarRefusal(err error, spec core.NewHangar) error {
	e := clierr.AsError(err)
	if e == nil {
		return err
	}

	switch {
	case e.Kind == clierr.KindConflict:
		return e.WithCode("hangar.name_taken").
			WithHint("Applications and Hangars share one set of names in a team, so the name %s "+
				"may belong to either.", spec.Name).
			WithStep("see the team's Hangars", "outplane", "hangar", "list").
			WithStep("see the team's applications", "outplane", "app", "list")
	case e.Code == "quota.limit_reached":
		e.NextSteps = nil
		return e.WithCode("hangar.limit_reached").
			WithHint("A team can have a fixed number of Hangars, and this one has reached it. "+
				"Retrying will not help; deleting one makes room.").
			WithStep("see the team's Hangars", "outplane", "hangar", "list")
	}
	return explainRepositoryAccess(err, "hangar", spec.Repository)
}

// describeNewHangar prints what would be created, so a dry run says more than
// "nothing was sent".
func describeNewHangar(req Request, spec core.NewHangar, groups []string) {
	agents := "no agents, a terminal only"
	if len(spec.Agents) > 0 {
		agents = strings.Join(catalogOrder(spec.Agents), ", ")
	}
	req.CLI.Out.Note("Would create %s with %s.", spec.Name, agents)

	req.CLI.Out.Note("  idle timeout: %s", core.IdleTimeoutLabel(spec.IdleTimeoutMinutes))
	if spec.Repository != "" {
		req.CLI.Out.Note("  repository: %s@%s, cloned into %s on its first start",
			spec.Repository, spec.Branch, cloneDir(spec.Repository))
	} else {
		req.CLI.Out.Note("  repository: none")
	}
	if len(spec.Env) > 0 {
		req.CLI.Out.Note("  %s, values not shown", plural(len(spec.Env), "environment variable"))
	}
	for _, g := range groups {
		req.CLI.Out.Note("  variable group %s", g)
	}
	req.CLI.Out.Note("Nothing was sent. Creating also starts it.")
}

// plannedHangarRow is the row a dry run reports. What only the server decides,
// the id, the state, the size and the disk, is null rather than guessed. A new
// Hangar has no ports open. The repository is what was asked for, which is what
// the server keeps.
func plannedHangarRow(spec core.NewHangar) map[string]any {
	var idle any
	if spec.IdleTimeoutMinutes != nil {
		idle = *spec.IdleTimeoutMinutes
	}
	return map[string]any{
		"id":                 nil,
		"name":               spec.Name,
		"displayName":        nil,
		"status":             nil,
		"paused":             false,
		"agents":             catalogOrder(spec.Agents),
		"size":               nil,
		"diskGb":             nil,
		"idleTimeoutMinutes": idle,
		"ports":              []map[string]any{},
		"repository":         nilIfEmpty(spec.Repository),
		"branch":             nilIfEmpty(spec.Branch),
		"publicSource":       spec.PublicRepo,
		"createdAt":          nil,
	}
}

// catalogOrder lists the agents the way the server stores them, in catalog
// order whatever order they were named in, so a dry run shows what the real
// call returns.
func catalogOrder(agents []string) []string {
	ordered := make([]string, 0, len(agents))
	for _, a := range core.HangarAgents {
		if slices.Contains(agents, a) {
			ordered = append(ordered, a)
		}
	}
	return ordered
}

func hangarCreateTable(row map[string]any, spec core.NewHangar, groups []string, changed bool) output.Table {
	row["envCount"] = len(spec.Env)
	row["envGroups"] = groups
	row["changed"] = changed
	return output.Table{
		Single:  true,
		Columns: []string{"name", "id", "status", "agents", "size", "diskGb", "idleTimeoutMinutes", "repository", "branch", "changed"},
		Total:   1,
		Rows:    []map[string]any{row},
	}
}
