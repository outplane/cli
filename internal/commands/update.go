package commands

import (
	"context"
	"os"
	"os/exec"

	"github.com/outplane/cli/internal/clierr"
	"github.com/outplane/cli/internal/install"
	"github.com/outplane/cli/internal/output"
)

func init() {
	register("update", update)
}

// update replaces this CLI with the newest release, using whichever channel
// installed it.
//
// The order below is the point of the command: work out what would happen, say
// so, and only then do it. Every path prints the install method, the binary's
// path and the exact command, so an installation the detection does not fit is
// visible before anything runs rather than mishandled quietly.
func update(ctx context.Context, req Request) (output.Table, error) {
	method := install.Detect()
	check := req.Flags.Bool("check")

	if !method.Managed() {
		// Nothing sensible to run. Guessing would install a second copy
		// somewhere else and leave this one shadowing it on PATH, which is a
		// worse problem than the one being solved.
		if check {
			describe(req, method, req.CLI.Version())
			return updateResult(method, req.CLI.Version(), false), nil
		}
		return output.Table{}, clierr.New(clierr.KindUsage,
			"this installation cannot be updated by the CLI").
			WithCode("update.unmanaged").
			WithHint("%s. Update it the same way it was installed.", method.Reason).
			WithDetail("path", method.Path)
	}

	describe(req, method, req.CLI.Version())

	if check {
		return updateResult(method, req.CLI.Version(), false), nil
	}

	if err := run(ctx, method); err != nil {
		return output.Table{}, clierr.New(clierr.KindInternal, "the update command failed: %v", err).
			WithCode("update.failed").
			WithHint("Run it directly to see what it reported: %s", method.Display)
	}

	req.CLI.Out.Note("Updated. Later commands will use the new version.")
	return updateResult(method, req.CLI.Version(), true), nil
}

// describe reports what was found, before anything is run.
func describe(req Request, method install.Method, version string) {
	req.CLI.Out.Note("Installed with %s, at %s.", method.Name, method.Path)
	req.CLI.Out.Note("Currently running %s.", version)
	if method.Managed() {
		req.CLI.Out.Note("Update command: %s", method.Display)
	}
}

// run executes the update, with the package manager's own output going straight
// to the terminal.
//
// Its output is passed through rather than captured because it is the only
// progress there is: npm can take a while, and a silent minute reads as a hang.
// A failure is reported by exit status, which is all this needs to know.
func run(ctx context.Context, method install.Method) error {
	cmd := exec.CommandContext(ctx, method.Command[0], method.Command[1:]...)
	cmd.Stdout = os.Stderr // progress is not the result; keep stdout clean for --json
	cmd.Stderr = os.Stderr
	cmd.Stdin = nil
	return cmd.Run()
}

func updateResult(method install.Method, version string, ran bool) output.Table {
	return output.Table{
		Single:  true,
		Columns: []string{"method", "path", "version", "command", "ran"},
		Total:   1,
		Rows: []map[string]any{{
			"method":  method.Name,
			"path":    nilIfEmpty(method.Path),
			"version": version,
			"command": nilIfEmpty(method.Display),
			"ran":     ran,
		}},
	}
}
