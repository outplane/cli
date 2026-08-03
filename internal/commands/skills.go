package commands

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/outplane/cli/internal/clierr"
	"github.com/outplane/cli/internal/output"
	"github.com/outplane/cli/internal/skills"
)

func init() {
	register("skills install", skillsInstall)
	register("skills list", skillsList)
	register("skills update", skillsUpdate)
	register("skills remove", skillsRemove)
}

// fetchTimeout is generous because the archive is small and the failure it
// guards against is a hung connection rather than a slow one.
const fetchTimeout = 60 * time.Second

// targets resolves which tools to act on, and where each keeps its skills.
//
// Shared by all four commands so that "which tools" means the same thing
// whether something is being installed, listed or removed. Without --agent it
// is every tool detected on this machine; with it, exactly that one, present or
// not, because naming a tool is saying you know it is there.
func targets(req Request) ([]skills.Agent, error) {
	if id := req.Flags.String("agent"); id != "" {
		agent, ok := skills.Find(id)
		if !ok {
			return nil, clierr.New(clierr.KindUsage, "there is no coding tool called %q", id).
				WithCode("skills.agent_unknown").
				WithHint("The ones this command knows are: %s.", strings.Join(skills.IDs(), ", "))
		}
		return []skills.Agent{agent}, nil
	}

	found := skills.Detect()
	if len(found) == 0 {
		return nil, clierr.New(clierr.KindNotFound,
			"no coding tool was found on this machine").
			WithCode("skills.no_agent_found").
			WithHint("Looked for: %s. Name one with --agent to install anyway.",
				strings.Join(skills.IDs(), ", ")).
			WithStep("see where each one keeps its skills",
				"outplane", "skills", "list", "--json")
	}
	return found, nil
}

// dirFor resolves one agent's skills directory, turning the project-mode
// refusal into an error a caller can act on.
func dirFor(agent skills.Agent, project bool) (string, error) {
	dir, err := agent.SkillsDir(project)
	if err != nil {
		if project {
			return "", clierr.New(clierr.KindUsage, "%v", err).
				WithCode("skills.no_project_dir").
				WithHint("Install it for this machine instead, without --project.")
		}
		return "", clierr.New(clierr.KindInternal, "%v", err)
	}
	return dir, nil
}

// download fetches the skill once, for however many tools are being written to.
func download(ctx context.Context, req Request, ref string) (map[string][]byte, string, error) {
	client := &http.Client{Timeout: fetchTimeout}

	if ref == "" {
		latest, err := skills.LatestRef(ctx, client)
		if err != nil {
			return nil, "", clierr.New(clierr.KindUpstream,
				"could not find the newest release of the skill: %v", err).
				WithCode("skills.fetch_failed").
				WithHint("The skill is published at github.com/%s. Pass --ref to install a particular release.",
					skills.Repo)
		}
		ref = latest
	}

	files, err := skills.Fetch(ctx, client, ref)
	if err != nil {
		return nil, "", clierr.New(clierr.KindUpstream,
			"could not download the skill: %v", err).
			WithCode("skills.fetch_failed").
			WithDetail("ref", ref)
	}
	return files, ref, nil
}

func skillsInstall(ctx context.Context, req Request) (output.Table, error) {
	agents, err := targets(req)
	if err != nil {
		return output.Table{}, err
	}
	project := req.Flags.Bool("project")

	// Resolve every destination before downloading anything, so a mistyped
	// flag fails without a network round trip.
	dirs := make([]string, len(agents))
	for i, a := range agents {
		if dirs[i], err = dirFor(a, project); err != nil {
			return output.Table{}, err
		}
	}

	files, ref, err := download(ctx, req, req.Flags.String("ref"))
	if err != nil {
		return output.Table{}, err
	}
	version := skills.Version(files)

	table := output.Table{
		Columns: []string{"agent", "path", "version", "changed"},
		Total:   len(agents),
	}
	for i, a := range agents {
		changed := true
		if req.CLI.DryRun {
			changed = false
		} else if _, err := skills.Install(files, dirs[i]); err != nil {
			return output.Table{}, clierr.New(clierr.KindInternal,
				"could not write the skill to %s: %v", dirs[i], err).
				WithCode("skills.write_failed")
		}
		table.Rows = append(table.Rows, map[string]any{
			"agent":   a.ID,
			"path":    dirs[i],
			"version": version,
			"changed": changed,
		})
	}

	if req.CLI.DryRun {
		table.Footer = fmt.Sprintf("Nothing was written. %s of the skill would go to the paths above.", ref)
		return table, nil
	}
	table.Footer = fmt.Sprintf(
		"Installed %s. Restart %s so it loads.", ref, toolWord(len(agents)))
	return table, nil
}

func skillsList(ctx context.Context, req Request) (output.Table, error) {
	_ = ctx
	agents, err := targets(req)
	if err != nil {
		return output.Table{}, err
	}
	project := req.Flags.Bool("project")

	table := output.Table{
		Columns: []string{"agent", "path", "installed", "version"},
		Total:   len(agents),
	}
	missing := 0
	for _, a := range agents {
		dir, err := dirFor(a, project)
		if err != nil {
			return output.Table{}, err
		}
		version, installed := skills.Installed(dir)
		if !installed {
			missing++
		}
		table.Rows = append(table.Rows, map[string]any{
			"agent":     a.ID,
			"path":      dir,
			"installed": installed,
			"version":   nilIfEmpty(version),
		})
	}

	if missing > 0 {
		table.Footer = "Add it with: outplane skills install"
	}
	return table, nil
}

func skillsUpdate(ctx context.Context, req Request) (output.Table, error) {
	agents, err := targets(req)
	if err != nil {
		return output.Table{}, err
	}
	project := req.Flags.Bool("project")

	// Updating acts on what is already installed. A tool without the skill is
	// left alone rather than quietly given one: deciding that a tool should
	// have it is what install is for.
	type target struct {
		agent skills.Agent
		dir   string
		have  string
	}
	var found []target
	for _, a := range agents {
		dir, err := dirFor(a, project)
		if err != nil {
			return output.Table{}, err
		}
		if version, installed := skills.Installed(dir); installed {
			found = append(found, target{a, dir, version})
		}
	}

	if len(found) == 0 {
		return output.Table{}, clierr.New(clierr.KindNotFound,
			"the skill is not installed for any of those tools").
			WithCode("skills.not_installed").
			WithStep("install it", "outplane", "skills", "install").
			WithStep("see what is installed where", "outplane", "skills", "list", "--json")
	}

	files, ref, err := download(ctx, req, "")
	if err != nil {
		return output.Table{}, err
	}
	version := skills.Version(files)

	table := output.Table{
		Columns: []string{"agent", "path", "version", "changed"},
		Total:   len(found),
	}
	updated := 0
	for _, t := range found {
		changed := t.have != version
		if changed && !req.CLI.DryRun {
			if _, err := skills.Install(files, t.dir); err != nil {
				return output.Table{}, clierr.New(clierr.KindInternal,
					"could not write the skill to %s: %v", t.dir, err).
					WithCode("skills.write_failed")
			}
			updated++
		}
		if req.CLI.DryRun {
			changed = false
		}
		table.Rows = append(table.Rows, map[string]any{
			"agent":   t.agent.ID,
			"path":    t.dir,
			"version": version,
			"changed": changed,
		})
	}

	switch {
	case req.CLI.DryRun:
		table.Footer = fmt.Sprintf("Nothing was written. The newest release is %s.", ref)
	case updated == 0:
		table.Footer = fmt.Sprintf("Already on %s.", ref)
	default:
		table.Footer = fmt.Sprintf("Updated to %s. Restart %s so it loads.", ref, toolWord(updated))
	}
	return table, nil
}

func skillsRemove(ctx context.Context, req Request) (output.Table, error) {
	_ = ctx
	agents, err := targets(req)
	if err != nil {
		return output.Table{}, err
	}
	project := req.Flags.Bool("project")

	table := output.Table{
		Columns: []string{"agent", "path", "changed"},
		Total:   len(agents),
	}
	for _, a := range agents {
		dir, err := dirFor(a, project)
		if err != nil {
			return output.Table{}, err
		}

		_, present := skills.Installed(dir)
		changed := present
		if present && !req.CLI.DryRun {
			if _, err := skills.Remove(dir); err != nil {
				return output.Table{}, clierr.New(clierr.KindInternal,
					"could not remove the skill from %s: %v", dir, err).
					WithCode("skills.write_failed")
			}
		}
		if req.CLI.DryRun {
			changed = false
		}
		table.Rows = append(table.Rows, map[string]any{
			"agent":   a.ID,
			"path":    dir,
			"changed": changed,
		})
	}
	return table, nil
}

func toolWord(n int) string {
	if n == 1 {
		return "the coding tool"
	}
	return "each coding tool"
}
