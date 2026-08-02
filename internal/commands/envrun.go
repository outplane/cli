package commands

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"github.com/outplane/cli/internal/clierr"
	"github.com/outplane/cli/internal/core"
	"github.com/outplane/cli/internal/output"
)

func init() {
	register("env run", envRun)
}

// envRun runs a local program with the application's variables in its
// environment.
//
// It is the answer to the reason `env pull` exists at all: most people want a
// local process to see the deployed configuration, and writing the values to
// disk to achieve that leaves them on disk. Here they exist for as long as the
// program runs and are never written anywhere.
//
// The program is executed directly, never through a shell. `env run -- rm -rf
// $HOME` therefore passes two literal arguments to rm and expands nothing,
// which is the only defensible behaviour for a command whose whole job is to
// hand somebody else's arguments to an executable. Shell syntax is available by
// asking for a shell: `env run -- sh -c "..."`.
func envRun(ctx context.Context, req Request) (output.Table, error) {
	if len(req.Args) == 0 || strings.TrimSpace(req.Args[0]) == "" {
		return output.Table{}, clierr.New(clierr.KindUsage, "no command given").
			WithCode("usage.missing_argument").
			WithHint("Everything after -- is run with the application's variables in its "+
				"environment. The -- is what keeps its flags from being read as this "+
				"command's flags.").
			WithStep("run a local server", "outplane", "env", "run", "--", "npm", "start").
			WithStep("run a one-off task", "outplane", "env", "run", "--", "python", "manage.py", "migrate")
	}

	app, err := flagApp(ctx, req, "env", "run")
	if err != nil {
		return output.Table{}, err
	}

	client, err := req.CLI.APIClient()
	if err != nil {
		return output.Table{}, err
	}

	vars, err := core.ListEnv(ctx, client, app.ID)
	if err != nil {
		return output.Table{}, err
	}

	if req.CLI.DryRun {
		req.CLI.Out.Note("Would run %s with %s from %s. Nothing was run.",
			strings.Join(req.Args, " "), plural(len(vars), "variable"), app.Name)
		return envRunTable(app, req.Args, vars, false), nil
	}

	req.CLI.Out.Note("Running %s with %s from %s.",
		strings.Join(req.Args, " "), plural(len(vars), "variable"), app.Name)

	// The table is marked as already written, in every format. The program owns
	// this command's output from here, and a report printed underneath it would
	// land in the middle of whatever was reading it.
	return envRunTable(app, req.Args, vars, true), runChild(ctx, req, vars)
}

// runChild starts the program, waits for it, and exits the way it did.
func runChild(ctx context.Context, req Request, vars []core.EnvVar) error {
	child := exec.Command(req.Args[0], req.Args[1:]...)
	child.Env = childEnv(vars, req.Flags.Bool("pure"))

	// Straight through, all three. A program that asks a question has to be
	// able to read the answer, and one that draws a progress bar has to reach
	// the same terminal the CLI was given.
	child.Stdin = os.Stdin
	child.Stdout = req.CLI.Out.Out
	child.Stderr = req.CLI.Out.Err

	if err := child.Start(); err != nil {
		return startError(req.Args[0], err)
	}

	// An interrupt from the keyboard reaches the child on its own, because it
	// shares this process group. A signal sent to the CLI alone does not, and
	// without this the CLI would return while the child kept running with the
	// terminal. Cancellation is passed on rather than the process being killed
	// outright, so the child gets to shut itself down.
	stopped := make(chan struct{})
	defer close(stopped)
	go func() {
		select {
		case <-ctx.Done():
			_ = child.Process.Signal(os.Interrupt)
		case <-stopped:
		}
	}()

	err := child.Wait()
	if err == nil {
		return nil
	}

	// The child's own status becomes ours, with nothing added. A caller testing
	// the exit code is asking about the program they ran.
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return clierr.Exit(childStatus(exit))
	}
	return clierr.New(clierr.KindInternal, "%s could not be run: %v", req.Args[0], err).
		WithCode("env.run_failed")
}

// childStatus is the exit code to report for a program that failed.
//
// A program killed by a signal has no exit code of its own: Go reports -1 for
// it, which would become 255 and mean nothing. Every shell answers this the
// same way and so does this, with 128 plus the signal number, so that a build
// killed for using too much memory reports the 137 a pipeline is looking for
// rather than a number that appears nowhere else.
//
// On Windows nothing is ever signalled and the first branch never runs.
func childStatus(exit *exec.ExitError) int {
	if status, ok := exit.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		return 128 + int(status.Signal())
	}
	if code := exit.ExitCode(); code >= 0 {
		return code
	}
	// Neither an exit code nor a signal, which should not happen. Failing as
	// 1 is at least a failure; -1 would be reported as success on some shells
	// and as 255 on others.
	return 1
}

// childEnv is the environment the program runs in.
//
// The local environment first and the application's variables on top, so that
// PATH, HOME and everything else a program needs to start still exist and the
// deployed values win where the two disagree. That order is the whole contract:
// the point of this command is to run against the deployed configuration.
//
// --pure drops the local environment, for the case where the question is what
// the application itself would see. It is genuinely different and genuinely
// rarer, which is why it is a flag rather than the default: most programs
// cannot even find their own interpreter without PATH.
func childEnv(vars []core.EnvVar, pure bool) []string {
	var env []string
	if !pure {
		env = os.Environ()
	}
	for _, v := range vars {
		env = append(env, v.Key+"="+v.Value)
	}
	return env
}

// startError explains a program that never ran.
//
// Not found is by far the common case and deserves its own message: a shell
// answers it with 127 and this CLI answers it with a usage error, because the
// mistake is in the invocation and the exit code table is the CLI's contract
// with whatever is reading it.
func startError(name string, err error) error {
	if errors.Is(err, exec.ErrNotFound) {
		return clierr.New(clierr.KindUsage, "there is no program called %q on PATH", name).
			WithCode("env.command_not_found").
			WithHint("Everything after -- is run directly, not through a shell, so a shell "+
				"built-in or a pipeline has to be given to a shell explicitly.").
			WithStep("use a shell for shell syntax", "outplane", "env", "run", "--",
				"sh", "-c", "<COMMAND>")
	}
	if errors.Is(err, os.ErrPermission) {
		return clierr.New(clierr.KindUsage, "%s cannot be executed by this user", name).
			WithCode("env.command_not_executable")
	}
	return clierr.New(clierr.KindInternal, "%s could not be started: %v", name, err).
		WithCode("env.run_failed")
}

// envRunTable describes the invocation, never its output.
//
// The values are not here and the keys are: a record of which variables a task
// ran with is useful, and a record of what they were is a leak into whatever
// collected it.
func envRunTable(app core.App, argv []string, vars []core.EnvVar, streamed bool) output.Table {
	return output.Table{
		Streamed: streamed,
		Single:   true,
		Columns:  []string{"app", "command", "variables"},
		Total:    1,
		Rows: []map[string]any{{
			"app":       app.Name,
			"appId":     app.ID,
			"command":   strings.Join(argv, " "),
			"argv":      argv,
			"variables": len(vars),
			"keys":      keysOf(vars),
		}},
	}
}
