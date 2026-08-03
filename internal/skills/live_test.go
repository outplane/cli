package skills

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"
)

// TestLive fetches the real skill from the real repository.
//
// Off by default: the rest of this package's tests must pass on a machine with
// no network, and a unit test that reaches GitHub is a unit test that fails for
// reasons that have nothing to do with the change being tested. Run it when the
// question is whether the published skill can actually be installed:
//
//	OUTPLANE_LIVE_SKILLS=1 go test ./internal/skills -run TestLive -v
func TestLive(t *testing.T) {
	if os.Getenv("OUTPLANE_LIVE_SKILLS") == "" {
		t.Skip("set OUTPLANE_LIVE_SKILLS=1 to fetch from the real repository")
	}
	client := &http.Client{Timeout: 30 * time.Second}
	ctx := context.Background()

	ref, err := LatestRef(ctx, client)
	if err != nil {
		t.Fatalf("LatestRef: %v", err)
	}
	t.Logf("latest ref: %s", ref)

	files, err := Fetch(ctx, client, ref)
	if err != nil {
		t.Fatalf("Fetch(%s): %v", ref, err)
	}
	if _, ok := files["SKILL.md"]; !ok {
		t.Fatal("no SKILL.md in the published skill")
	}
	t.Logf("%d files, version %s", len(files), Version(files))
	for name := range files {
		t.Logf("  %s", name)
	}

	dir := t.TempDir()
	target, err := Install(files, dir)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	t.Logf("installed to %s", target)

	got, ok := Installed(dir)
	if !ok {
		t.Fatal("Installed reports nothing after installing")
	}
	if got != Version(files) {
		t.Fatalf("installed version %q, fetched %q", got, Version(files))
	}
}
