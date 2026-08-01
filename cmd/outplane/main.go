// Command outplane is the Out Plane command line interface.
//
// This file turns the declarative command registry into a runnable program and
// does nothing else. It carries no product knowledge: every command's
// behaviour, help text, examples and error codes live in internal/registry,
// and the code below walks that slice.
//
// Keeping this file thin is what makes the CLI extensible. Adding a command
// means adding a struct value, never editing main.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/outplane/cli/internal/cli"
	"github.com/outplane/cli/internal/clierr"
	"github.com/outplane/cli/internal/commands"
	"github.com/outplane/cli/internal/config"
	"github.com/outplane/cli/internal/execctx"
	"github.com/outplane/cli/internal/help"
	"github.com/outplane/cli/internal/output"
	"github.com/outplane/cli/internal/registry"
	"github.com/outplane/cli/internal/schema"
	"github.com/spf13/cobra"
)

// version is stamped by the release pipeline:
//
//	go build -ldflags "-X main.version=1.2.3"
//
// A build that was not stamped says so, and is then treated like any other
// version the server cannot read: refused. Nothing anywhere branches on this
// value. A local build that needs to reach the API stamps a version, exactly as
// a release does.
var version = "unstamped"

func main() {
	os.Exit(run())
}

func run() int {
	exec := execctx.Detect()
	root := newRoot(&exec)

	// Cobra prints its own errors and usage. Both are suppressed: errors are
	// rendered by the output writer so that machine mode gets a structured
	// envelope, and usage is our own 13-section renderer.
	root.SilenceErrors = true
	root.SilenceUsage = true

	// ExecuteC rather than Execute, because on a parse failure it returns the
	// command cobra had got as far as. That is what lets the error below name
	// the right --help.
	cmd, err := root.ExecuteC()
	if err == nil {
		return 0
	}

	// A handler's error has already been rendered, by the output writer, which
	// is the only thing that knows whether stdout wants JSON. Only the exit
	// status is left.
	var handled *clierr.Error
	if errors.As(err, &handled) {
		return clierr.ExitCodeOf(err)
	}

	// Anything else came from cobra, before a command ran: an unknown flag, an
	// unknown subcommand, a missing argument. Nothing has printed it.
	//
	// This used to fall through to `return clierr.ExitCodeOf(err)`, which gave
	// exit 1 and no output whatsoever. A silent non-zero exit is the worst
	// possible answer to a mistyped flag: a person sees nothing, and an agent
	// cannot tell a typo from a crash. Flag typos are also the most common
	// mistake there is, so this path is anything but rare.
	return renderStartupFailure(err, exec, cmd)
}

// renderStartupFailure prints an invocation cobra refused, and returns its exit
// code.
//
// It goes through the ordinary writer so that machine mode still receives the
// usual error envelope on stdout rather than a bare line of prose on stderr.
func renderStartupFailure(err error, exec execctx.Context, cmd *cobra.Command) int {
	path := "outplane"
	if cmd != nil {
		path = cmd.CommandPath()
	}

	wrapped := clierr.New(clierr.KindUsage, "%v", err).
		WithCode("usage.invalid_invocation").
		WithHint("Run `%s --help` to see what this command accepts.", path).
		WithStep("see the accepted flags and arguments", strings.Fields(path+" --help")...)

	return output.New(cmd.OutOrStdout(), cmd.ErrOrStderr(), exec).Error(wrapped)
}

// globalOverrides collects the flags that apply to every command. They are
// bound once, on the root, and cobra propagates them to every subcommand.
type globalOverrides struct {
	output  string
	asJSON  bool
	team    string
	token   string
	apiURL  string
	fields  string
	quiet   bool
	noColor bool
	dryRun  bool
	yes     bool
}

func newRoot(exec *execctx.Context) *cobra.Command {
	var g globalOverrides

	root := &cobra.Command{
		Use:   "outplane",
		Short: "Deploy and operate applications on Out Plane",
		// Running `outplane` with no arguments prints help and exits 0. It is
		// not an error to ask what a tool does.
		RunE: func(cmd *cobra.Command, args []string) error {
			printRootHelp(cmd.OutOrStdout())
			return nil
		},
		// A typo must fail hard. `outplane app remove` should hit a wall, not
		// be quietly resolved into `app rename`, because an agent that
		// hallucinated a command name needs to learn that it does not exist.
		DisableSuggestions: true,
		SilenceUsage:       true,
	}

	f := root.PersistentFlags()
	f.StringVarP(&g.output, "output", "o", "auto", "output format: auto, text, json, ndjson")
	f.BoolVar(&g.asJSON, "json", false, "shorthand for --output json")
	f.StringVar(&g.team, "team", "", "team slug or id, overriding the linked team")
	f.StringVar(&g.token, "token", "", "API token. Prefer OUTPLANE_TOKEN: argv is visible in process lists")
	f.StringVar(&g.apiURL, "api-url", "", "")
	f.StringVar(&g.fields, "fields", "", "limit structured output to these fields")
	f.BoolVarP(&g.quiet, "quiet", "q", false, "suppress everything except errors")
	f.BoolVar(&g.noColor, "no-color", false, "disable colour")
	f.BoolVar(&g.dryRun, "dry-run", false, "print the request that would be sent, without sending it")
	f.BoolVarP(&g.yes, "yes", "y", false, "acknowledge a reversible change")

	// --api-url exists for local development against a dev API and for a
	// future self-hosted deployment. Neither concerns an ordinary user, so it
	// is hidden rather than documented. OUTPLANE_API_URL does the same job.
	_ = f.MarkHidden("api-url")

	// The registry is the source of truth for the command tree. This loop is
	// the whole of the wiring.
	for _, decl := range registry.Commands {
		attach(root, decl, exec, &g)
	}

	root.AddCommand(newSchemaCommand())
	root.AddCommand(newVersionCommand())

	root.SetHelpFunc(func(cmd *cobra.Command, _ []string) {
		if decl, ok := declFor(cmd); ok {
			help.Render(cmd.OutOrStdout(), decl, registry.GlobalFlags())
			return
		}
		// A group, such as `outplane app --help`. Showing the whole command
		// surface here would discard the narrowing the reader just did.
		if cmd != root && cmd.HasSubCommands() {
			printGroupHelp(cmd.OutOrStdout(), cmd.Name())
			return
		}
		printRootHelp(cmd.OutOrStdout())
	})

	return root
}

// declarations maps a built cobra command back to the registry entry it came
// from, so that the help renderer can find the examples and automation notes.
var declarations = map[*cobra.Command]registry.Command{}

func declFor(cmd *cobra.Command) (registry.Command, bool) {
	d, ok := declarations[cmd]
	return d, ok
}

// attach creates the cobra command for one registry declaration, creating any
// missing parent groups along the way. `app list` and `app delete` share the
// `app` group, which is created once by whichever arrives first.
func attach(root *cobra.Command, decl registry.Command, exec *execctx.Context, g *globalOverrides) {
	parent := root
	for _, segment := range decl.Path[:len(decl.Path)-1] {
		parent = childOrGroup(parent, segment)
	}

	leaf := &cobra.Command{
		Use:                strings.Join(decl.Path[len(decl.Path)-1:], " "),
		Short:              decl.Short,
		Aliases:            aliasLeaves(decl.Aliases),
		DisableSuggestions: true,
		SilenceUsage:       true,
		Args:               positionalArgs(decl),
		RunE: func(cmd *cobra.Command, args []string) error {
			return execute(cmd.Context(), decl, args, cmd, exec, g)
		},
	}

	for _, fl := range decl.Flags {
		switch fl.Type {
		case "bool":
			leaf.Flags().BoolP(fl.Name, fl.Short, fl.Default == "true", fl.Description)
		default:
			leaf.Flags().StringP(fl.Name, fl.Short, fl.Default, fl.Description)
		}
	}

	declarations[leaf] = decl
	parent.AddCommand(leaf)
}

// positionalArgs enforces the argument count a command declared.
//
// Without this, cobra accepts any number of positional arguments and a handler
// reading req.Args[0] panics on an empty slice. `outplane team use` with no
// team did exactly that, printing a Go stack trace where it should have printed
// one sentence. The declaration already says which arguments are required; this
// is what makes that declaration bind.
//
// The error is a plain error rather than a *clierr.Error on purpose. It is
// raised before any command runs, so nothing has rendered it, and run() renders
// exactly the errors that are not already clierr values. Returning one here
// would make run() assume it had been printed, and the message would vanish.
func positionalArgs(decl registry.Command) cobra.PositionalArgs {
	required := 0
	variadic := false
	for _, a := range decl.Args {
		if a.Required {
			required++
		}
		if a.Variadic {
			variadic = true
		}
	}

	return func(_ *cobra.Command, args []string) error {
		if len(args) < required {
			return fmt.Errorf("missing required argument <%s>", decl.Args[len(args)].Name)
		}
		if !variadic && len(args) > len(decl.Args) {
			return fmt.Errorf("unexpected argument %q", args[len(decl.Args)])
		}
		return nil
	}
}

// childOrGroup finds an existing subcommand by name, or creates a group.
//
// A group is not runnable: `outplane app` on its own prints help, because
// there is no sensible default action and guessing one would be worse than
// asking.
//
// NoArgs is what makes `outplane app remove` fail. Cobra's default for a
// command with children is to accept any leftover word and quietly run the
// parent, so an invented subcommand printed help and exited 0. Success is the
// worst answer to give an agent that hallucinated a command name: it learns
// nothing, and whatever it expected to happen did not.
func childOrGroup(parent *cobra.Command, name string) *cobra.Command {
	for _, c := range parent.Commands() {
		if c.Name() == name {
			return c
		}
	}
	group := &cobra.Command{
		Use:                name,
		Short:              "manage " + name + "s",
		DisableSuggestions: true,
		SilenceUsage:       true,
		Args:               cobra.NoArgs,
		// A RunE is needed even though a group does nothing, because cobra
		// short-circuits a command it considers unrunnable: it prints help and
		// returns nil BEFORE validating arguments, so NoArgs above would never
		// be consulted and `outplane app remove` would exit 0. Being runnable
		// is what puts the argument check back in the path.
		RunE: func(cmd *cobra.Command, _ []string) error {
			printGroupHelp(cmd.OutOrStdout(), cmd.Name())
			return nil
		},
	}
	parent.AddCommand(group)
	return group
}

// printGroupHelp lists the commands inside one group.
//
// Separate from printRootHelp because somebody who typed `outplane app` has
// already narrowed things down, and answering with the entire command surface
// throws that away.
func printGroupHelp(w interface{ Write([]byte) (int, error) }, group string) {
	fmt.Fprintf(w, "Commands for managing %ss.\n\n", group)
	fmt.Fprintln(w, "USAGE")
	fmt.Fprintf(w, "  outplane %s <command> [flags]\n", group)
	fmt.Fprintln(w)
	fmt.Fprintln(w, "COMMANDS")

	for _, c := range sortedCommands() {
		if len(c.Path) > 1 && c.Path[0] == group {
			fmt.Fprintf(w, "  %-24s %s\n", strings.Join(c.Path, " "), c.Short)
		}
	}

	fmt.Fprintln(w)
	fmt.Fprintln(w, "LEARN MORE")
	fmt.Fprintf(w, "  outplane %s <command> --help   detailed help, with runnable examples\n", group)
	fmt.Fprintln(w)
}

// sortedCommands returns the registry in a stable display order, so that help
// and the group listing cannot drift apart.
func sortedCommands() []registry.Command {
	out := append([]registry.Command(nil), registry.Commands...)
	sort.Slice(out, func(i, j int) bool {
		return strings.Join(out[i].Path, " ") < strings.Join(out[j].Path, " ")
	})
	return out
}

// aliasLeaves converts full alias paths to the last segment, which is what
// cobra registers. "deploy" as an alias of "deploy create" becomes the alias
// "deploy" on the leaf, reachable as `outplane deploy`.
func aliasLeaves(aliases []string) []string {
	out := make([]string, 0, len(aliases))
	for _, a := range aliases {
		parts := strings.Fields(a)
		if len(parts) > 0 {
			out = append(out, parts[len(parts)-1])
		}
	}
	return out
}

// execute runs one command: resolve context, run, render.
//
// Every command follows this path, which is what makes behaviour consistent.
// A command that printed its own output or read its own configuration would be
// the first step toward each command behaving slightly differently.
func execute(
	ctx context.Context,
	decl registry.Command,
	args []string,
	cmd *cobra.Command,
	exec *execctx.Context,
	g *globalOverrides,
) error {
	applyGlobals(exec, g)

	rt, err := cli.Build(*exec, config.Overrides{
		APIURL: g.apiURL,
		Token:  g.token,
		Team:   g.team,
	}, version, cmd.OutOrStdout(), cmd.ErrOrStderr())
	if err != nil {
		return renderAndReturn(nil, err, *exec, cmd)
	}
	rt.DryRun = g.dryRun
	if g.fields != "" {
		rt.Fields = splitFields(g.fields)
	}

	if err := rejectSuppressedGlobals(decl, cmd); err != nil {
		return finish(rt.Out.Error(err), err)
	}

	handler, ok := commands.Lookup(decl.Path)
	if !ok {
		// Declared but not implemented. Say so plainly: a command that
		// silently does nothing is worse than one that admits it.
		err := clierr.New(clierr.KindInternal,
			"outplane %s is not implemented yet", strings.Join(decl.Path, " ")).
			WithHint("The interface is settled; the implementation is not.").
			WithStep("see the interface it will have", append([]string{"outplane"}, append(decl.Path, "--help")...)...)
		return finish(rt.Out.Error(err), err)
	}

	result, err := handler(ctx, commands.Request{
		CLI:   rt,
		Args:  args,
		Flags: flagValues(cmd, decl),
	})
	if err != nil {
		return finish(rt.Out.Error(err), err)
	}
	// The registry says which fields this command offers, so --fields can be
	// checked against the contract rather than against this one response.
	result.Declared = declaredFields(decl)

	if err := rt.Out.Result(result, rt.Fields); err != nil {
		return finish(rt.Out.Error(err), err)
	}
	return nil
}

// rejectSuppressedGlobals fails when a command was given a global flag it
// declared as not applying to it.
//
// The global flags are registered once, on the root, so cobra accepts every one
// of them everywhere. SuppressGlobals hides the inapplicable ones from --help,
// and hiding alone would leave `outplane team use beta --team acme` quietly
// accepted while doing nothing with --team. Accepting an argument and ignoring
// it is the failure this whole codebase keeps trying to avoid: the caller is
// told nothing and believes something happened.
func rejectSuppressedGlobals(decl registry.Command, cmd *cobra.Command) error {
	for _, name := range decl.SuppressGlobals {
		if !cmd.Flags().Changed(name) {
			continue
		}
		path := strings.Join(decl.Path, " ")
		return clierr.New(clierr.KindUsage, "--%s does not apply to `outplane %s`", name, path).
			WithCode("usage.flag_not_applicable").
			WithHint("It is accepted by other commands, but this one decides %s another way. "+
				"Run `outplane %s --help` to see what it takes.", name, path)
	}
	return nil
}

func declaredFields(decl registry.Command) []string {
	names := make([]string, 0, len(decl.OutputFields))
	for _, f := range decl.OutputFields {
		names = append(names, f.Name)
	}
	return names
}

// finish returns an error carrying the right exit code, without letting cobra
// print anything: the output writer already rendered it to the correct stream
// in the correct format.
func finish(_ int, err error) error { return err }

func renderAndReturn(_ any, err error, exec execctx.Context, cmd *cobra.Command) error {
	fmt.Fprintln(cmd.ErrOrStderr(), "Error:", err)
	return err
}

// applyGlobals folds the parsed global flags into the execution context, so
// that every later decision reads one struct instead of consulting flags.
func applyGlobals(exec *execctx.Context, g *globalOverrides) {
	if g.asJSON {
		exec.RequestedFormat = execctx.FormatJSON
	} else if g.output != "" && g.output != "auto" {
		exec.RequestedFormat = execctx.Format(g.output)
	}
	exec.Quiet = g.quiet
	exec.AssumeYes = g.yes
	if g.noColor {
		exec.NoColour = true
	}
}

func splitFields(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// flagValues collects the declared flags into a map for the Run function, so
// that command code never touches cobra.
func flagValues(cmd *cobra.Command, decl registry.Command) commands.Flags {
	values := make(commands.Flags, len(decl.Flags))
	for _, fl := range decl.Flags {
		switch fl.Type {
		case "bool":
			v, _ := cmd.Flags().GetBool(fl.Name)
			values[fl.Name] = v
		default:
			v, _ := cmd.Flags().GetString(fl.Name)
			values[fl.Name] = v
		}
	}
	return values
}

// newSchemaCommand builds `outplane schema`.
//
// It runs before authentication, before configuration loading and before any
// network access, because it is the entry point an agent uses to discover
// everything else. Requiring a token here would leave an agent unable to find
// out how to obtain one.
func newSchemaCommand() *cobra.Command {
	return &cobra.Command{
		Use:                "schema [command path]",
		Short:              "print the machine-readable command surface",
		DisableSuggestions: true,
		SilenceUsage:       true,
		RunE: func(cmd *cobra.Command, args []string) error {
			doc := schema.Build(registry.Commands, version)
			if len(args) > 0 {
				doc = doc.Filter(args)
				if len(doc.Commands) == 0 {
					return clierr.New(clierr.KindUsage, "no such command: %s", strings.Join(args, " "))
				}
			}
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			enc.SetEscapeHTML(false)
			return enc.Encode(doc)
		},
	}
}

func newVersionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "print the CLI version",
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Fprintln(cmd.OutOrStdout(), "outplane "+version)
			return nil
		},
	}
}

func printRootHelp(w interface{ Write([]byte) (int, error) }) {
	fmt.Fprintln(w, "Deploy and operate applications on Out Plane.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "USAGE")
	fmt.Fprintln(w, "  outplane <command> [flags]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "COMMANDS")

	for _, c := range sortedCommands() {
		fmt.Fprintf(w, "  %-24s %s\n", strings.Join(c.Path, " "), c.Short)
	}

	fmt.Fprintln(w)
	fmt.Fprintln(w, "LEARN MORE")
	fmt.Fprintln(w, "  outplane <command> --help   detailed help, with runnable examples")
	fmt.Fprintln(w, "  outplane schema            the machine-readable command surface,")
	fmt.Fprintln(w, "                             usable with no authentication and no network")
	fmt.Fprintln(w)
}
