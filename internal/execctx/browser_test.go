package execctx

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// CanOpenBrowser used to be Interactive() plus a few environment checks, and
// Interactive() excludes agent harnesses. That is right for a prompt, which
// needs a keyboard, and wrong for the browser handoff, which needs none: the
// console mints the token when somebody presses Approve and posts it to a
// loopback listener. The cost of the borrowed rule was that an agent could
// install the CLI and then had to send the person to another window to sign in,
// which is the step a first deployment is most likely to die on.
//
// These are the cases that made the difference, plus the ones that must keep
// refusing. The refusals are not about agents. They are about whether a browser
// opened here could reach the listener that is also here.

func TestCanOpenBrowser(t *testing.T) {
	// The environment leaks into this function, so clear what the checks read.
	// t.Setenv restores on cleanup and refuses to run under t.Parallel.
	clearEnv := func(t *testing.T) {
		t.Helper()
		// The suite must give the same answer inside a container as outside it,
		// and a contributor in a devcontainer would otherwise fail every case
		// that expects true, for a reason none of them is about.
		absent := filepath.Join(t.TempDir(), "no-such-marker")
		orig := containerMarkers
		containerMarkers = []string{absent}
		t.Cleanup(func() { containerMarkers = orig })
		t.Setenv("KUBERNETES_SERVICE_HOST", "")
		for _, k := range []string{"SSH_CONNECTION", "SSH_TTY", "DISPLAY", "WAYLAND_DISPLAY"} {
			t.Setenv(k, "")
		}
		// A Linux runner with no display server would refuse every case below
		// for a reason none of them is about, so give it one.
		if isLinux() {
			t.Setenv("DISPLAY", ":0")
		}
	}

	cases := []struct {
		name string
		ctx  Context
		env  map[string]string
		want bool
	}{
		{
			name: "agent harness with no terminal is allowed",
			ctx:  Context{AgentHarness: "claudecode"},
			want: true,
		},
		{
			name: "agent harness in CI is refused, because nobody is there to approve",
			ctx:  Context{AgentHarness: "claudecode", CI: true},
			want: false,
		},
		{
			name: "person at a terminal is allowed, as before",
			ctx:  Context{StdinTTY: true, StdoutTTY: true},
			want: true,
		},
		{
			name: "piped with no harness is refused, because a script should not raise a window",
			ctx:  Context{},
			want: false,
		},
		{
			name: "half a terminal is not a terminal",
			ctx:  Context{StdinTTY: true},
			want: false,
		},
		{
			name: "agent over SSH is refused: the browser would open on the wrong machine",
			ctx:  Context{AgentHarness: "claudecode"},
			env:  map[string]string{"SSH_CONNECTION": "10.0.0.1 22 10.0.0.2 22"},
			want: false,
		},
		{
			name: "person over SSH is refused for the same reason",
			ctx:  Context{StdinTTY: true, StdoutTTY: true},
			env:  map[string]string{"SSH_TTY": "/dev/pts/0"},
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearEnv(t)
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			if got := tc.ctx.CanOpenBrowser(); got != tc.want {
				t.Errorf("CanOpenBrowser() = %v, want %v", got, tc.want)
			}
		})
	}
}

// The two questions are separate, and conflating them is what this change
// undid. Interactive() must keep excluding agent harnesses, because a prompt
// written into one waits for a keypress that never arrives.
func TestAgentIsNeverInteractive(t *testing.T) {
	if (Context{StdinTTY: true, StdoutTTY: true, AgentHarness: "claudecode"}).Interactive() {
		t.Error("Interactive() = true under an agent harness; a prompt there hangs forever")
	}
	// Separately, on a context that is otherwise interactive. Asserting this on
	// the one above proved nothing, because the harness had already made it
	// false and deleting the CI clause left the suite green.
	if (Context{StdinTTY: true, StdoutTTY: true, CI: true}).Interactive() {
		t.Error("Interactive() = true in CI")
	}
}

func TestHeadlessLinuxIsRefused(t *testing.T) {
	if !isLinux() {
		t.Skipf("display server check only applies on linux, running on %s", runtime.GOOS)
	}
	t.Setenv("SSH_CONNECTION", "")
	t.Setenv("SSH_TTY", "")
	t.Setenv("DISPLAY", "")
	t.Setenv("WAYLAND_DISPLAY", "")

	if (Context{AgentHarness: "claudecode"}).CanOpenBrowser() {
		t.Error("CanOpenBrowser() = true with no display server")
	}
}

// The container check had no test at all, which is how it survived a mutation
// run: deleting it left the suite green. A browser opened inside a container
// cannot reach a listener the console would have to post to.
func TestContainerIsRefused(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "containerenv")
	if err := os.WriteFile(marker, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	for _, k := range []string{"SSH_CONNECTION", "SSH_TTY", "KUBERNETES_SERVICE_HOST"} {
		t.Setenv(k, "")
	}
	if isLinux() {
		t.Setenv("DISPLAY", ":0")
	}

	orig := containerMarkers
	containerMarkers = []string{marker}
	t.Cleanup(func() { containerMarkers = orig })

	for _, c := range []Context{
		{AgentHarness: "claudecode"},
		{StdinTTY: true, StdoutTTY: true},
	} {
		if c.CanOpenBrowser() {
			t.Errorf("CanOpenBrowser() = true inside a container, for %+v", c)
		}
	}
}

// A pod writes no marker file, so it is recognised by the variable every one of
// them carries.
func TestKubernetesIsRefused(t *testing.T) {
	for _, k := range []string{"SSH_CONNECTION", "SSH_TTY"} {
		t.Setenv(k, "")
	}
	if isLinux() {
		t.Setenv("DISPLAY", ":0")
	}
	orig := containerMarkers
	containerMarkers = []string{filepath.Join(t.TempDir(), "absent")}
	t.Cleanup(func() { containerMarkers = orig })

	t.Setenv("KUBERNETES_SERVICE_HOST", "10.96.0.1")
	if (Context{AgentHarness: "claudecode"}).CanOpenBrowser() {
		t.Error("CanOpenBrowser() = true inside a pod")
	}
}
