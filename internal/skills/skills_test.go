package skills

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tarball builds an archive shaped like the one GitHub generates for a tag: one
// top directory carrying the ref, everything else below it.
func tarball(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var raw bytes.Buffer
	gz := gzip.NewWriter(&raw)
	tw := tar.NewWriter(gz)
	for name, body := range entries {
		if err := tw.WriteHeader(&tar.Header{
			Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return raw.Bytes()
}

func serve(t *testing.T, body []byte) *http.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	// Every request, whatever its address, gets the archive under test.
	return &http.Client{Transport: rewrite{srv.URL}}
}

type rewrite struct{ to string }

func (r rewrite) RoundTrip(req *http.Request) (*http.Response, error) {
	target, err := http.NewRequest(req.Method, r.to, nil)
	if err != nil {
		return nil, err
	}
	return http.DefaultTransport.RoundTrip(target.WithContext(req.Context()))
}

const skillBody = "---\nname: use-outplane\nmetadata:\n  version: \"1.2.3\"\n---\n\nbody\n"

func TestFetchTakesOnlyTheSkill(t *testing.T) {
	client := serve(t, tarball(t, map[string]string{
		"skills-abc123/skills/use-outplane/SKILL.md":             skillBody,
		"skills-abc123/skills/use-outplane/references/deploy.md": "deploy",
		"skills-abc123/README.md":                                "repo readme",
		"skills-abc123/LICENSE":                                  "licence",
		"skills-abc123/scripts/gen-refs.mjs":                     "code",
		"skills-abc123/.claude-plugin/plugin.json":               "{}",
		"skills-abc123/skills/use-outplane/scripts/run.sh":       "#!/bin/sh\nrm -rf /\n",
		"skills-abc123/skills/some-other-skill/SKILL.md":         "not ours",
	}))

	files, err := Fetch(context.Background(), client, "v1.2.3")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	want := []string{"SKILL.md", "references/deploy.md"}
	if len(files) != len(want) {
		t.Fatalf("took %d files, want %d: %v", len(files), len(want), keys(files))
	}
	for _, name := range want {
		if _, ok := files[name]; !ok {
			t.Errorf("missing %s", name)
		}
	}
	// The shell script is the one that matters: a skills folder holds Markdown,
	// and anything executable in there is either a mistake or an attack.
	for name := range files {
		if !strings.HasSuffix(name, ".md") {
			t.Errorf("took a non-Markdown file: %s", name)
		}
	}
	if got := Version(files); got != "1.2.3" {
		t.Errorf("Version = %q, want 1.2.3", got)
	}
}

func TestFetchRefusesAnArchiveWithoutTheSkill(t *testing.T) {
	client := serve(t, tarball(t, map[string]string{
		"skills-abc123/README.md": "nothing else here",
	}))
	if _, err := Fetch(context.Background(), client, "v1.2.3"); err == nil {
		t.Fatal("accepted an archive with no SKILL.md")
	}
}

// An entry that climbs out of the skill directory has to be dropped rather than
// written: the destination is a home directory.
func TestFetchRefusesPathsThatEscape(t *testing.T) {
	client := serve(t, tarball(t, map[string]string{
		"skills-abc123/skills/use-outplane/SKILL.md":         skillBody,
		"skills-abc123/skills/use-outplane/../../../evil.md": "escaped",
		"skills-abc123/skills/use-outplane/nested/../ok.md":  "not clean",
	}))

	files, err := Fetch(context.Background(), client, "v1.2.3")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	for name := range files {
		if strings.Contains(name, "..") {
			t.Errorf("kept a path that climbs out: %s", name)
		}
	}
	if _, ok := files["SKILL.md"]; !ok {
		t.Error("dropped the legitimate file along with the bad ones")
	}
}

func TestSafePath(t *testing.T) {
	for _, ok := range []string{"SKILL.md", "references/deploy.md", "a/b/c.md"} {
		if !safePath(ok) {
			t.Errorf("safePath(%q) = false, want true", ok)
		}
	}
	for _, bad := range []string{
		"", "/etc/passwd", "../escape.md", "a/../../escape.md", "./SKILL.md", "..",
	} {
		if safePath(bad) {
			t.Errorf("safePath(%q) = true, want false", bad)
		}
	}
}

func TestInstallReplacesRatherThanMerges(t *testing.T) {
	dir := t.TempDir()

	if _, err := Install(map[string][]byte{
		"SKILL.md":          []byte(skillBody),
		"references/old.md": []byte("old"),
	}, dir); err != nil {
		t.Fatalf("first install: %v", err)
	}

	if _, err := Install(map[string][]byte{
		"SKILL.md":          []byte(skillBody),
		"references/new.md": []byte("new"),
	}, dir); err != nil {
		t.Fatalf("second install: %v", err)
	}

	// A reference dropped upstream has to disappear here, or it stays on disk
	// forever as a file nothing points at.
	stale := filepath.Join(dir, Dir, "references", "old.md")
	if _, err := os.Stat(stale); err == nil {
		t.Error("a file removed upstream survived the second install")
	}
	if _, err := os.Stat(filepath.Join(dir, Dir, "references", "new.md")); err != nil {
		t.Errorf("the new file is missing: %v", err)
	}
	// Nothing is left behind from the atomic swap.
	if _, err := os.Stat(filepath.Join(dir, Dir+".new")); err == nil {
		t.Error("the temporary directory was left on disk")
	}
}

func TestInstalledAndRemove(t *testing.T) {
	dir := t.TempDir()

	if _, ok := Installed(dir); ok {
		t.Error("reports a skill in an empty directory")
	}
	if removed, err := Remove(dir); err != nil || removed {
		t.Errorf("Remove on nothing = (%v, %v), want (false, nil)", removed, err)
	}

	if _, err := Install(map[string][]byte{"SKILL.md": []byte(skillBody)}, dir); err != nil {
		t.Fatal(err)
	}
	version, ok := Installed(dir)
	if !ok || version != "1.2.3" {
		t.Errorf("Installed = (%q, %v), want (1.2.3, true)", version, ok)
	}

	removed, err := Remove(dir)
	if err != nil || !removed {
		t.Errorf("Remove = (%v, %v), want (true, nil)", removed, err)
	}
	// Only the skill goes. The folder belongs to the tool and may hold others.
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("the skills directory itself was removed: %v", err)
	}
}

func TestEveryAgentResolvesADirectory(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, a := range Agents() {
		if _, err := a.SkillsDir(false); err != nil {
			t.Errorf("%s: %v", a.ID, err)
		}
		// A tool with no documented project location has to say so rather than
		// invent a path and write where nothing reads.
		dir, err := a.SkillsDir(true)
		if a.project == "" {
			if err == nil {
				t.Errorf("%s: project mode returned %q instead of refusing", a.ID, dir)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: project mode: %v", a.ID, err)
		}
	}
}

func keys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
