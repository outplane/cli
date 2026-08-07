// Package registry is the single source of truth for the Out Plane CLI's
// command surface.
//
// Every command is declared once, here, as a plain Command struct value. From
// that one declaration we generate:
//
//   - the cobra command tree                (cmd/outplane)
//   - the `--help` text                     (internal/help)
//   - the `outplane schema` document        (internal/schema)
//   - shell completions                     (cobra, from the same tree)
//   - the documentation pages               (docs generator)
//   - the golden contract tests             (test/golden)
//
// Adding a command means adding a struct value. It does not mean touching six
// files, and it is not possible to ship a command whose help text, examples,
// error codes and machine schema disagree with each other, because there is
// only one place to write them down.
//
// This is deliberate. The CLI is maintained by a very small team plus AI
// agents, and both need the codebase to explain itself. An agent asked to add
// a command should be able to read one existing declaration and infer the
// whole pattern. If the pattern is not obvious from a single example, the
// pattern is wrong.
//
// There is no reflection here, no code generation step, no init-order magic.
// Commands is a plain slice; the cobra tree is built from it with an ordinary
// loop that anyone can read top to bottom.
package registry

import (
	"strconv"
	"strings"
)

// Risk classifies what a command can do to the user's resources. It drives
// three separate things, which is why it is a first-class field rather than a
// comment:
//
//   - `outplane schema` publishes it, so an agent harness (Claude Code, Cursor)
//     can auto-allow reads and gate everything else without hardcoding a list
//     of our command names.
//   - the confirmation protocol keys off it: RiskDestructive never prompts and
//     never auto-approves, it exits 4 with a replayable command.
//   - the help renderer prints a warning banner for RiskDestructive.
//
// An unmarked command is UNKNOWN, not safe. Zero value is deliberately invalid
// so that forgetting the field fails loudly instead of silently claiming to be
// read-only.
type Risk int

const (
	// RiskUnset is the zero value and is always a bug. Validation rejects it.
	RiskUnset Risk = iota

	// RiskRead changes nothing. Safe for a harness to auto-approve.
	RiskRead

	// RiskWrite makes a reversible change: deploy, scale, set an env var.
	RiskWrite

	// RiskDestructive makes an irreversible one: delete an app, a database, a
	// volume. There is no undelete and no soft-delete window on this platform,
	// so this is as bad as it sounds.
	RiskDestructive
)

// SessionKind says which credential a command can run under.
//
// Out Plane has two: a user JWT from an interactive login, and a team-scoped
// API token. They are not interchangeable. An API token's subject is the
// literal string "api-token:{guid}", so any endpoint that resolves a real user
// identity behaves incorrectly under one.
//
// Marking this per command lets the CLI refuse with a clear message before
// making a request, instead of relaying a confusing server error.
type SessionKind int

const (
	// SessionAny works with either credential. Most commands.
	SessionAny SessionKind = iota

	// SessionUser requires an interactive login. Used by commands that touch
	// GitHub App installations, which are bound to a user id and can never be
	// owned by a team token.
	SessionUser
)

// StreamKind says whether a command produces a stream rather than a single
// result, which changes the default output encoding: streams are NDJSON so an
// agent can process them line by line instead of buffering a whole build log
// into its context window.
type StreamKind int

const (
	StreamNone StreamKind = iota
	StreamNDJSON

	// StreamLines is output that is already text and has no record structure
	// to encode: a build log is one long stream produced by somebody else's
	// compiler. Wrapping each line in an object would add a field nobody asked
	// for and break `outplane deploy logs | grep error`.
	StreamLines
)

// OutputMode is the per-command declaration of what a piped invocation
// produces. The tool-wide default is JSON when stdout is not a TTY, but a
// command may override it. `logs` does, because `outplane logs | grep ERROR` is
// the most common human pipeline in the whole CLI and turning it into JSON
// would break it.
//
// The override must be declared here rather than special-cased in the renderer,
// because the schema publishes it. An undeclared exception is a silent contract
// violation: an agent reads "piped: json" from the schema and gets text.
type OutputMode struct {
	TTY   string // "text"
	Piped string // "json" | "text"
}

// Command is one invocable command or subcommand.
//
// The field order below is the order a reader should think in: identity, then
// safety, then behaviour, then inputs, then outputs, then documentation, then
// the code that actually runs.
type Command struct {
	// ── Identity ────────────────────────────────────────────────────────

	// Path is the command as the user types it, split on spaces.
	// {"app", "list"} means `outplane app list`.
	Path []string

	// Aliases are alternative spellings of THIS SAME command. They are
	// visible: they appear in the ALIASES section of --help so users can
	// discover them. `outplane deploy` is an alias of `outplane deploy create`
	// because it is the most-typed command in the CLI and hiding it would mean
	// nobody finds it.
	//
	// Aliases are not related commands. See Related for those.
	Aliases []string

	// Short is one line, lowercase, no trailing period. It appears next to the
	// command in parent listings, so it must make sense out of context.
	Short string

	// Long is one or two paragraphs shown at the top of --help. Explain what
	// the command is for, not how the flags work; the flags document
	// themselves.
	Long string

	// ── Safety ──────────────────────────────────────────────────────────

	Risk         Risk
	RequiresAuth bool
	Session      SessionKind

	// Idempotent means running it twice leaves the same state as running it
	// once. Published in the schema so an agent knows a retry after a network
	// timeout is safe. Mutating commands that are not idempotent should accept
	// an idempotency key once the API supports one.
	Idempotent bool

	// RootWired means the command is declared here but built by hand on the
	// cobra root instead of by the usual wiring loop.
	//
	// Four commands have to run before configuration is read and before a
	// credential is resolved: schema, help, version and completion. That puts
	// their execution outside the path that renders a failure, which is why
	// main.go builds them itself. Leaving them undeclared, though, made them
	// invisible: `outplane schema` published eighty five commands and was not
	// one of them, so an agent could only discover the command that describes
	// the CLI if it already knew the command existed.
	//
	// So the declaration lives here, where the schema, the help listing and the
	// documentation all read from, and only the construction stays over there.
	RootWired bool

	// ── Behaviour ───────────────────────────────────────────────────────

	// LongRunning means the command may block for minutes. The help renderer
	// requires AutomationNotes for these, because the single most common agent
	// failure is reporting a queued build as a shipped deploy.
	LongRunning bool

	Streams StreamKind

	// SupportsDryRun means --dry-run validates locally and prints the request
	// that would be sent, without making it. Every mutating command should
	// support it.
	SupportsDryRun bool

	// Output overrides the tool-wide TTY/piped defaults. Nil means use them.
	Output *OutputMode

	// SuppressGlobals names global flags that do not apply to this command, so
	// that help does not offer an option which would do nothing.
	//
	// `login` suppresses --team: the team is chosen in the console, and showing
	// a team flag would contradict the command's own documentation. An option
	// that appears in help and then has no effect is worse than one that does
	// not exist, for a human and for an agent alike.
	SuppressGlobals []string

	// APICalls lists the API endpoints this command hits, for the schema and
	// for whoever has to trace a failure back to a controller. Purely
	// documentary; nothing reads it at runtime.
	APICalls []string

	// ── Inputs ──────────────────────────────────────────────────────────

	Args  []Arg
	Flags []Flag

	// ── Outputs ─────────────────────────────────────────────────────────

	// OutputFields is every field this command's structured output can
	// contain. One declaration feeds three consumers: the JSON FIELDS help
	// section, validation of --fields and --json, and the schema.
	//
	// This is what makes bare `--json` able to print the available field list,
	// which is the single highest-value discovery affordance in the CLI: it
	// turns a typo into a complete field inventory in one round trip, with no
	// auth and no docs.
	OutputFields []Field

	// ErrorCodes are the stable error codes this specific command can emit.
	// They reference entries in the global code table by name; CI fails if a
	// name here has no entry there.
	ErrorCodes []string

	// ExitCodes are only the codes this command can actually return, not the
	// full table. Help prints these plus a pointer to `outplane help exit-codes`.
	ExitCodes []int

	// ── Documentation ───────────────────────────────────────────────────

	// Examples are runnable. Rules enforced by validation, not by convention:
	//   - at least 3 for every leaf command
	//   - at least one uses --json or -o ndjson
	//   - at least one is safe to run as-is (read-only)
	//   - for a mutating command, at least one uses --dry-run
	//
	// They are never hand-written into a help string. There is exactly one
	// place a maintainer adds an example, and it feeds help, schema, docs and
	// the golden tests at once.
	Examples []Example

	// AutomationNotes are plain sentences about what the command does NOT do,
	// and which command to run next. Mandatory for anything long-running,
	// asynchronous, paginated, clamped, or read-modify-write.
	//
	// This field exists because agents otherwise report a queued build as a
	// shipped deploy. It is the cheapest correctness fix available to them.
	//
	// Write declarative facts, never imperatives addressed at the agent.
	// "Without --wait this returns at Queued" is fine. "You should now run..."
	// is not: agent harnesses flag directive phrasing as prompt injection.
	AutomationNotes []string

	// Related names 3 to 6 sibling commands by full path. An agent that lands
	// on the wrong command should be one hop from the right one without
	// re-reading the whole tree.
	Related []string

	DocsURL string
}

// Arg is a positional argument.
type Arg struct {
	Name string

	// Short is one line. It is shown in the ARGUMENTS help section and in the
	// schema, so write it for someone who has never seen the command.
	Short string

	Required bool

	// Variadic means this argument absorbs all remaining values. Only the last
	// argument may be variadic.
	Variadic bool

	// Resolves names the resource kind this argument identifies, if any:
	// "app", "database", "volume", "deployment". The CLI uses it to turn a
	// human-friendly name into the GUID the API wants, and to produce a
	// not-found error that suggests the right list command.
	//
	// This exists because the API has no lookup-by-name endpoint for anything:
	// every path parameter is a GUID, so name resolution is entirely the
	// client's job.
	Resolves string

	// Pattern is a client-side regexp that mirrors the server's validator.
	// Checking it locally turns a round trip into an instant, specific error.
	// It must never be stricter than the server's rule.
	Pattern string
}

// Flag is a named option.
type Flag struct {
	Name  string // without leading dashes: "follow"
	Short string // single letter, no dash: "f". Empty means no short form.

	// Type is one of: bool, string, int, duration, string[].
	Type string

	Default string

	// Description is one line, shown in FLAGS. If the flag has a non-obvious
	// interaction with another flag or with the platform, say it here rather
	// than in Long: this is where a reader looks.
	Description string

	// Enum constrains the accepted values. Published in the schema so an agent
	// does not have to guess, and validated locally so a wrong value fails
	// instantly instead of after a round trip.
	Enum []string

	// Repeatable means the flag may be given more than once and the values
	// accumulate, like --port.
	Repeatable bool

	// Discouraged marks a flag that works but should rarely be used. --token
	// is discouraged because argv is visible in process lists and CI logs.
	// Help renders these with a warning.
	Discouraged string
}

// Field is one field of a command's structured output.
type Field struct {
	Name string

	// Type is a human-readable type string for the schema and help:
	// "string", "int", "bool", "object", "string | null".
	Type string

	Description string

	// Enum lists the possible values when the field is an enumeration.
	//
	// Note the platform reality this has to survive: deployment status is an
	// integer enum on the server, and a value we do not know about may appear.
	// The CLI never guesses what an unknown value means. `deploy create --wait`
	// keeps waiting and exits 124 naming the raw value, rather than reporting a
	// false success or a false failure.
	Enum []string
}

// Example is a runnable example.
//
// It carries two forms of the same thing on purpose: Command is what a human
// copies out of the help text, Argv is what an agent executes directly with no
// shell tokenization step. Keeping them in one struct means they cannot drift.
// CommandLine is the example as somebody would type it.
//
// Command and Argv are the same invocation written twice, and the four `skills`
// examples shipped with only the second: the documentation renders Command, so
// every one of them published an empty code block. Deriving it when it is
// missing means the two cannot disagree by omission. Command is still allowed,
// because a few examples read better with quoting a naive join would not
// produce.
func (e Example) CommandLine() string {
	if e.Command != "" {
		return e.Command
	}
	parts := make([]string, 0, len(e.Argv))
	for _, a := range e.Argv {
		// Quote what a shell would otherwise split or interpret, so the line
		// stays copy-and-paste correct.
		if a == "" || strings.ContainsAny(a, " \t\"'$&|;<>()*?") {
			parts = append(parts, strconv.Quote(a))
			continue
		}
		parts = append(parts, a)
	}
	return strings.Join(parts, " ")
}

type Example struct {
	// Title says what the example accomplishes, in the user's terms.
	// "Deploy and wait for it to finish", not "Use the --wait flag".
	Title string

	Command string
	Argv    []string

	// Placeholders maps a literal in Command to the placeholder it stands for:
	// {"checkout": "<APP_NAME>"}. It exists so nobody ships an example that
	// looks runnable but silently targets a resource the caller does not have.
	Placeholders map[string]string

	// Risk of running this specific example, which may be lower than the
	// command's own risk. A --dry-run example of `app delete` is RiskRead.
	Risk Risk

	// OutputSample is an abbreviated version of what the example prints in
	// JSON mode. Optional, but the most useful single thing an agent can read.
	OutputSample map[string]any
}

// Commands is the full command surface. Order matters: it is the order used in
// help listings and in the schema document, and the schema must be
// deterministic across runs and machines, so this is a slice and never a map.
var Commands []Command

// Register adds commands to the surface. Each registry file calls it from an
// init function, which keeps the declarations next to nothing else.
//
// Validation of these declarations (every command has a Risk, at least three
// examples, an AutomationNotes block if long-running, and so on) runs as a test
// rather than at startup: a broken declaration should fail CI, not the user's
// command.
func Register(cmds ...Command) {
	Commands = append(Commands, cmds...)
}
