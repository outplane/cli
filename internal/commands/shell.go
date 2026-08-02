package commands

import (
	"context"
	"io"
	"os"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/outplane/cli/internal/api"
	"github.com/outplane/cli/internal/clierr"
	"github.com/outplane/cli/internal/core"
	"github.com/outplane/cli/internal/output"
)

func init() {
	register("app shell", appShell)
}

// appShell opens a terminal on one running instance.
//
// Every other command in the CLI asks a question and renders an answer. This
// one hands the terminal over: from the moment the session opens, what is on
// the screen belongs to a process on the far end, and this command's only job
// is to stay out of its way and put things back afterwards.
func appShell(ctx context.Context, req Request) (output.Table, error) {
	app, err := targetApp(ctx, req, "app", "shell")
	if err != nil {
		return output.Table{}, err
	}

	client, err := req.CLI.APIClient()
	if err != nil {
		return output.Table{}, err
	}

	instance, err := shellInstance(ctx, req, client, app)
	if err != nil {
		return output.Table{}, err
	}
	command := strings.TrimSpace(req.Flags.String("command"))

	// Before the terminal check rather than after it. A dry run opens nothing,
	// so it is the one form of this command that works in a pipe, and that is
	// where somebody or something asks what it would do.
	if req.CLI.DryRun {
		req.CLI.Out.Note("Would open a session on %s, running %s. Nothing was sent.",
			instance, shellCommandName(command))
		return shellTable(app, instance, command, false), nil
	}

	if err := requireTerminal(req); err != nil {
		return output.Table{}, err
	}

	session, err := core.OpenShell(ctx, client, app.ID, instance, command)
	if err != nil {
		return output.Table{}, err
	}
	defer func() { _ = session.Close() }()

	if err := runSession(ctx, req, session); err != nil {
		return output.Table{}, err
	}
	// The session wrote its own bytes, so there is nothing left to render.
	return output.Table{Streamed: true}, nil
}

// runSession puts the local terminal in raw mode and joins the two ends.
//
// Raw mode is what makes this a terminal rather than a line reader: no local
// echo, no line buffering, and no signals from the keyboard. Ctrl-C reaches the
// program running on the instance, exactly as it would if the reader were
// sitting in front of it, instead of stopping the CLI.
func runSession(ctx context.Context, req Request, session *core.Shell) error {
	stdin := int(os.Stdin.Fd())

	state, err := term.MakeRaw(stdin)
	if err != nil {
		return clierr.New(clierr.KindInternal, "could not put the terminal in raw mode: %v", err).
			WithHint("A shell needs the terminal to itself. Nothing was opened.")
	}
	// Restoring matters more than anything else in this file. A terminal left
	// raw echoes nothing and obeys no key, and the reader's next command is
	// typed into what looks like a hung machine.
	defer func() { _ = term.Restore(stdin, state) }()

	// Cancelled when the session ends, so the two helper goroutines stop with
	// it rather than outliving the connection they were serving.
	ctx, stop := context.WithCancel(ctx)
	defer stop()

	// Sent before anything is drawn. The far end starts at a size the reader
	// does not have, and a prompt wrapped at the wrong width would be the first
	// thing they saw.
	if cols, rows, err := term.GetSize(stdin); err == nil {
		_ = session.Resize(ctx, cols, rows)
	}

	go session.Keepalive(ctx)
	go watchSize(ctx, session, stdin)
	go sendInput(ctx, session, os.Stdin)

	return session.Stream(ctx, req.CLI.Out.Out)
}

// sendInput forwards the keyboard until the session ends.
//
// It reports nothing, because there is nobody to report to and nothing useful
// to say: a read of standard input cannot be cancelled, so this goroutine sits
// blocked on a keypress until the process exits. The session's end is noticed
// by the read loop, which is the one talking to the reader.
func sendInput(ctx context.Context, session *core.Shell, in io.Reader) {
	buf := make([]byte, 4096)
	for {
		n, err := in.Read(buf)
		if n > 0 {
			if sendErr := session.Send(ctx, buf[:n]); sendErr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

// watchSize follows the window by asking rather than by waiting to be told.
//
// A resize signal would be immediate and costs a file per platform: Windows has
// no SIGWINCH and the constant does not compile there. Reading the size four
// times a second is one system call each, works on every target, and lags by
// less than the time it takes to let go of a window edge.
func watchSize(ctx context.Context, session *core.Shell, fd int) {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()

	cols, rows, _ := term.GetSize(fd)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			current, currentRows, err := term.GetSize(fd)
			if err != nil || (current == cols && currentRows == rows) {
				continue
			}
			cols, rows = current, currentRows
			if err := session.Resize(ctx, cols, rows); err != nil {
				return
			}
		}
	}
}

// requireTerminal refuses a session that would have nowhere to run.
//
// A shell needs a keyboard at one end and a screen at the other. Behind a pipe
// there is neither, and opening one anyway would leave a session on a running
// instance that nothing can type into and nothing can close.
func requireTerminal(req Request) error {
	if harness := req.CLI.Exec.AgentHarness; harness != "" {
		return clierr.New(clierr.KindUsage, "an interactive shell cannot run under %s", harness).
			WithCode("shell.not_interactive").
			WithHint("A shell is a terminal session: it has no transcript, no exit status "+
				"and no limit on what it can do to a running instance. Everything it "+
				"could be used to read has a command of its own.").
			WithStep("read what the instance is printing", "outplane", "logs", "<APP_NAME>").
			WithStep("see what is running", "outplane", "app", "instances", "<APP_NAME>").
			WithStep("see what a session would open", "outplane", "app", "shell", "--dry-run")
	}

	if !req.CLI.Exec.StdinTTY || !req.CLI.Exec.StdoutTTY {
		return clierr.New(clierr.KindUsage, "this command needs a terminal").
			WithCode("shell.not_interactive").
			WithHint("Standard input or output is not a terminal, so there is nothing to "+
				"type with and nothing to draw on. A session cannot be piped, redirected "+
				"or run from a script.").
			WithStep("see what a session would open", "outplane", "app", "shell", "--dry-run")
	}
	return nil
}

// shellInstance decides which copy of the application to open.
//
// An application running three instances has three shells, and they are not
// interchangeable: a file written in one is in one. Naming one is therefore
// normal rather than advanced, and what is chosen is always reported.
func shellInstance(ctx context.Context, req Request, client *api.Client, app core.App) (string, error) {
	instances, err := core.AppInstances(ctx, client, app.ID)
	if err != nil {
		return "", err
	}

	if named := strings.TrimSpace(req.Flags.String("instance")); named != "" {
		for _, instance := range instances {
			if instance.Name == named {
				return named, nil
			}
		}
		return "", unknownInstance(app, named, instances)
	}

	if pick := firstInstance(instances, func(i core.Instance) bool { return i.Ready }); pick != "" {
		return pick, nil
	}

	// Running but not ready is the state a failing health check leaves behind,
	// and it is when a terminal is most wanted. Refusing to open one would
	// withhold the tool exactly where it earns its place.
	if pick := firstInstance(instances, func(i core.Instance) bool { return i.Phase == "Running" }); pick != "" {
		req.CLI.Out.Note("No instance of %s is ready. Opening %s, which is running.", app.Name, pick)
		return pick, nil
	}
	return "", noInstance(app, instances)
}

func firstInstance(instances []core.Instance, match func(core.Instance) bool) string {
	for _, instance := range instances {
		if match(instance) {
			return instance.Name
		}
	}
	return ""
}

func unknownInstance(app core.App, named string, instances []core.Instance) error {
	e := clierr.New(clierr.KindNotFound, "%s is not running an instance called %q", app.Name, named).
		WithCode("shell.instance_not_found").
		WithStep("list the instances", "outplane", "app", "instances", app.Name)
	if names := instanceNames(instances); len(names) > 0 {
		return e.WithHint("It is running: %s. An instance is renamed every time it "+
			"restarts, so a name from a minute ago may already be gone.",
			strings.Join(names, ", "))
	}
	return e.WithHint("It is running nothing at all.")
}

func noInstance(app core.App, instances []core.Instance) error {
	e := clierr.New(clierr.KindNotFound, "%s has no instance to open a shell on", app.Name).
		WithCode("shell.no_instance").
		WithStep("see why", "outplane", "app", "get", app.Name)
	if len(instances) == 0 {
		return e.WithHint("It is running nothing. A paused application and one that has " +
			"never deployed both look like this.")
	}
	return e.WithHint("Its instances are %s. A shell needs one that has started.",
		strings.Join(instancePhases(instances), ", "))
}

func instanceNames(instances []core.Instance) []string {
	names := make([]string, 0, len(instances))
	for _, instance := range instances {
		names = append(names, instance.Name)
	}
	return names
}

func instancePhases(instances []core.Instance) []string {
	phases := make([]string, 0, len(instances))
	for _, instance := range instances {
		phases = append(phases, instance.Name+" ("+instance.Phase+")")
	}
	return phases
}

// shellCommandName is what will be run, for a message that has to name it
// before the server has decided.
func shellCommandName(command string) string {
	if command == "" {
		return core.DefaultShellCommand
	}
	return command
}

func shellTable(app core.App, instance, command string, connected bool) output.Table {
	return output.Table{
		Single:  true,
		Columns: []string{"app", "instance", "command", "connected"},
		Total:   1,
		Rows: []map[string]any{{
			"app":       app.Name,
			"appId":     app.ID,
			"instance":  instance,
			"command":   shellCommandName(command),
			"connected": connected,
		}},
	}
}
