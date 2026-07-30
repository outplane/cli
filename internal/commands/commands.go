// Package commands holds the implementations behind the declared commands.
//
// Declaration and implementation are separate packages on purpose. The registry
// describes what a command is: its risk, its flags, its examples, its error
// codes. This package describes what it does. Keeping them apart means the
// registry stays a pure data structure that anything can import, including the
// schema generator, the help renderer and the documentation build, without
// dragging in an HTTP client.
//
// The two are joined by the command path. A declaration with no handler is a
// command that is documented but not yet implemented, which the CLI reports
// honestly rather than pretending to work.
package commands

import (
	"context"
	"strings"

	"github.com/outplane/cli/internal/cli"
	"github.com/outplane/cli/internal/output"
)

// Request is everything a handler receives.
//
// Args and Flags arrive already parsed, so a handler never touches cobra and
// can be called from anywhere: a test, and later an MCP tool server.
type Request struct {
	CLI   *cli.Context
	Args  []string
	Flags Flags
}

// Flags is parsed flag values, keyed by flag name without dashes.
//
// The accessors below return zero values for absent flags rather than an
// error. A flag that was declared always exists; one that was not is a
// programming mistake caught by the golden tests, not something a handler
// should have to check on every line.
type Flags map[string]any

func (f Flags) String(name string) string {
	if v, ok := f[name].(string); ok {
		return v
	}
	return ""
}

func (f Flags) Bool(name string) bool {
	if v, ok := f[name].(bool); ok {
		return v
	}
	return false
}

// Handler runs one command and returns a renderable result.
//
// It returns data rather than printing, which is what lets the same function
// produce a terminal table, a JSON object and an NDJSON stream without knowing
// which one the caller wants.
type Handler func(ctx context.Context, req Request) (output.Table, error)

// handlers maps a command path to its implementation.
var handlers = map[string]Handler{}

// register wires a handler to a command path. Called from each file's init.
func register(path string, h Handler) { handlers[path] = h }

// Lookup finds the handler for a command path, if one exists.
func Lookup(path []string) (Handler, bool) {
	h, ok := handlers[strings.Join(path, " ")]
	return h, ok
}
