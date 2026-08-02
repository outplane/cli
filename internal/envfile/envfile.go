// Package envfile reads and writes the .env format.
//
// There is no specification for it. Every tool that reads one agrees on
// `KEY=value` and disagrees about the rest, so the subset implemented here is
// the intersection of what the common readers accept, and each departure from
// the loosest possible reading is a deliberate choice to fail rather than to
// guess:
//
//   - Blank lines and lines whose first non-space character is # are skipped.
//   - A leading `export ` is allowed and ignored, so a file written to be
//     sourced by a shell can be read here unchanged.
//   - A bare value ends at the end of the line. An unquoted # does NOT begin a
//     comment, because a password of pa55word#1 is far more common than an
//     inline comment, and dropping half a credential is a failure nobody sees
//     until something cannot authenticate.
//   - A single-quoted value is literal. A double-quoted value understands
//     \n, \r, \t, \\ and \", which is what makes a certificate fit on one line.
//     Either kind may span several lines, because a certificate that does not
//     fit on one is the other way people write it.
//   - A key that appears twice is an error rather than a last-one-wins, since
//     the two lines disagree and only the author knows which was meant.
//   - Text after a closing quote is an error, except a comment. The value has
//     already ended, so keeping the part before the quote would be a guess
//     about which half of the line was meant.
//   - A byte order mark on the first line is dropped. Several editors write
//     one and none of them mention it, and a key that begins with an invisible
//     character matches nothing anywhere.
//   - Carriage returns are dropped with the line ending, so a file written on
//     Windows reads the same as one written anywhere else.
//
// Nothing here talks to the network or the filesystem. It turns text into
// variables and variables into text, which is what makes both directions
// testable and what keeps the format in one place instead of in two commands.
package envfile

import (
	"bufio"
	"fmt"
	"io"
	"sort"
	"strings"
	"unicode"
)

// Var is one variable in a file.
type Var struct {
	Key   string
	Value string

	// Line is where it was found, for an error that can be acted on.
	Line int
}

// ParseError is a line that could not be read, named by line number.
type ParseError struct {
	Line   int
	Reason string
	Text   string
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("line %d: %s", e.Line, e.Reason)
}

// Parse reads a file into variables, in the order they appear.
//
// Order is preserved rather than sorted: a file is something a person wrote,
// and reporting the third variable as the third one is the difference between
// an error they can find and an error they have to hunt for.
func Parse(r io.Reader) ([]Var, error) {
	scanner := bufio.NewScanner(r)
	// A certificate on one line is longer than the default 64KB limit, and the
	// value limit upstream is smaller than this by a wide margin, so a line
	// this long will be refused with a message about the value rather than an
	// unexplained truncation.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var (
		vars  []Var
		seen  = map[string]int{}
		line  int
		first int
	)

	for scanner.Scan() {
		line++
		text := scanner.Text()
		if line == 1 {
			// A byte order mark, which several editors write and none of them
			// mention. Left in place it becomes part of the first key, and the
			// variable that results is invisible in every listing and matches
			// nothing, because the character it differs by cannot be seen.
			text = strings.TrimPrefix(text, "\ufeff")
		}

		trimmed := strings.TrimLeftFunc(text, unicode.IsSpace)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		key, rest, ok := strings.Cut(strings.TrimPrefix(trimmed, "export "), "=")
		if !ok {
			return nil, &ParseError{Line: line, Text: text,
				Reason: "no = on this line, so there is no way to tell a name from a value"}
		}

		key = strings.TrimSpace(key)
		if key == "" {
			return nil, &ParseError{Line: line, Text: text, Reason: "the name before = is empty"}
		}

		first = line
		value, err := readValue(rest, scanner, &line)
		if err != nil {
			err.Line = first
			err.Text = text
			return nil, err
		}

		if at, duplicate := seen[strings.ToUpper(key)]; duplicate {
			return nil, &ParseError{Line: first, Text: text,
				Reason: fmt.Sprintf("%s is also set on line %d, and the two disagree", key, at)}
		}
		seen[strings.ToUpper(key)] = first

		vars = append(vars, Var{Key: key, Value: value, Line: first})
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return vars, nil
}

// readValue reads everything to the right of the first =, consuming further
// lines when a quoted value has not been closed yet.
func readValue(rest string, scanner *bufio.Scanner, line *int) (string, *ParseError) {
	rest = strings.TrimLeftFunc(rest, unicode.IsSpace)

	quote, quoted := openingQuote(rest)
	if !quoted {
		// Trailing space is dropped. A shell would drop it too, and a value
		// that differs from what the file appears to say by an invisible
		// character is a bug nobody can see.
		return strings.TrimRightFunc(rest, unicode.IsSpace), nil
	}

	body := rest[1:]
	for {
		if end, ok := closingQuote(body, quote); ok {
			// What follows the closing quote is either nothing, or a comment.
			// Anything else means the line does not say what it looks like it
			// says, and returning the part before the quote would be this
			// parser choosing which half of somebody's value to keep.
			if after := strings.TrimSpace(body[end+1:]); after != "" && !strings.HasPrefix(after, "#") {
				return "", &ParseError{Reason: fmt.Sprintf(
					"the value ends at the closing %c and there is more text after it", quote)}
			}
			return unescape(body[:end], quote), nil
		}
		if !scanner.Scan() {
			return "", &ParseError{Reason: fmt.Sprintf(
				"the %c quote opened here is never closed", quote)}
		}
		*line++
		body += "\n" + scanner.Text()
	}
}

func openingQuote(s string) (byte, bool) {
	if s == "" {
		return 0, false
	}
	if s[0] == '\'' || s[0] == '"' {
		return s[0], true
	}
	return 0, false
}

// closingQuote finds the quote that ends the value, skipping one that is
// escaped. Only a double-quoted value can escape its own quote; inside single
// quotes a backslash is an ordinary character, which is the whole point of
// single quotes.
func closingQuote(body string, quote byte) (int, bool) {
	for i := 0; i < len(body); i++ {
		if quote == '"' && body[i] == '\\' {
			i++
			continue
		}
		if body[i] == quote {
			return i, true
		}
	}
	return 0, false
}

// unescape turns the escapes a double-quoted value may contain into the bytes
// they stand for. A single-quoted value is returned as it was written.
func unescape(s string, quote byte) string {
	if quote == '\'' || !strings.Contains(s, "\\") {
		return s
	}

	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i == len(s)-1 {
			b.WriteByte(s[i])
			continue
		}
		i++
		switch s[i] {
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 't':
			b.WriteByte('\t')
		case '\\', '"':
			b.WriteByte(s[i])
		default:
			// An escape this format does not define is left as it was found.
			// Swallowing the backslash would quietly change a Windows path or
			// a regular expression into a different string.
			b.WriteByte('\\')
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// Format writes variables as a file, sorted by key.
//
// Sorted rather than in the order the server returned them: a file that is
// written again after a change should differ only where the values differ, so
// that a diff is about the change and not about the order.
func Format(w io.Writer, vars []Var, header string) error {
	if header != "" {
		for _, line := range strings.Split(strings.TrimRight(header, "\n"), "\n") {
			if _, err := fmt.Fprintf(w, "# %s\n", line); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
	}

	ordered := make([]Var, len(vars))
	copy(ordered, vars)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Key < ordered[j].Key })

	for _, v := range ordered {
		if _, err := fmt.Fprintf(w, "%s=%s\n", v.Key, Quote(v.Value)); err != nil {
			return err
		}
	}
	return nil
}

// Quote renders one value so that reading it back returns what went in.
//
// Quotes are added only where they are needed. A file full of unnecessary
// quotes is harder to read and harder to diff, and most values are a word.
func Quote(value string) string {
	if value == "" || !needsQuoting(value) {
		return value
	}

	var b strings.Builder
	b.Grow(len(value) + 2)
	b.WriteByte('"')
	for i := 0; i < len(value); i++ {
		switch value[i] {
		case '"', '\\':
			b.WriteByte('\\')
			b.WriteByte(value[i])
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			b.WriteByte(value[i])
		}
	}
	b.WriteByte('"')
	return b.String()
}

// needsQuoting reports whether a value would read back differently unquoted.
//
// Leading and trailing space count, because Parse trims them: a value that
// ends in a space has to be quoted or it comes back shorter than it went out.
func needsQuoting(value string) bool {
	if strings.TrimSpace(value) != value {
		return true
	}
	// A value that begins with a quote would be read back as a quoted value,
	// and the quote it appears to open would be swallowed.
	if _, quoted := openingQuote(value); quoted {
		return true
	}
	return strings.ContainsAny(value, "\n\r\t\"\\ ")
}
