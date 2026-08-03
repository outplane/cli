package registry

import (
	"strings"
	"testing"
)

// The declarations are data, and data can be wrong in ways a compiler cannot
// see. Two of these have already happened: a short flag that collided with a
// global panicked the whole binary on every command, and a Related entry named
// a command that had been renamed. Both are one-line mistakes in a struct
// literal, both are invisible in review, and both are found here in
// milliseconds.

func paths() map[string]Command {
	byPath := make(map[string]Command, len(Commands))
	for _, c := range Commands {
		byPath[strings.Join(c.Path, " ")] = c
	}
	return byPath
}

func TestEveryCommandIsUsable(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range Commands {
		path := strings.Join(c.Path, " ")
		t.Run(path, func(t *testing.T) {
			if len(c.Path) == 0 {
				t.Fatal("no path")
			}
			if seen[path] {
				t.Fatalf("declared twice")
			}
			seen[path] = true

			if c.Risk == RiskUnset {
				t.Error("no risk: an unmarked command is UNKNOWN, not safe")
			}
			if strings.TrimSpace(c.Short) == "" {
				t.Error("no summary")
			}
			if strings.HasSuffix(c.Short, ".") {
				t.Error("the summary ends with a period; it sits in a listing")
			}
			if strings.TrimSpace(c.Long) == "" {
				t.Error("no long description")
			}
			if len(c.ExitCodes) == 0 {
				t.Error("no exit codes")
			}
		})
	}
}

// A short letter that a global already uses makes cobra panic when the command
// tree is built, which takes down every command rather than the one at fault.
func TestNoFlagShorthandCollides(t *testing.T) {
	globals := map[string]string{}
	for _, g := range GlobalFlags() {
		if g.Short != "" {
			globals[g.Short] = g.Name
		}
	}

	for _, c := range Commands {
		path := strings.Join(c.Path, " ")
		local := map[string]string{}
		for _, f := range c.Flags {
			if f.Short == "" {
				continue
			}
			if other, taken := globals[f.Short]; taken && other != f.Name {
				t.Errorf("%s: -%s is --%s locally and --%s globally", path, f.Short, f.Name, other)
			}
			if other, taken := local[f.Short]; taken {
				t.Errorf("%s: -%s is both --%s and --%s", path, f.Short, f.Name, other)
			}
			local[f.Short] = f.Name
		}
	}
}

func TestFlagsAreWellFormed(t *testing.T) {
	types := map[string]bool{"bool": true, "string": true, "int": true, "duration": true, "strings": true}

	for _, c := range Commands {
		path := strings.Join(c.Path, " ")
		seen := map[string]bool{}
		for _, f := range c.Flags {
			if !types[f.Type] {
				t.Errorf("%s: --%s has type %q, which the tree builder does not know", path, f.Name, f.Type)
			}
			if strings.HasPrefix(f.Name, "-") {
				t.Errorf("%s: --%s is declared with dashes", path, f.Name)
			}
			if strings.TrimSpace(f.Description) == "" {
				t.Errorf("%s: --%s has no description", path, f.Name)
			}
			if seen[f.Name] {
				t.Errorf("%s: --%s is declared twice", path, f.Name)
			}
			seen[f.Name] = true
		}
	}
}

// Only the last argument may absorb the rest, and a required argument cannot
// follow an optional one: the tree builder counts positions and both mistakes
// make it count the wrong thing.
func TestArgumentsAreOrdered(t *testing.T) {
	for _, c := range Commands {
		path := strings.Join(c.Path, " ")
		optionalSeen := false
		for i, a := range c.Args {
			if strings.TrimSpace(a.Name) == "" {
				t.Errorf("%s: argument %d has no name", path, i)
			}
			if a.Variadic && i != len(c.Args)-1 {
				t.Errorf("%s: %s is variadic but not last", path, a.Name)
			}
			if a.Required && optionalSeen {
				t.Errorf("%s: %s is required after an optional argument", path, a.Name)
			}
			if !a.Required {
				optionalSeen = true
			}
		}
	}
}

// Related and every runnable example point at real commands. This is the audit
// that used to be done by hand after each new group.
func TestNothingPointsAtACommandThatDoesNotExist(t *testing.T) {
	known := paths()
	// A group such as `outplane app` is a real thing to name even though it is
	// not a command of its own.
	groups := map[string]bool{}
	for path := range known {
		if parts := strings.Fields(path); len(parts) > 1 {
			for i := 1; i < len(parts); i++ {
				groups[strings.Join(parts[:i], " ")] = true
			}
		}
	}
	// schema and version are real commands built directly on the root rather
	// than declared here, because they run before configuration and before
	// authentication. They are nameable all the same.
	outside := map[string]bool{"schema": true, "version": true}
	exists := func(path string) bool {
		return known[path].Short != "" || groups[path] || outside[path]
	}

	for _, c := range Commands {
		path := strings.Join(c.Path, " ")
		for _, r := range c.Related {
			if !exists(r) {
				t.Errorf("%s: Related names %q, which does not exist", path, r)
			}
			if r == path {
				t.Errorf("%s: Related names itself", path)
			}
		}

		for _, ex := range c.Examples {
			if len(ex.Argv) == 0 {
				t.Errorf("%s: example %q has no argv", path, ex.Title)
				continue
			}
			if ex.Argv[0] != "outplane" {
				t.Errorf("%s: example %q starts with %q", path, ex.Title, ex.Argv[0])
			}
			if !invokes(ex.Argv, c.Path) {
				t.Errorf("%s: example %q runs `%s`, which is a different command",
					path, ex.Title, strings.Join(ex.Argv, " "))
			}
		}
	}
}

// invokes reports whether an example's argv actually runs the command it is
// declared under. An example that quietly runs something else is worse than no
// example: it is copied, it works, and it teaches the wrong thing.
func invokes(argv []string, cmd []string) bool {
	if len(argv) < len(cmd)+1 {
		return false
	}
	for i, part := range cmd {
		if argv[i+1] != part {
			return false
		}
	}
	return true
}

// The rules the plan sets for what an example has to cover. They exist because
// an agent reads examples before it reads prose.
func TestExamplesCoverWhatAnAgentNeeds(t *testing.T) {
	for _, c := range Commands {
		path := strings.Join(c.Path, " ")
		t.Run(path, func(t *testing.T) {
			if len(c.Examples) < 3 {
				t.Errorf("%d examples, and a leaf command needs at least 3", len(c.Examples))
			}

			// A command with no declared fields has no structured output to
			// read, so asking it to show --json would be showing a flag that
			// does nothing.
			needsMachine := len(c.OutputFields) > 0

			// Some commands have no form that changes nothing: signing in,
			// signing out, linking a directory, handing over the terminal.
			// Requiring a safe example there would mean inventing one.
			alwaysActs := map[string]bool{
				"login": true, "logout": true, "link": true, "unlink": true,
				"team use": true, "update": true, "app shell": true,
			}

			machine, safe, dry := false, false, false
			for _, ex := range c.Examples {
				for i, arg := range ex.Argv {
					switch arg {
					case "--json", "-o", "--output":
						machine = true
					case "--dry-run":
						dry = true
					}
					_ = i
				}
				if ex.Risk == RiskRead {
					safe = true
				}
			}

			if needsMachine && !machine {
				t.Error("no example uses --json or -o, which is how an agent reads output")
			}
			if !safe && !alwaysActs[path] {
				t.Error("no example is safe to run as written")
			}
			if c.SupportsDryRun && !dry {
				t.Error("--dry-run is supported and no example shows it")
			}
		})
	}
}

// A long-running or asynchronous command that says nothing about it is the
// single most common way an agent reports a queued build as a finished one.
func TestLongRunningCommandsExplainThemselves(t *testing.T) {
	for _, c := range Commands {
		if (c.LongRunning || c.Streams != StreamNone) && len(c.AutomationNotes) == 0 {
			t.Errorf("%s: long-running or streaming, with no automation notes",
				strings.Join(c.Path, " "))
		}
	}
}

// Mutating commands need a way to be tried first. The exceptions are commands
// whose whole effect is local or immediate, and they are listed rather than
// inferred, so that adding a mutating command without --dry-run is a decision
// somebody makes on purpose.
func TestMutatingCommandsCanBeTriedFirst(t *testing.T) {
	exempt := map[string]bool{
		"login": true, "logout": true, "link": true, "unlink": true,
		"team use": true, "update": true, "app shell": true, "api": true,
	}

	for _, c := range Commands {
		path := strings.Join(c.Path, " ")
		if c.Risk == RiskRead || exempt[path] || c.SupportsDryRun {
			continue
		}
		t.Errorf("%s: mutating, not exempt, and has no --dry-run", path)
	}
}

// Every command needs a documentation address.
//
// It is declared by hand on each command rather than derived, and the four
// `skills` commands shipped without one: the field is optional in the struct, so
// nothing complained until the documentation generator, which groups pages by
// that address, met a command that had none. The cost of forgetting is a command
// whose help points nowhere, which is invisible until somebody follows it.
func TestEveryCommandPointsAtDocumentation(t *testing.T) {
	const prefix = "https://docs.outplane.com/cli"

	for _, c := range Commands {
		path := strings.Join(c.Path, " ")
		if c.DocsURL == "" {
			t.Errorf("%s: no DocsURL", path)
			continue
		}
		if !strings.HasPrefix(c.DocsURL, prefix) {
			t.Errorf("%s: DocsURL %q does not start with %s", path, c.DocsURL, prefix)
		}
	}
}
