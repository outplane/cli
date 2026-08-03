// Package skills installs the Out Plane agent skill into a coding agent.
//
// The skill lives in its own repository, github.com/outplane/skills, and that
// repository is the source of truth. It is installed by tools that have never
// heard of this CLI, through plugin marketplaces and through the skills CLI, and
// it is released on its own schedule so that a wording fix does not have to wait
// for a CLI release. This command is a third door onto the same thing, for people
// who already have the CLI and would rather not learn a second tool's install
// flow.
//
// Which is why it fetches rather than carries a copy. A skill compiled into this
// binary would be exactly as old as the binary, and the whole point of publishing
// the skill separately is that it does not have to be.
package skills

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Repo is the skill's home. Everything here reads from it and nothing writes.
const Repo = "outplane/skills"

// Dir is the skill's directory name, in the repository and on disk. An agent
// finds a skill by its directory, so the two have to match.
const Dir = "use-outplane"

// Limits on what will be unpacked.
//
// The archive comes from a repository we publish, so these are not a defence
// against us; they are a defence against a redirect that lands somewhere else, a
// truncated transfer, and the ordinary tar bomb. A skill is a handful of
// Markdown files, so the ceilings can be low enough to be meaningful.
const (
	maxArchive  = 8 << 20   // 8 MiB compressed
	maxFile     = 512 << 10 // 512 KiB per file
	maxFiles    = 100
	maxTotalOut = 4 << 20 // 4 MiB unpacked
)

// Agent is a coding tool that reads skills from a directory.
type Agent struct {
	ID   string // what --agent accepts
	Name string // what a person reads
	// dir is the tool's configuration directory, relative to the home
	// directory. Its presence is what "this tool is installed" means.
	dir string
	// project is the same directory relative to the working directory, for an
	// installation that is committed with a repository. Empty where the tool
	// has no documented project-level location, which is better than guessing
	// a path and writing a skill somewhere nothing will read it.
	project string
}

// Agents is every tool this command knows how to install into.
func Agents() []Agent {
	return []Agent{
		{ID: "claude-code", Name: "Claude Code", dir: ".claude", project: ".claude"},
		{ID: "cursor", Name: "Cursor", dir: ".cursor", project: ".cursor"},
		{ID: "codex", Name: "OpenAI Codex", dir: ".codex", project: ".codex"},
		{ID: "opencode", Name: "OpenCode", dir: ".config/opencode"},
	}
}

// SkillsDir is where this agent keeps its skills, or an error saying why not.
func (a Agent) SkillsDir(project bool) (string, error) {
	if project {
		if a.project == "" {
			return "", fmt.Errorf("%s has no project-level skills directory", a.Name)
		}
		return filepath.Join(a.project, "skills"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	// OpenCode follows the XDG convention, and a machine that sets it means it.
	if a.ID == "opencode" {
		if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
			return filepath.Join(xdg, "opencode", "skills"), nil
		}
	}
	return filepath.Join(home, a.dir, "skills"), nil
}

// Present reports whether this tool looks installed on this machine.
//
// The test is the configuration directory rather than the skills directory,
// because a tool that has never been given a custom skill does not have the
// second one yet. Creating it is part of installing.
func (a Agent) Present() bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	if a.ID == "opencode" {
		if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
			_, err := os.Stat(filepath.Join(xdg, "opencode"))
			return err == nil
		}
	}
	_, err = os.Stat(filepath.Join(home, a.dir))
	return err == nil
}

// Detect returns the tools that appear to be installed.
func Detect() []Agent {
	var found []Agent
	for _, a := range Agents() {
		if a.Present() {
			found = append(found, a)
		}
	}
	return found
}

// Find returns the agent with this id.
func Find(id string) (Agent, bool) {
	for _, a := range Agents() {
		if a.ID == id {
			return a, true
		}
	}
	return Agent{}, false
}

// IDs lists every agent id, for an error that has to name the choices.
func IDs() []string {
	ids := make([]string, 0, len(Agents()))
	for _, a := range Agents() {
		ids = append(ids, a.ID)
	}
	return ids
}

// ── fetching ────────────────────────────────────────────────────────────────

// LatestRef asks the repository which release is newest.
//
// The API rather than the /releases/latest redirect: the redirect is HTML and
// took its time to resolve for a freshly published release, and this answer has
// to be right the first time somebody runs the command.
func LatestRef(ctx context.Context, client *http.Client) (string, error) {
	url := "https://api.github.com/repos/" + Repo + "/releases/latest"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	res, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("the skill repository answered %s", res.Status)
	}
	var body struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&body); err != nil {
		return "", err
	}
	if body.TagName == "" {
		return "", errors.New("the skill repository has no published release")
	}
	return body.TagName, nil
}

// wanted matches a file belonging to the skill, and captures its path within it.
//
// The archive's top directory carries the commit, so it is matched rather than
// assumed. Only files under the skill's own directory are taken: the repository
// also holds scripts, manifests and a licence, and none of that belongs in
// somebody's skills folder.
var wanted = regexp.MustCompile(`^[^/]+/skills/` + Dir + `/(.+)$`)

// Fetch downloads one ref of the skill and returns its files, keyed by their
// path within the skill directory.
func Fetch(ctx context.Context, client *http.Client, ref string) (map[string][]byte, error) {
	url := fmt.Sprintf("https://github.com/%s/archive/refs/tags/%s.tar.gz", Repo, ref)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("could not download %s of the skill: %s", ref, res.Status)
	}

	gz, err := gzip.NewReader(io.LimitReader(res.Body, maxArchive))
	if err != nil {
		return nil, fmt.Errorf("the download is not a valid archive: %w", err)
	}
	defer gz.Close()

	files := map[string][]byte{}
	var total int64
	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("the archive could not be read: %w", err)
		}
		// Anything that is not a plain file is skipped rather than refused: a
		// repository is free to contain a directory or a symlink, and neither
		// is something to write into a skills folder.
		if header.Typeflag != tar.TypeReg {
			continue
		}
		m := wanted.FindStringSubmatch(header.Name)
		if m == nil {
			continue
		}
		rel := m[1]
		if !safePath(rel) || !strings.HasSuffix(rel, ".md") {
			continue
		}
		if header.Size > maxFile {
			return nil, fmt.Errorf("%s is larger than this command will write", rel)
		}
		if len(files) >= maxFiles {
			return nil, errors.New("the skill has more files than this command will write")
		}
		data, err := io.ReadAll(io.LimitReader(tr, maxFile+1))
		if err != nil {
			return nil, err
		}
		total += int64(len(data))
		if total > maxTotalOut {
			return nil, errors.New("the skill is larger than this command will write")
		}
		files[rel] = data
	}

	if _, ok := files["SKILL.md"]; !ok {
		return nil, fmt.Errorf("%s of the skill does not contain SKILL.md", ref)
	}
	return files, nil
}

// safePath rejects anything that would write outside the skill's directory.
//
// The archive is ours, so this is belt and braces; it is also four lines, and
// the failure it prevents is writing an arbitrary file into somebody's home
// directory.
func safePath(rel string) bool {
	if rel == "" || strings.HasPrefix(rel, "/") || filepath.IsAbs(rel) {
		return false
	}
	clean := filepath.ToSlash(filepath.Clean(rel))
	if clean == "." || strings.HasPrefix(clean, "../") || clean == ".." {
		return false
	}
	return clean == rel
}

// ── on disk ─────────────────────────────────────────────────────────────────

// Install writes the skill into a skills directory, replacing what is there.
//
// Replacing rather than merging, because a file removed upstream has to
// disappear here too: a reference the skill no longer points at is a file
// nothing will ever load and nobody will ever notice.
func Install(files map[string][]byte, skillsDir string) (string, error) {
	target := filepath.Join(skillsDir, Dir)

	tmp := target + ".new"
	if err := os.RemoveAll(tmp); err != nil {
		return "", err
	}
	for rel, data := range files {
		path := filepath.Join(tmp, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return "", err
		}
	}

	// The old copy goes only once the new one is complete on disk, so an
	// interrupted download leaves the working skill in place.
	if err := os.RemoveAll(target); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, target); err != nil {
		return "", err
	}
	return target, nil
}

// Remove deletes an installed skill, reporting whether there was one.
func Remove(skillsDir string) (bool, error) {
	target := filepath.Join(skillsDir, Dir)
	if _, err := os.Stat(target); errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return true, os.RemoveAll(target)
}

var versionLine = regexp.MustCompile(`(?m)^\s*version:\s*"?([0-9]+\.[0-9]+\.[0-9]+)"?\s*$`)

// Installed reads the version of the skill in this directory, if one is there.
//
// From the skill's own frontmatter rather than a marker file this command
// writes: a marker would be a file the agent does not expect in a skill folder,
// and it would disagree with the frontmatter the moment somebody installed the
// skill another way, which is exactly the case this has to report on.
func Installed(skillsDir string) (version string, ok bool) {
	data, err := os.ReadFile(filepath.Join(skillsDir, Dir, "SKILL.md"))
	if err != nil {
		return "", false
	}
	m := versionLine.FindSubmatch(data)
	if m == nil {
		return "", true
	}
	return string(m[1]), true
}

// Version reads the version out of a freshly fetched skill.
func Version(files map[string][]byte) string {
	m := versionLine.FindSubmatch(files["SKILL.md"])
	if m == nil {
		return ""
	}
	return string(m[1])
}
