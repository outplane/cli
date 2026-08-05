package commands

import (
	"context"
	"os"
	"os/exec"
	"strings"

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
			return updateResult(method, nilVersion(req.CLI.Version()), false), nil
		}
		return output.Table{}, clierr.New(clierr.KindUsage,
			"this installation cannot be updated by the CLI").
			WithCode("update.unmanaged").
			WithHint("%s. Update it the same way it was installed.", method.Reason).
			WithDetail("path", method.Path)
	}

	describe(req, method, req.CLI.Version())

	if check {
		return updateResult(method, nilVersion(req.CLI.Version()), false), nil
	}

	if err := run(ctx, method); err != nil {
		return output.Table{}, clierr.New(clierr.KindInternal, "the update command failed: %v", err).
			WithCode("update.failed").
			WithHint("Run it directly to see what it reported: %s", method.Display)
	}

	// The version to report is the one now on disk, not the one running this
	// line. They differ by exactly the update that just happened, and printing
	// the old number beside "ran: true" told a reader the update had not
	// worked.
	installed := installedVersion(ctx, method.Path)
	if installed == "" {
		req.CLI.Out.Note("Updated, though the new version could not be read back.")
	} else {
		req.CLI.Out.Note("Updated to %s. Later commands will use it.", installed)
	}
	return updateResult(method, nilVersion(installed), true), nil
}

// installedVersion asks the binary that later commands will run what it is.
//
// The one on PATH, not the one at the path this process was started from. The
// installer puts the binary where it can write, which is usually the file that
// was already there and occasionally is not: a copy run from somewhere else, or
// a directory that stopped being writable. Reading the file this process came
// from would then report the version that did not change, which is the mistake
// this whole function exists to stop making.
//
// Empty when it cannot be read, which is reported rather than papered over with
// the version of the process asking: a number that is confidently wrong is
// worse than a missing one.
func installedVersion(ctx context.Context, path string) string {
	if onPath, err := exec.LookPath("outplane"); err == nil {
		path = onPath
	}
	if path == "" {
		return ""
	}
	out, err := exec.CommandContext(ctx, path, "version").Output()
	if err != nil {
		return ""
	}
	// `outplane 1.2.3`
	fields := strings.Fields(string(out))
	if len(fields) < 2 {
		return ""
	}
	return fields[len(fields)-1]
}

// nilVersion keeps an unreadable version out of the result as null rather than
// as an empty string, so a caller branches on absence instead of on "".
func nilVersion(v string) any {
	if v == "" {
		return nil
	}
	return v
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

func updateResult(method install.Method, version any, ran bool) output.Table {
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
