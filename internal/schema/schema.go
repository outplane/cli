// Package schema turns the command registry into a machine-readable document.
//
// `outplane schema` is the entry point for an AI agent. Everything it needs to
// use the CLI correctly, every command, argument, flag, output field, error
// code, exit code, risk level and runnable example, comes out of one call.
//
// Three properties are non-negotiable, and each exists for a concrete reason:
//
//   - It works with no authentication, no config file and no network. An agent
//     that has to log in before it can discover how to log in is stuck.
//   - It is deterministic. Same binary, same bytes, every time and on every
//     machine. That is why the registry is a slice and never a map: map
//     iteration order in Go is randomised, and a schema that reorders itself
//     breaks caching and makes diffs useless.
//   - It is a superset of the published CLI Spec, so a generic tool that knows
//     nothing about Out Plane can still consume it. Our additions ride along as
//     extra properties, which that spec explicitly permits.
package schema

import (
	"github.com/outplane/cli/internal/registry"
)

// clispecVersion is the specification revision this document conforms to.
const clispecVersion = "0.2"

// Document is the whole schema. Field order here is the field order in the
// JSON output, so it is arranged for a human reading it top to bottom.
type Document struct {
	Clispec     string `json:"clispec"`
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description"`

	// SchemaVersion is the shape of this document, which changes far less often
	// than the CLI itself. An agent pins expectations against this rather than
	// against the release version.
	SchemaVersion string `json:"schema_version"`

	DocsURL string `json:"docs_url"`

	// Output declares the default encoding, so a consumer reads the contract
	// instead of probing for it.
	Output OutputContract `json:"output"`

	GlobalArgs []Arg        `json:"global_args"`
	Errors     []ErrorKind  `json:"errors"`
	RiskLevels []RiskLevel  `json:"risk_levels"`
	Commands   []CommandDoc `json:"commands"`
}

// OutputContract is what the tool produces on a terminal versus in a pipe.
type OutputContract struct {
	TTY   string `json:"tty"`
	Piped string `json:"piped"`
}

// ErrorKind is one coarse error class. The set is finite and closed: an agent
// that knows nothing about Out Plane can branch on it exhaustively.
//
// Fine-grained codes such as "app.name_taken" live on individual commands.
// They carry more meaning but require knowing the product; kinds do not.
type ErrorKind struct {
	Kind        string `json:"kind"`
	ExitCode    int    `json:"exit_code"`
	Retryable   bool   `json:"retryable"`
	Description string `json:"description"`
}

// RiskLevel documents the risk vocabulary, so a harness can build an allowlist
// from the schema instead of hardcoding our command names.
type RiskLevel struct {
	Name        string `json:"name"`
	Mutating    bool   `json:"mutating"`
	Description string `json:"description"`
}

// CommandDoc is one command as an agent sees it.
type CommandDoc struct {
	Name        string   `json:"name"`
	Path        []string `json:"path"`
	Description string   `json:"description"`
	Aliases     []string `json:"aliases,omitempty"`

	Mutating        bool   `json:"mutating"`
	Risk            string `json:"risk"`
	RequiresAuth    bool   `json:"requires_auth"`
	RequiresSession string `json:"requires_session"`
	Idempotent      bool   `json:"idempotent"`
	LongRunning     bool   `json:"long_running,omitempty"`
	Streams         string `json:"streams,omitempty"`
	SupportsDryRun  bool   `json:"supports_dry_run,omitempty"`

	// Output is present only when the command overrides the tool-wide default.
	// An undeclared override would be a silent contract violation: the agent
	// reads "piped: json" from the top level and gets something else.
	Output *OutputContract `json:"output,omitempty"`

	APICalls []string `json:"api_calls,omitempty"`

	Args         []Arg      `json:"args,omitempty"`
	OutputFields []FieldDoc `json:"output_fields,omitempty"`

	// UnsupportedGlobalArgs names global flags this command rejects, with the
	// leading dashes so the entries match global_args.
	//
	// Without it the schema says --team applies everywhere, and an agent
	// planning `outplane team use beta --team acme` would be following the
	// contract into a usage error. The exclusion is small and the schema is the
	// only place an agent can learn it.
	UnsupportedGlobalArgs []string `json:"unsupported_global_args,omitempty"`

	ErrorCodes []string `json:"error_codes,omitempty"`
	ExitCodes  []int    `json:"exit_codes,omitempty"`

	Examples        []ExampleDoc `json:"examples,omitempty"`
	AutomationNotes []string     `json:"automation_notes,omitempty"`
	Related         []string     `json:"related,omitempty"`
	DocsURL         string       `json:"docs_url,omitempty"`
}

// Arg covers both positional arguments and flags. They differ in how they are
// written on a command line, not in what a caller needs to know about them,
// and collapsing them into one type means an agent has one thing to parse.
type Arg struct {
	Name        string   `json:"name"`
	Short       string   `json:"short,omitempty"`
	Type        string   `json:"type"`
	Description string   `json:"description,omitempty"`
	Required    bool     `json:"required,omitempty"`
	Positional  bool     `json:"positional,omitempty"`
	Variadic    bool     `json:"variadic,omitempty"`
	Repeatable  bool     `json:"repeatable,omitempty"`
	Default     string   `json:"default,omitempty"`
	Enum        []string `json:"enum,omitempty"`
	Pattern     string   `json:"pattern,omitempty"`

	// Resolves names the resource kind a value identifies, so an agent knows
	// that this argument accepts a human-readable name and which list command
	// produces valid values.
	Resolves string `json:"resolves,omitempty"`

	// Discouraged explains why a working option should rarely be used.
	Discouraged string `json:"discouraged,omitempty"`
}

// FieldDoc is one field of a command's structured output.
type FieldDoc struct {
	Name        string   `json:"name"`
	Type        string   `json:"type"`
	Description string   `json:"description,omitempty"`
	Enum        []string `json:"enum,omitempty"`
}

// ExampleDoc is a runnable example.
//
// Command is for a human to copy. Argv is for a machine to execute with no
// shell tokenisation. Placeholders says which literals are stand-ins, so an
// agent never runs an example that looks real but targets a resource it does
// not own.
type ExampleDoc struct {
	Title        string            `json:"title"`
	Command      string            `json:"command"`
	Argv         []string          `json:"argv"`
	Risk         string            `json:"risk"`
	Placeholders map[string]string `json:"placeholders,omitempty"`
	OutputSample map[string]any    `json:"output_sample,omitempty"`
}

// Build produces the schema document for the given commands.
//
// It takes the command slice as a parameter rather than reading the package
// level registry, so it can be tested with a fixture and so the dependency
// direction stays one way: schema depends on registry, never the reverse.
func Build(cmds []registry.Command, version string) Document {
	doc := Document{
		Clispec:       clispecVersion,
		Name:          "outplane",
		Version:       version,
		Description:   "Deploy and operate applications on Out Plane.",
		SchemaVersion: "1",
		DocsURL:       "https://docs.outplane.com/cli",
		Output:        OutputContract{TTY: "text", Piped: "json"},
		GlobalArgs:    globalArgs(),
		Errors:        errorKinds(),
		RiskLevels:    riskLevels(),
	}

	doc.Commands = make([]CommandDoc, 0, len(cmds))
	for _, c := range cmds {
		doc.Commands = append(doc.Commands, describe(c))
	}
	return doc
}

// Filter narrows a document to one command subtree, keeping all top-level
// metadata. `outplane schema deploy create` should not force an agent to read
// the entire surface to learn about one command; bounded output applies to the
// schema too.
func (d Document) Filter(path []string) Document {
	if len(path) == 0 {
		return d
	}
	out := d
	out.Commands = nil
	for _, c := range d.Commands {
		if hasPrefix(c.Path, path) {
			out.Commands = append(out.Commands, c)
		}
	}
	return out
}

func hasPrefix(path, prefix []string) bool {
	if len(prefix) > len(path) {
		return false
	}
	for i, p := range prefix {
		if path[i] != p {
			return false
		}
	}
	return true
}

func describe(c registry.Command) CommandDoc {
	doc := CommandDoc{
		Name:            join(c.Path),
		Path:            c.Path,
		Description:     c.Short,
		Aliases:         c.Aliases,
		Mutating:        c.Risk != registry.RiskRead,
		Risk:            riskName(c.Risk),
		RequiresAuth:    c.RequiresAuth,
		RequiresSession: sessionName(c.Session),
		Idempotent:      c.Idempotent,
		LongRunning:     c.LongRunning,
		SupportsDryRun:  c.SupportsDryRun,
		APICalls:        c.APICalls,

		UnsupportedGlobalArgs: dashed(c.SuppressGlobals),

		ErrorCodes:      c.ErrorCodes,
		ExitCodes:       c.ExitCodes,
		AutomationNotes: c.AutomationNotes,
		Related:         c.Related,
		DocsURL:         c.DocsURL,
	}

	if c.Streams == registry.StreamNDJSON {
		doc.Streams = "ndjson"
	}
	if c.Output != nil {
		doc.Output = &OutputContract{TTY: c.Output.TTY, Piped: c.Output.Piped}
	}

	for _, a := range c.Args {
		doc.Args = append(doc.Args, Arg{
			Name:        a.Name,
			Type:        "string",
			Description: a.Short,
			Required:    a.Required,
			Positional:  true,
			Variadic:    a.Variadic,
			Pattern:     a.Pattern,
			Resolves:    a.Resolves,
		})
	}
	for _, f := range c.Flags {
		doc.Args = append(doc.Args, Arg{
			Name:        "--" + f.Name,
			Short:       shortFlag(f.Short),
			Type:        f.Type,
			Description: f.Description,
			Default:     f.Default,
			Enum:        f.Enum,
			Repeatable:  f.Repeatable,
			Discouraged: f.Discouraged,
		})
	}

	for _, o := range c.OutputFields {
		doc.OutputFields = append(doc.OutputFields, FieldDoc{
			Name:        o.Name,
			Type:        o.Type,
			Description: o.Description,
			Enum:        o.Enum,
		})
	}

	for _, e := range c.Examples {
		doc.Examples = append(doc.Examples, ExampleDoc{
			Title:        e.Title,
			Command:      e.Command,
			Argv:         e.Argv,
			Risk:         riskName(e.Risk),
			Placeholders: e.Placeholders,
			OutputSample: e.OutputSample,
		})
	}

	return doc
}

func riskName(r registry.Risk) string {
	switch r {
	case registry.RiskRead:
		return "read"
	case registry.RiskWrite:
		return "write"
	case registry.RiskDestructive:
		return "destructive"
	default:
		// RiskUnset reaching here means a declaration is missing its Risk.
		// Validation rejects that in CI, so this is a belt-and-braces value
		// that is honest rather than falsely reassuring.
		return "unknown"
	}
}

func sessionName(s registry.SessionKind) string {
	if s == registry.SessionUser {
		return "user"
	}
	return "any"
}

func shortFlag(s string) string {
	if s == "" {
		return ""
	}
	return "-" + s
}

func join(path []string) string {
	out := ""
	for i, p := range path {
		if i > 0 {
			out += " "
		}
		out += p
	}
	return out
}

// dashed prefixes bare flag names so they match the form used in global_args.
// A consumer comparing the two lists should not have to normalise either one.
func dashed(names []string) []string {
	if len(names) == 0 {
		return nil
	}
	out := make([]string, 0, len(names))
	for _, n := range names {
		out = append(out, "--"+n)
	}
	return out
}

// globalArgs are the flags every command accepts.
//
// Derived from registry.GlobalFlags rather than restated here. This function
// used to hold its own copy, and it drifted: it advertised --jq and --timeout,
// neither of which the CLI has, and left out --token, which it does. The schema
// is what an agent plans against, so a wrong entry becomes a command that fails
// on execution for a reason nothing explained.
func globalArgs() []Arg {
	flags := registry.GlobalFlags()
	args := make([]Arg, 0, len(flags))
	for _, f := range flags {
		args = append(args, Arg{
			Name:        "--" + f.Name,
			Short:       shortFlag(f.Short),
			Type:        f.Type,
			Description: f.Description,
			Default:     f.Default,
			Enum:        f.Enum,
			Repeatable:  f.Repeatable,
			Discouraged: f.Discouraged,
		})
	}
	return args
}

// errorKinds is the closed set of coarse error classes.
//
// Exit codes are a public contract: append only, never reused, never
// redefined. A caller that learned them from an old release must not be
// silently wrong against a new one.
func errorKinds() []ErrorKind {
	return []ErrorKind{
		{"usage", 2, false, "Invalid arguments, unknown flag, or client-side validation failure."},
		{"auth", 3, false, "Not authenticated, token revoked or expired, or forbidden for this team."},
		{"confirmation_required", 4, false, "A destructive operation stopped. Replay the command in confirm_command."},
		{"not_found", 5, false, "The named resource does not exist, or is not visible to this credential."},
		{"conflict", 6, false, "The resource already exists, or a concurrent change won."},
		{"quota", 7, false, "Plan limit reached or payment required. Not a rate limit; retrying will not help."},
		{"upstream", 8, true, "The Out Plane API returned a server error."},
		{"timeout", 124, true, "A client-side deadline expired. The server operation may still be running."},
		{"interrupted", 130, false, "Cancelled by the user."},
		{"internal", 1, false, "An unexpected failure in the CLI itself."},
	}
}

func riskLevels() []RiskLevel {
	return []RiskLevel{
		{"read", false, "Changes nothing. Safe to run without review."},
		{"write", true, "Makes a reversible change."},
		{"destructive", true,
			"Irreversible. Never proceeds without confirmation, and never auto-approves under an agent."},
	}
}
