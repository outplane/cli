// Package install answers one question about the running binary: how it got
// here, and therefore what would replace it with a newer one.
//
// It exists as its own package because more than one thing needs the answer.
// `outplane update` runs the command, `outplane update --check` prints it, and
// anything reporting on the local setup can name the channel. Deciding it in a
// command handler would mean deciding it again in the next one.
//
// Nothing here installs anything. Detect reports; the caller runs.
package install

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// The two supported channels, named once.
//
// Every other mention of them, in help text, in error messages and in the
// documentation, should read from here. Two copies of an install command is how
// a release ends up telling half its users to run something that no longer
// works.
const (
	// NpmPackage is the wrapper package. The platform binaries it pulls in are
	// @outplane/cli-<os>-<arch>, but nobody installs those directly.
	NpmPackage = "outplane"

	ScriptURL = "https://outplane.com/install.sh"
)

// scriptUpgrade re-runs the install script, and reports it correctly when it
// fails.
//
// The obvious form, `curl -fsSL URL | sh`, cannot: a pipeline exits with the
// status of its LAST command, so a 404 leaves curl printing an error while an
// empty script feeds a shell that exits 0. The update then reports success
// having installed nothing. That was not hypothetical; it is what the first
// run of this command did.
//
// Capturing the script first and running it second makes curl's failure the
// command's failure. `set -o pipefail` would be simpler and is not POSIX, so it
// cannot be relied on in whatever /bin/sh turns out to be.
const scriptUpgrade = `script=$(curl -fsSL ` + ScriptURL + `) && printf '%s' "$script" | sh`

// Method is how this binary was installed, and what would update it.
type Method struct {
	// Name is how a person refers to the channel: "npm", "the install script".
	Name string

	// Path is the resolved location of the running binary. Reported so that a
	// surprising answer can be checked rather than argued with.
	Path string

	// Command is the argv that updates this installation, or nil when this is
	// not an installation the CLI knows how to replace.
	Command []string

	// Display is the same command as a person would type it. It differs from
	// Command for the install script, whose argv wraps a shell pipeline and
	// reads badly when joined.
	Display string

	// Reason explains why Command is nil, in one sentence, and is empty
	// otherwise.
	Reason string
}

// Managed reports whether this CLI knows how to update this installation.
func (m Method) Managed() bool { return len(m.Command) > 0 }

// Detect classifies the running binary.
//
// Symlinks are resolved first, and that is not a detail: npm puts a link in the
// global bin directory pointing into node_modules, so the unresolved path looks
// like an ordinary /usr/local/bin install and would be updated the wrong way.
func Detect() Method {
	return classify(executablePath(), runtime.GOOS)
}

// executablePath resolves the running binary, following symlinks.
//
// Both failures are reported the same way, as an empty string: there is nothing
// useful to say about "the operating system would not tell us where we are"
// beyond that the answer is unknown, and classify already has a branch for it.
func executablePath() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		return resolved
	}
	return exe
}

// classify is the whole decision, as a pure function of the binary's path and
// the platform, so that every branch can be exercised without installing
// anything anywhere.
func classify(path, goos string) Method {
	if path == "" {
		return Method{
			Name:   "unknown",
			Reason: "the location of the running binary could not be determined",
		}
	}

	// Compared in lower case and with forward slashes so that one set of
	// fragments works on Windows too.
	needle := strings.ToLower(filepath.ToSlash(path))

	switch {
	case strings.Contains(needle, "/node_modules/"):
		// npm owns this file. Replacing it by hand would be undone by the next
		// install and would break the package's integrity check on the way.
		return Method{
			Name:    "npm",
			Path:    path,
			Command: []string{"npm", "install", "-g", NpmPackage + "@latest"},
			Display: "npm install -g " + NpmPackage + "@latest",
		}

	case goos == "windows":
		// Windows is served by npm alone; the install script is a shell script
		// for macOS and Linux. Offering it here would print a command that
		// cannot run.
		return Method{
			Name:   "unknown",
			Path:   path,
			Reason: "on Windows the CLI is installed with npm, and this binary did not come from it",
		}

	default:
		// The install script is the only other channel there is, so anything
		// not owned by npm is assumed to have come from it. Running it again is
		// how it upgrades: it overwrites in place.
		//
		// There is deliberately no third case. Recognising other package
		// managers would mean carrying a list of channels Out Plane does not
		// publish to, kept accurate forever, to improve one sentence of error
		// text. `update` prints the command and the binary's path before
		// running anything, so an installation this guess does not fit is
		// visible rather than silently mishandled.
		return Method{
			Name:    "the install script",
			Path:    path,
			Command: []string{"sh", "-c", scriptUpgrade},
			Display: scriptUpgrade,
		}
	}
}
