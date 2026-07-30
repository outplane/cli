// Package help renders a command's help text.
//
// The section order below is fixed and the headings are uppercase, both on
// purpose: an agent should be able to anchor on ^EXAMPLES$ and trust what
// follows. A help format that reorders itself between commands is a format an
// agent has to parse rather than read.
//
// Nothing here is hand-written per command. Everything comes from the registry
// declaration, so the help text, the schema and the documentation site cannot
// disagree: there is only one place to change any of it.
package help

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/outplane/cli/internal/registry"
)

// Render writes the full help text for one command.
//
// It takes an io.Writer rather than printing, so the same function serves the
// terminal, a golden test and the documentation generator without a flag to
// switch behaviour.
func Render(w io.Writer, c registry.Command, globalFlags []registry.Flag) {
	p := &printer{w: w}

	// Description first, with no heading. This is the only prose in the
	// output; everything below is structured.
	if c.Long != "" {
		p.para(c.Long)
	} else if c.Short != "" {
		p.para(capitalise(c.Short) + ".")
	}

	p.section("USAGE")
	p.line("  " + usage(c))

	if len(c.Aliases) > 0 {
		p.section("ALIASES")
		for _, a := range c.Aliases {
			p.line("  outplane " + a)
		}
	}

	if len(c.Args) > 0 {
		p.section("ARGUMENTS")
		for _, a := range c.Args {
			name := a.Name
			if !a.Required {
				name = "[" + name + "]"
			}
			p.kv(name, a.Short)
		}
	}

	// Local and inherited flags are merged into one section. Splitting them
	// would mean a reader of `outplane deploy create --help` has to already
	// know that --team is declared on the parent in order to discover it.
	//
	// A command may redeclare a global flag to give it a more specific meaning:
	// `app delete` narrows --yes from "acknowledge a reversible change" to
	// "acknowledge the deletion, and this alone is not enough". The local
	// declaration wins and the global one is dropped, because printing both
	// would show the reader two contradictory descriptions of one flag.
	if len(c.Flags) > 0 || len(globalFlags) > 0 {
		p.section("FLAGS")
		local := make(map[string]bool, len(c.Flags))
		for _, f := range c.Flags {
			local[f.Name] = true
			p.kv(flagLabel(f), flagDescription(f))
		}
		for _, f := range globalFlags {
			if local[f.Name] {
				continue
			}
			p.kv(flagLabel(f), flagDescription(f))
		}
	}

	if len(c.OutputFields) > 0 {
		p.section("JSON FIELDS")
		names := make([]string, 0, len(c.OutputFields))
		for _, f := range c.OutputFields {
			names = append(names, f.Name)
		}
		sort.Strings(names)
		p.line("  " + strings.Join(names, ", "))
		p.line("")
		p.line("  Pass --json with no value to print this list.")
	}

	if len(c.Examples) > 0 {
		p.section("EXAMPLES")
		for i, e := range c.Examples {
			if i > 0 {
				p.line("")
			}
			p.line("  # " + e.Title)
			p.line("  " + e.Command)
		}
	}

	// The section that stops an agent reporting a queued build as a shipped
	// deploy. It states what the command does not do, in plain sentences.
	if len(c.AutomationNotes) > 0 {
		p.section("AUTOMATION NOTES")
		for _, n := range c.AutomationNotes {
			p.wrapped("  ", n)
		}
	}

	if len(c.ErrorCodes) > 0 {
		p.section("ERRORS")
		for _, code := range c.ErrorCodes {
			p.line("  " + code)
		}
	}

	if len(c.ExitCodes) > 0 {
		p.section("EXIT CODES")
		p.line("  " + intsToString(c.ExitCodes))
		p.line("")
		p.line("  Full table: outplane help exit-codes")
	}

	if len(c.Related) > 0 {
		p.section("RELATED")
		for _, r := range c.Related {
			p.line("  outplane " + r)
		}
	}

	p.section("LEARN MORE")
	p.line("  outplane schema " + strings.Join(c.Path, " "))
	p.line("    the machine-readable definition of this command")
	if c.DocsURL != "" {
		p.line("  " + c.DocsURL)
	}
	p.line("")
}

func usage(c registry.Command) string {
	var b strings.Builder
	b.WriteString("outplane ")
	b.WriteString(strings.Join(c.Path, " "))
	for _, a := range c.Args {
		b.WriteString(" ")
		if a.Required {
			b.WriteString("<" + a.Name + ">")
		} else {
			b.WriteString("[" + a.Name + "]")
		}
		if a.Variadic {
			b.WriteString("...")
		}
	}
	if len(c.Flags) > 0 {
		b.WriteString(" [flags]")
	}
	return b.String()
}

func flagLabel(f registry.Flag) string {
	label := "--" + f.Name
	if f.Short != "" {
		label = "-" + f.Short + ", " + label
	}
	switch f.Type {
	case "bool", "":
		// A boolean takes no value, so showing a placeholder would be a lie.
	default:
		label += " <" + f.Type + ">"
	}
	return label
}

func flagDescription(f registry.Flag) string {
	d := f.Description
	if len(f.Enum) > 0 {
		d += " (" + strings.Join(f.Enum, " | ") + ")"
	}
	if f.Default != "" && f.Default != "false" {
		d += " [default: " + f.Default + "]"
	}
	if f.Discouraged != "" {
		d += "  DISCOURAGED: " + f.Discouraged
	}
	return d
}

// printer keeps the layout rules in one place so no section invents its own
// spacing.
type printer struct {
	w io.Writer
}

func (p *printer) line(s string) { fmt.Fprintln(p.w, s) }

func (p *printer) section(name string) {
	fmt.Fprintln(p.w)
	fmt.Fprintln(p.w, name)
}

func (p *printer) para(s string) {
	for _, line := range strings.Split(s, "\n") {
		fmt.Fprintln(p.w, line)
	}
}

// kv prints a label and its description in two columns, wrapping the
// description so that a long line does not run off the terminal.
func (p *printer) kv(label, desc string) {
	const col = 26
	pad := col - len(label) - 2
	if pad < 1 {
		// The label is wider than the column, so give the description its own
		// line rather than producing a ragged, unreadable row.
		fmt.Fprintf(p.w, "  %s\n", label)
		p.wrapped(strings.Repeat(" ", col), desc)
		return
	}
	first, rest := splitAt(desc, 78-col)
	fmt.Fprintf(p.w, "  %s%s%s\n", label, strings.Repeat(" ", pad), first)
	if rest != "" {
		p.wrapped(strings.Repeat(" ", col), rest)
	}
}

// wrapped prints text at a fixed indent, breaking on spaces near column 78.
func (p *printer) wrapped(indent, text string) {
	width := 78 - len(indent)
	for text != "" {
		var chunk string
		chunk, text = splitAt(text, width)
		fmt.Fprintf(p.w, "%s%s\n", indent, chunk)
	}
}

// splitAt breaks s at the last space at or before width, returning the head and
// the remainder. A word longer than width is not broken; readable output
// matters more than a hard right margin.
func splitAt(s string, width int) (head, rest string) {
	s = strings.TrimSpace(s)
	if len(s) <= width {
		return s, ""
	}
	cut := strings.LastIndex(s[:width], " ")
	if cut <= 0 {
		return s, ""
	}
	return s[:cut], strings.TrimSpace(s[cut:])
}

func intsToString(ns []int) string {
	parts := make([]string, len(ns))
	for i, n := range ns {
		parts[i] = fmt.Sprint(n)
	}
	return strings.Join(parts, ", ")
}

func capitalise(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
