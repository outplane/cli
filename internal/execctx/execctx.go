// Package execctx answers one question: who is running this command, and what
// can they see?
//
// Every decision about interactivity, colour, output format and login
// transport derives from a single Context value that is detected once, at
// startup, and then never re-derived. The alternative, which is to ask
// "is this a TTY?" and "is this CI?" separately at each call site, is how
// inconsistencies creep in: one command prompts in CI, another emits colour
// into a pipe, a third opens a browser inside a container.
//
// The rules below are therefore pure functions of Context. They take no
// arguments beyond the context, do no I/O, and are exhaustively unit-tested as
// a truth table. If a new behaviour depends on the environment, it becomes a
// method here rather than an `if` somewhere else.
package execctx

import (
	"os"
	"strings"
)

// Format is how results are encoded.
type Format string

const (
	// FormatAuto resolves to Text on a terminal and JSON when piped. It is the
	// default so that a human gets a table and a script gets parseable output
	// without either having to ask.
	FormatAuto Format = "auto"

	FormatText   Format = "text"
	FormatJSON   Format = "json"
	FormatNDJSON Format = "ndjson"
)

// Formats is every value --output accepts, in the order the help lists them.
var Formats = []Format{FormatAuto, FormatText, FormatJSON, FormatNDJSON}

// ParseFormat validates what --output was given.
//
// An unrecognised value is an error rather than a fallback to text. A fallback
// is the worst of both: `--output jsonl` would print a table and exit 0, so a
// consumer parsing stdout as JSON fails on data that looks like a result, and
// a person reading the exit code concludes the command worked.
func ParseFormat(s string) (Format, bool) {
	for _, f := range Formats {
		if Format(s) == f {
			return f, true
		}
	}
	return "", false
}

// Context describes the environment a single invocation runs in.
//
// It is a plain value. Copy it freely; nothing here is a handle to anything.
type Context struct {
	// RequestedFormat is what --output asked for, or FormatAuto if the flag
	// was not given. Resolve it with Format().
	RequestedFormat Format

	// StdoutTTY and StdinTTY report whether each stream is attached to a
	// terminal. StdinTTY matters on its own: it is the difference between a
	// human who can answer a prompt and a process that cannot.
	StdoutTTY bool
	StdinTTY  bool

	// CI reports a continuous integration environment.
	CI bool

	// AgentHarness names the AI coding agent driving this invocation, or is
	// empty. Detection is by environment variable presence, never by value,
	// and additionally requires stdin not to be a terminal, so that a stale
	// export in someone's shell profile cannot misclassify a human sitting at
	// a keyboard.
	AgentHarness string

	// NoColour is set by --no-color, NO_COLOR, or TERM=dumb.
	NoColour bool

	// Quiet suppresses everything except errors.
	Quiet bool

	// AssumeYes is --yes. It acknowledges a reversible mutation. It is
	// deliberately not sufficient for a destructive one; see CanAutoApprove.
	AssumeYes bool
}

// Detect builds a Context from the process environment. It is called exactly
// once, from main, before any command runs.
func Detect() Context {
	c := Context{
		RequestedFormat: FormatAuto,
		StdoutTTY:       isTerminal(os.Stdout),
		StdinTTY:        isTerminal(os.Stdin),
		CI:              detectCI(),
	}
	c.AgentHarness = detectAgent(c.StdinTTY)
	c.NoColour = detectNoColour(c.StdoutTTY)
	return c
}

// Format resolves the effective output format.
//
// An explicit --output always wins. Otherwise a terminal gets text and
// anything else gets JSON, so that `outplane app list | jq` works without the
// caller remembering a flag.
func (c Context) Format() Format {
	if c.RequestedFormat != FormatAuto && c.RequestedFormat != "" {
		return c.RequestedFormat
	}
	if c.StdoutTTY {
		return FormatText
	}
	return FormatJSON
}

// Machine reports whether output is structured rather than prose.
//
// It is derived from the resolved format alone, deliberately. An earlier
// version also returned true whenever stdout was not a terminal, which made
// `outplane app list -o text | less` produce JSON: an explicit request was
// silently overruled by an inference. Format() already handles the inference,
// so consulting the terminal a second time can only ever contradict it.
//
// Colour and spinners are governed by NoColour, not by this, because those
// depend on the terminal even when the format is text.
func (c Context) Machine() bool {
	f := c.Format()
	return f == FormatJSON || f == FormatNDJSON
}

// Interactive reports whether the CLI may ask a question and wait for an
// answer. It requires a terminal on both ends: a prompt written to a pipe is
// invisible, and a prompt with no keyboard behind it is a hang.
//
// CI and agent harnesses are excluded even when a pseudo-terminal is present,
// because in both cases nobody is watching.
func (c Context) Interactive() bool {
	return c.StdinTTY && c.StdoutTTY && !c.CI && c.AgentHarness == ""
}

// containerMarkers are the files a container runtime leaves behind. A variable
// rather than a constant so a test can point it somewhere that does not exist:
// the check is otherwise unfalsifiable from inside a container, which is where
// a contributor with a devcontainer runs the suite.
var containerMarkers = []string{"/.dockerenv", "/run/.containerenv"}

// CanOpenBrowser reports whether a browser-based login can be attempted.
//
// Deliberately not Interactive(). That answers a different question, which is
// whether the CLI may print a prompt and wait for a keypress, and it excludes
// agent harnesses because they hand out a pseudo-terminal with no keyboard
// behind it. Nothing here is typed: the console mints the token when somebody
// presses Approve and posts it straight to a loopback listener. Borrowing the
// keyboard rule cost an agent the one step of a first deployment it could
// otherwise have finished, and sent the person off to another window.
//
// What still has to hold is that a browser opened on this machine can reach
// that listener, which is what the rest of these checks are about. An agent in
// a container is refused for the same reason an SSH session is, and the reason
// is not that it is an agent.
func (c Context) CanOpenBrowser() bool {
	// Nobody is there to approve, so the browser would open into the void.
	if c.CI {
		return false
	}
	// A harness stands in for the terminal an agent does not have. Without one,
	// a piped invocation is a script, and a script should not raise a window.
	if c.AgentHarness == "" && !(c.StdinTTY && c.StdoutTTY) {
		return false
	}
	// An SSH session has a terminal, but the browser would open on the wrong
	// machine, and the listener is on this one.
	if os.Getenv("SSH_CONNECTION") != "" || os.Getenv("SSH_TTY") != "" {
		return false
	}
	// A container has no browser and usually no display. Two markers, because
	// one runtime writes each, and neither is written by the other.
	for _, marker := range containerMarkers {
		if _, err := os.Stat(marker); err == nil {
			return false
		}
	}
	// A pod, which writes no marker file at all.
	if os.Getenv("KUBERNETES_SERVICE_HOST") != "" {
		return false
	}
	// On Linux, no display server means no browser.
	if isLinux() && os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
		return false
	}
	return true
}

// CanAutoApprove reports whether a mutation may proceed without a human
// confirming it in this moment.
//
// Reversible writes are allowed through with --yes, and are allowed through
// unattended, because refusing them would make the CLI useless in CI.
//
// Destructive operations are never auto-approved, by anyone, ever. They exit 4
// with a replayable command instead, which pushes the decision to whoever is
// actually accountable: a human at a terminal, a CI approval gate, or an agent
// harness's permission prompt. A client-side flag is not a safety boundary,
// because an agent that read our documentation can emit any flag we define.
func (c Context) CanAutoApprove(destructive bool) bool {
	if destructive {
		return false
	}
	return c.AssumeYes || !c.Interactive()
}

// agentEnvVars are environment variables whose mere presence indicates an AI
// coding agent. Presence, not value: harnesses set these to session ids and
// other opaque strings, and comparing values would break on the next release.
//
// Sourced from the published allowlists of CLIs that already do this well.
// Adding an entry is cheap; removing one is a behaviour change.
var agentEnvVars = []string{
	"CLAUDECODE",
	"CLAUDE_CODE",
	"CLAUDE_CODE_SESSION_ID",
	"CLAUDE_CODE_ENTRYPOINT",
	"CURSOR_AGENT",
	"CURSOR_TRACE_ID",
	"CODEX_SANDBOX",
	"OPENAI_CODEX",
	"CODEX_THREAD_ID",
	"OPENCODE",
	"OPENCODE_SESSION_ID",
	"AMP_CURRENT_THREAD_ID",
	"AIDER",
	"COPILOT_AGENT_SESSION_ID",
	"COPILOT_CLI",
	"GEMINI_CLI",
	"REPLIT_AGENT",
	"PI_CODING_AGENT",
	"CLINE_ACTIVE",
}

// detectAgent returns the name of the detected harness, or an empty string.
//
// The stdinTTY guard is load-bearing. A developer who once exported one of
// these variables in a shell profile would otherwise have prompts silently
// disabled for every future invocation, which is a confusing failure that is
// very hard to trace back to its cause.
func detectAgent(stdinTTY bool) string {
	if stdinTTY {
		return ""
	}
	for _, key := range agentEnvVars {
		if _, ok := os.LookupEnv(key); ok {
			return strings.ToLower(key)
		}
	}
	return ""
}

// detectCI recognises a continuous integration environment.
//
// CI is treated as set when it holds any value other than the explicit
// negatives, because runners disagree on what they put there: some use "true",
// some "1", some the name of the system. Meanwhile developers do sometimes
// have CI=false in a shell profile, and that must not be read as "yes".
func detectCI() bool {
	v, ok := os.LookupEnv("CI")
	if ok {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "", "0", "false", "no":
			// fall through to the vendor-specific checks below
		default:
			return true
		}
	}
	for _, key := range []string{
		"GITHUB_ACTIONS", "GITLAB_CI", "BUILDKITE", "CIRCLECI",
		"TRAVIS", "TF_BUILD", "TEAMCITY_VERSION", "JENKINS_URL",
	} {
		if _, ok := os.LookupEnv(key); ok {
			return true
		}
	}
	return false
}

// detectNoColour honours the conventions users already expect, so that nobody
// has to learn an Out Plane specific way to turn colour off.
func detectNoColour(stdoutTTY bool) bool {
	if !stdoutTTY {
		return true
	}
	if _, ok := os.LookupEnv("NO_COLOR"); ok {
		return true
	}
	if os.Getenv("TERM") == "dumb" {
		return true
	}
	return false
}

// isTerminal reports whether f is attached to a character device.
//
// Deliberately implemented with the standard library rather than a dependency:
// this is the whole of what golang.org/x/term would give us here, and every
// dependency is a chance to reintroduce cgo, which would break the static
// binary that motivated choosing Go.
// isTerminal is implemented per platform, in isatty_*.go.
//
// It used to test os.ModeCharDevice, which is wrong in a way that mattered:
// /dev/null is a character device, so every invocation with stdin or stdout
// redirected to it — the normal shape of a CI job, a cron entry and any agent
// harness that allocates no pty — was treated as an interactive terminal. That
// silently disabled agent detection, which is what the destructive-command gate
// depends on, and chose text output where a pipe expected JSON.
