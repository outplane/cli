// Package output renders results and errors.
//
// Two rules govern everything here, and both exist so that piping works:
//
//	stdout carries result data and nothing else.
//	stderr carries progress, warnings and human-readable errors.
//
// If those ever blur, `outplane app list --json | jq` starts failing on a
// warning banner, which is the kind of bug that is reported as "the CLI is
// broken" and takes a day to trace.
//
// The renderer is chosen once from the execution context and then used
// everywhere. Commands never decide how to print; they return data.
package output

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/outplane/cli/internal/clierr"
	"github.com/outplane/cli/internal/execctx"
)

// Table is a command's result in a form both a person and a machine can use.
//
// Commands build one of these instead of formatting anything, which is what
// lets the same result become a terminal table, a JSON object, or an NDJSON
// stream without the command knowing which.
type Table struct {
	// Columns are the headers, in display order. For JSON they are also the
	// key order, so that output is stable and diffable.
	Columns []string

	// Rows are maps rather than slices so that --fields can drop columns
	// without the caller having to re-index anything.
	Rows []map[string]any

	// Total is the number of items that exist, which may exceed len(Rows) if a
	// command ever paginates. Reported in band so a consumer never has to
	// guess whether it saw everything.
	Total int

	// Truncated says the result is incomplete. Silently returning a partial
	// list reads as "this is all of it", which is worse than an explicit flag.
	Truncated bool

	// Single marks a result that is one object rather than a collection, so
	// that `app get` emits an object and `app list` emits {items, total}.
	Single bool
}

// Writer renders results and errors to the right streams.
type Writer struct {
	Out io.Writer
	Err io.Writer
	Ctx execctx.Context
}

// New builds a Writer for the given context.
func New(out, errw io.Writer, ctx execctx.Context) *Writer {
	return &Writer{Out: out, Err: errw, Ctx: ctx}
}

// Result renders a successful outcome to stdout.
func (w *Writer) Result(t Table, fields []string) error {
	if len(fields) > 0 {
		t = t.selectFields(fields)
	}
	switch w.Ctx.Format() {
	case execctx.FormatJSON:
		return w.json(t)
	case execctx.FormatNDJSON:
		return w.ndjson(t)
	default:
		return w.text(t)
	}
}

// json writes one object.
//
// A collection is wrapped as {"items": [...], "total": N, "truncated": bool}
// rather than emitted as a bare array. A bare array cannot grow: the day
// pagination or a warning needs to ride along, adding it would be a breaking
// change for every existing consumer.
func (w *Writer) json(t Table) error {
	enc := json.NewEncoder(w.Out)
	enc.SetIndent("", "  ")
	// Go escapes <, > and & as \u003c and friends by default, which is a
	// browser-safety default that makes no sense here: it turns a readable
	// placeholder like <TOKEN> into noise in a terminal and in a log.
	enc.SetEscapeHTML(false)
	if t.Single {
		if len(t.Rows) == 0 {
			return enc.Encode(map[string]any{})
		}
		return enc.Encode(t.Rows[0])
	}
	return enc.Encode(map[string]any{
		"items":     t.Rows,
		"total":     t.Total,
		"truncated": t.Truncated,
	})
}

// ndjson writes one object per line, flushed as it goes, so that a consumer
// processes incrementally instead of buffering an entire build log into
// memory or into an agent's context window.
func (w *Writer) ndjson(t Table) error {
	enc := json.NewEncoder(w.Out)
	enc.SetEscapeHTML(false)
	for _, row := range t.Rows {
		if err := enc.Encode(row); err != nil {
			return err
		}
	}
	return nil
}

// text writes an aligned table for a person.
func (w *Writer) text(t Table) error {
	if len(t.Rows) == 0 {
		fmt.Fprintln(w.Err, "No results.")
		return nil
	}

	if t.Single {
		// A single object reads better as label/value pairs than as a
		// one-row table with a dozen columns running off the screen.
		width := 0
		for _, c := range t.Columns {
			if len(c) > width {
				width = len(c)
			}
		}
		for _, c := range t.Columns {
			fmt.Fprintf(w.Out, "%-*s  %s\n", width, c, format(t.Rows[0][c]))
		}
		return nil
	}

	widths := make([]int, len(t.Columns))
	for i, c := range t.Columns {
		widths[i] = len(c)
	}
	for _, row := range t.Rows {
		for i, c := range t.Columns {
			if n := len(format(row[c])); n > widths[i] {
				widths[i] = n
			}
		}
	}

	header := make([]string, len(t.Columns))
	for i, c := range t.Columns {
		header[i] = pad(strings.ToUpper(c), widths[i])
	}
	fmt.Fprintln(w.Out, strings.TrimRight(strings.Join(header, "  "), " "))

	for _, row := range t.Rows {
		cells := make([]string, len(t.Columns))
		for i, c := range t.Columns {
			cells[i] = pad(format(row[c]), widths[i])
		}
		fmt.Fprintln(w.Out, strings.TrimRight(strings.Join(cells, "  "), " "))
	}

	if t.Truncated {
		fmt.Fprintf(w.Err, "\nShowing %d of %d. The result was truncated.\n", len(t.Rows), t.Total)
	}
	return nil
}

// Error renders a failure and returns the process exit code.
//
// In a machine format the error envelope goes to STDOUT, replacing the result.
// That is deliberate and slightly unusual: it means a consumer can parse
// stdout unconditionally and find either a result or an error there, without
// having to capture two streams and decide which to believe. A short human
// line still goes to stderr so that a person watching a terminal sees
// something readable.
//
// In text mode nothing goes to stdout at all, so `outplane app list > apps.txt`
// leaves an empty file on failure rather than a file full of error text.
func (w *Writer) Error(err error) int {
	e := clierr.AsError(err)
	if e == nil {
		return 0
	}

	if w.Ctx.Machine() {
		enc := json.NewEncoder(w.Out)
		enc.SetIndent("", "  ")
		enc.SetEscapeHTML(false)
		_ = enc.Encode(map[string]any{"error": e})
		fmt.Fprintf(w.Err, "%s\n", e.Message)
		return e.ExitCode()
	}

	fmt.Fprintf(w.Err, "Error: %s\n", e.Message)
	if e.Hint != "" {
		fmt.Fprintf(w.Err, "\n%s\n", e.Hint)
	}
	if len(e.NextSteps) > 0 {
		fmt.Fprintln(w.Err, "\nTry:")
		for _, s := range e.NextSteps {
			fmt.Fprintf(w.Err, "  %-46s %s\n", strings.Join(s.Argv, " "), s.Why)
		}
	}
	if len(e.ConfirmCommand) > 0 {
		fmt.Fprintln(w.Err, "\nTo proceed, run:")
		fmt.Fprintf(w.Err, "  %s\n", strings.Join(e.ConfirmCommand, " "))
	}
	if e.RequestID != "" {
		fmt.Fprintf(w.Err, "\nRequest ID: %s\n", e.RequestID)
	}
	return e.ExitCode()
}

// Note writes an informational line to stderr.
//
// Always stderr, never stdout, and suppressed by --quiet. A note on stdout
// would corrupt the output of a command being piped, which is the single
// easiest way to break every script a user has written.
func (w *Writer) Note(format string, args ...any) {
	if w.Ctx.Quiet {
		return
	}
	fmt.Fprintf(w.Err, format+"\n", args...)
}

// selectFields narrows a table to the requested columns.
//
// Unknown names are ignored rather than rejected here; validation happens at
// flag-parsing time, where the error can list the available fields.
func (t Table) selectFields(fields []string) Table {
	keep := make(map[string]bool, len(fields))
	for _, f := range fields {
		keep[strings.TrimSpace(f)] = true
	}

	out := Table{Total: t.Total, Truncated: t.Truncated, Single: t.Single}
	for _, c := range t.Columns {
		if keep[c] {
			out.Columns = append(out.Columns, c)
		}
	}
	for _, row := range t.Rows {
		narrowed := make(map[string]any, len(out.Columns))
		for _, c := range out.Columns {
			narrowed[c] = row[c]
		}
		out.Rows = append(out.Rows, narrowed)
	}
	return out
}

// format renders a cell for the text table.
func format(v any) string {
	switch x := v.(type) {
	case nil:
		// An em dash reads as "nothing here" without being mistaken for a
		// value, which an empty cell can be.
		return "—"
	case string:
		if x == "" {
			return "—"
		}
		return x
	case bool:
		if x {
			return "yes"
		}
		return "no"
	default:
		return fmt.Sprint(x)
	}
}

func pad(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-len(s))
}
