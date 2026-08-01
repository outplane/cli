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
	"sort"
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

	// Footer is printed after the rows, in text mode only.
	//
	// Some results need a closing sentence that belongs after the data rather
	// than before it: "the one you wanted is missing? grant access here". A
	// handler cannot write that itself, because the table is rendered after the
	// handler returns, so anything it printed would land above the table it
	// refers to.
	//
	// Text only. In a machine format the same fact belongs in the command's
	// automation notes, where a consumer can find it without parsing prose.
	Footer string

	// Streamed says the handler has already written the output itself, and
	// there is nothing left to render.
	//
	// Log commands need this. A table is built in memory and printed at the
	// end, which is the one thing a stream must not do: a follow has no end,
	// and even a finished build log is thousands of lines nobody wants
	// buffered. Those handlers write as they read and set this, rather than
	// pretending to return rows.
	Streamed bool

	// Declared is every field this command documents, from its registry entry.
	// Filled in by the caller rather than by the handler, so that no handler
	// has to restate what the registry already says.
	//
	// It exists so --fields validates against the command's contract instead of
	// against whatever this particular response happened to contain. Without
	// it, `--fields updatedAt` is accepted against a team with applications and
	// rejected against an empty one, which is a script that works all week and
	// fails on a Monday.
	Declared []string
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
	// Already written by the handler, as it read. Printing "No results." here
	// after a thousand lines of log would be the writer contradicting them.
	if t.Streamed {
		return nil
	}

	if len(fields) > 0 {
		narrowed, err := t.selectFields(fields)
		if err != nil {
			return err
		}
		t = narrowed
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
	// A nil slice encodes as null, and null is not an empty list: a consumer
	// iterating items has to special-case it, and one that does not crashes on
	// the first team with nothing in it. An empty result is [].
	items := t.Rows
	if items == nil {
		items = []map[string]any{}
	}
	return enc.Encode(map[string]any{
		"items":     items,
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
		if t.Footer != "" {
			fmt.Fprintf(w.Err, "\n%s\n", t.Footer)
		}
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
			w.labelled(width, c, format(t.Rows[0][c]))
		}
		return nil
	}

	widths := make([]int, len(t.Columns))
	for i, c := range t.Columns {
		widths[i] = len(heading(c))
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
		header[i] = pad(heading(c), widths[i])
	}
	fmt.Fprintln(w.Out, strings.TrimRight(strings.Join(header, "  "), " "))

	for _, row := range t.Rows {
		cells := make([]string, len(t.Columns))
		for i, c := range t.Columns {
			cells[i] = pad(oneLine(format(row[c])), widths[i])
		}
		fmt.Fprintln(w.Out, strings.TrimRight(strings.Join(cells, "  "), " "))
	}

	if t.Truncated {
		fmt.Fprintf(w.Err, "\nShowing %d of %d. The result was truncated.\n", len(t.Rows), t.Total)
	}
	if t.Footer != "" {
		fmt.Fprintf(w.Err, "\n%s\n", t.Footer)
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
// selectFields narrows a result to the fields the caller asked for.
//
// Two things here are load-bearing.
//
// It selects from every field the rows carry, not just the ones the text table
// shows by default. Columns is a display choice: `app list` shows four columns
// and returns nine fields, and --fields is how a caller reaches the other five.
// Selecting only from Columns would make the documented fields unreachable in
// exactly the case they exist for.
//
// An unknown field is an error rather than an omission. Silently dropping one
// answers "give me updatedAt" with a result that has no updatedAt and no
// complaint, which reads as "this record has no timestamp" and is how a typo
// becomes a wrong conclusion instead of a failed command.
func (t Table) selectFields(fields []string) (Table, error) {
	available := t.fieldNames()

	out := Table{Total: t.Total, Truncated: t.Truncated, Single: t.Single}
	seen := make(map[string]bool, len(fields))

	// Requested order wins over declared order, because a caller who wrote
	// --fields status,name meant those two in that order.
	for _, raw := range fields {
		f := strings.TrimSpace(raw)
		if f == "" || seen[f] {
			continue
		}
		if !available[f] {
			return Table{}, unknownFieldError(f, available)
		}
		seen[f] = true
		out.Columns = append(out.Columns, f)
	}

	for _, row := range t.Rows {
		narrowed := make(map[string]any, len(out.Columns))
		for _, c := range out.Columns {
			narrowed[c] = row[c]
		}
		out.Rows = append(out.Rows, narrowed)
	}
	return out, nil
}

// fieldNames is every field this result can offer.
//
// The declared set comes first and is what makes validation independent of the
// data: an empty list accepts exactly the same field names a full one does.
// Rows are folded in as well so that a handler returning more than it declared
// stays usable rather than being punished for it.
func (t Table) fieldNames() map[string]bool {
	names := make(map[string]bool, len(t.Declared)+len(t.Columns))
	for _, f := range t.Declared {
		names[f] = true
	}
	for _, c := range t.Columns {
		names[c] = true
	}
	for _, row := range t.Rows {
		for k := range row {
			names[k] = true
		}
	}
	return names
}

func unknownFieldError(field string, available map[string]bool) error {
	names := make([]string, 0, len(available))
	for n := range available {
		names = append(names, n)
	}
	sort.Strings(names)

	return clierr.New(clierr.KindUsage, "no field named %q", field).
		WithCode("usage.unknown_field").
		WithHint("Available fields: %s.", strings.Join(names, ", ")).
		WithDetail("availableFields", names)
}

// labelled prints one label/value pair, keeping a multi-line value aligned
// under the first line rather than letting it collapse the layout.
//
// Values really are multi-line: a commit message is the obvious one, and it
// arrives with blank lines and trailers intact. Printed naively, the second
// line starts at column zero and the reader can no longer tell where one field
// ends and the next begins.
func (w *Writer) labelled(width int, label, value string) {
	lines := strings.Split(value, "\n")
	fmt.Fprintf(w.Out, "%-*s  %s\n", width, label, lines[0])

	indent := strings.Repeat(" ", width+2)
	for _, line := range lines[1:] {
		if line == "" {
			fmt.Fprintln(w.Out)
			continue
		}
		fmt.Fprintf(w.Out, "%s%s\n", indent, line)
	}
}

// oneLine flattens a value for a table cell.
//
// A row is one line by definition, so a newline inside a cell does not make the
// table taller, it destroys every column after it. Collapsing is the only
// option; the full value is still in --json.
func oneLine(s string) string {
	if !strings.ContainsAny(s, "\n\r\t") {
		return s
	}
	return strings.Join(strings.Fields(s), " ")
}

// heading turns a field name into a column header.
//
// Field names are camelCase because that is what the JSON uses, and uppercasing
// one directly produces EXPIRESAT and DEPLOYMENTSTATUS. Splitting on the case
// boundary first gives EXPIRES AT, which is what a person reads.
//
// The space is safe here because the text table is for people. Anything parsing
// output should read --json, where these names keep their original form; the
// two are different renderings of the same field on purpose.
func heading(field string) string {
	var b strings.Builder
	for i, r := range field {
		if i > 0 && r >= 'A' && r <= 'Z' {
			b.WriteByte(' ')
		}
		b.WriteRune(r)
	}
	return strings.ToUpper(b.String())
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
