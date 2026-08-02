package envfile

import (
	"strings"
	"testing"
)

// The format has no specification, so these cases are the specification. Each
// one is a shape that appears in a real file and that a looser reader gets
// wrong: a password with a #, a certificate over several lines, a value that
// ends in a space.
func TestParse(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []Var
	}{
		{
			name: "plain",
			in:   "FOO=bar\nBAZ=qux\n",
			want: []Var{{Key: "FOO", Value: "bar"}, {Key: "BAZ", Value: "qux"}},
		},
		{
			name: "comments, blank lines and export",
			in:   "# a note\n\n  # indented\nexport FOO=bar\n",
			want: []Var{{Key: "FOO", Value: "bar"}},
		},
		{
			name: "a hash is part of the value",
			in:   "PASSWORD=pa55word#1\n",
			want: []Var{{Key: "PASSWORD", Value: "pa55word#1"}},
		},
		{
			name: "an equals sign is part of the value",
			in:   "DSN=postgres://u:p@h/db?a=b&c=d\n",
			want: []Var{{Key: "DSN", Value: "postgres://u:p@h/db?a=b&c=d"}},
		},
		{
			name: "single quotes are literal",
			in:   `LITERAL='a\nb #c "d"'` + "\n",
			want: []Var{{Key: "LITERAL", Value: `a\nb #c "d"`}},
		},
		{
			name: "double quotes understand escapes",
			in:   `KEY="line1\nline2\ttabbed \"quoted\" back\\slash"` + "\n",
			want: []Var{{Key: "KEY", Value: "line1\nline2\ttabbed \"quoted\" back\\slash"}},
		},
		{
			name: "an unknown escape is left alone",
			in:   `PATTERN="\d+\w"` + "\n",
			want: []Var{{Key: "PATTERN", Value: `\d+\w`}},
		},
		{
			name: "a quoted value may span lines",
			in:   "CERT=\"-----BEGIN-----\nline\n-----END-----\"\nAFTER=1\n",
			want: []Var{
				{Key: "CERT", Value: "-----BEGIN-----\nline\n-----END-----"},
				{Key: "AFTER", Value: "1"},
			},
		},
		{
			name: "surrounding space is dropped, quoted space is kept",
			in:   "A=  bar  \nB=\"  bar  \"\n",
			want: []Var{{Key: "A", Value: "bar"}, {Key: "B", Value: "  bar  "}},
		},
		{
			name: "an empty value is a value",
			in:   "EMPTY=\n",
			want: []Var{{Key: "EMPTY", Value: ""}},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Parse(strings.NewReader(c.in))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if len(got) != len(c.want) {
				t.Fatalf("got %d variables, want %d: %+v", len(got), len(c.want), got)
			}
			for i := range got {
				if got[i].Key != c.want[i].Key || got[i].Value != c.want[i].Value {
					t.Errorf("[%d] got %q=%q, want %q=%q",
						i, got[i].Key, got[i].Value, c.want[i].Key, c.want[i].Value)
				}
			}
		})
	}
}

func TestParseRejects(t *testing.T) {
	cases := map[string]string{
		"no equals sign":               "FOO bar\n",
		"empty name":                   "=bar\n",
		"unclosed quote":               "FOO=\"bar\n",
		"the same key twice":           "FOO=a\nFOO=b\n",
		"the same key in another case": "foo=a\nFOO=b\n",
	}

	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse(strings.NewReader(in)); err == nil {
				t.Fatalf("Parse accepted %q", in)
			}
		})
	}
}

// Anything written has to read back byte for byte, or `env pull` followed by
// `env push` would change values nobody touched.
func TestRoundTrip(t *testing.T) {
	values := []string{
		"plain",
		"",
		"with spaces",
		"trailing space ",
		" leading space",
		"hash#inside",
		"quote\"inside",
		"back\\slash",
		"new\nline",
		"tab\there",
		"carriage\rreturn",
		"'single quoted looking'",
		"\"double quoted looking\"",
		"-----BEGIN KEY-----\nabc\n-----END KEY-----",
		"türkçe değerler 🚀",
		"a=b=c",
	}

	vars := make([]Var, 0, len(values))
	for i, v := range values {
		vars = append(vars, Var{Key: "K" + string(rune('A'+i)), Value: v})
	}

	var b strings.Builder
	if err := Format(&b, vars, "written by a test"); err != nil {
		t.Fatalf("Format: %v", err)
	}

	back, err := Parse(strings.NewReader(b.String()))
	if err != nil {
		t.Fatalf("Parse of what Format wrote: %v\n%s", err, b.String())
	}
	if len(back) != len(vars) {
		t.Fatalf("got %d variables back, wrote %d", len(back), len(vars))
	}

	got := map[string]string{}
	for _, v := range back {
		got[v.Key] = v.Value
	}
	for _, v := range vars {
		if got[v.Key] != v.Value {
			t.Errorf("%s came back as %q, went out as %q", v.Key, got[v.Key], v.Value)
		}
	}
}
