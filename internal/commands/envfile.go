package commands

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/outplane/cli/internal/api"
	"github.com/outplane/cli/internal/clierr"
	"github.com/outplane/cli/internal/core"
	"github.com/outplane/cli/internal/envfile"
	"github.com/outplane/cli/internal/output"
)

func init() {
	register("env pull", envPull)
	register("env push", envPush)
}

// A file on one side and an application on the other.
//
// The pair is deliberately not a synchronisation. `pull` overwrites the file
// and `push` adds to the application, and neither deletes anything on the far
// side, because the two ends are not copies of each other: an application also
// carries variables from its groups, and a file also carries the ones somebody
// only ever wanted locally. A command that made them identical would have to
// delete, and the thing it would delete is a colleague's variable.
//
// Removal stays where it is visible: `outplane env unset`, one name at a time.

// defaultEnvFile is what both commands use when no path is given. It is the
// name every other tool in a project already agrees on.
const defaultEnvFile = ".env"

// envPull writes an application's variables to a file.
//
// This is the one command in the CLI that puts secrets on disk in the clear,
// which is what it is for, and why the file is written readable by nobody else
// and why the reader is told where it landed.
func envPull(ctx context.Context, req Request) (output.Table, error) {
	app, err := flagApp(ctx, req, "env", "pull")
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

	path := envFilePath(req)
	keys := keysOf(vars)

	// Checked before the dry run, not after it, so that a dry run predicts the
	// refusal instead of describing a write that would not happen. A file that
	// exists holds somebody's local edits, and nothing here can tell an edit
	// from a stale copy, so the choice belongs to whoever made it.
	if err := checkOverwrite(req, path); err != nil {
		return output.Table{}, err
	}

	if req.CLI.DryRun {
		req.CLI.Out.Note("Would write %d variables from %s to %s. Nothing was written.",
			len(vars), app.Name, path)
		return envFileTable("pull", path, app, keys, 0, false), nil
	}

	entries := make([]envfile.Var, 0, len(vars))
	for _, v := range vars {
		entries = append(entries, envfile.Var{Key: v.Key, Value: v.Value})
	}

	if err := writePrivately(path, entries, app.Name); err != nil {
		return output.Table{}, err
	}

	req.CLI.Out.Note("Wrote %d variables from %s to %s.", len(vars), app.Name, path)
	if len(vars) > 0 {
		req.CLI.Out.Note("It holds real values. Keep it out of version control.")
	}
	noteGroups(ctx, req, client, app)

	return envFileTable("pull", path, app, keys, 0, true), nil
}

// envPush sets the variables in a file on the application.
//
// It sends only what differs. Sending everything would work, since the server
// merges, but a report of "3 changed" is a different thing to read from a
// report of "47 sent", and the second one hides the answer to the question the
// reader actually has.
func envPush(ctx context.Context, req Request) (output.Table, error) {
	path := envFilePath(req)

	entries, err := readEnvFile(path)
	if err != nil {
		return output.Table{}, err
	}

	app, err := flagApp(ctx, req, "env", "push")
	if err != nil {
		return output.Table{}, err
	}

	client, err := req.CLI.APIClient()
	if err != nil {
		return output.Table{}, err
	}

	current, err := core.ListEnv(ctx, client, app.ID)
	if err != nil {
		return output.Table{}, err
	}

	values, added, changed, unchanged := diffEnv(entries, current)

	// Nothing to send is a success, not a failure, and it is the ordinary result
	// of running this twice. A file with nothing in it lands here too: pushing
	// removes nothing, so an empty file asks for nothing and gets it. Refusing
	// it would mean `env pull` on an application with no variables writes a file
	// its own sibling will not read.
	if len(values) == 0 {
		if len(entries) == 0 {
			req.CLI.Out.Note("%s sets no variables, so there was nothing to send.", path)
		} else {
			req.CLI.Out.Note("%s already has every variable in %s, with the same values.",
				app.Name, path)
		}
		// The flag was given and did nothing, which is worth a sentence: a
		// deployment nobody asked about is worse than a deployment explained.
		if req.Flags.Bool("deploy") {
			req.CLI.Out.Note("Nothing changed, so --deploy started no deployment.")
		}
		return envPushTable(path, app, added, changed, unchanged, false, 0), nil
	}

	summary := fmt.Sprintf("%d new, %d changed, %d already the same",
		len(added), len(changed), len(unchanged))

	if req.CLI.DryRun {
		req.CLI.Out.Note("Would send %s to %s: %s. Nothing was sent.",
			plural(len(values), "variable"), app.Name, summary)
		noteKeys(req, "new", added)
		noteKeys(req, "changed", changed)
		return envPushTable(path, app, added, changed, unchanged, false, 0), nil
	}

	if err := core.SetEnv(ctx, client, app.ID, values); err != nil {
		return output.Table{}, err
	}
	req.CLI.Out.Note("Set %s on %s from %s: %s.",
		plural(len(values), "variable"), app.Name, path, summary)

	id, err := applyChange(ctx, req, client, app, "values")
	if err != nil {
		return output.Table{}, err
	}
	return envPushTable(path, app, added, changed, unchanged, true, id), nil
}

// diffEnv works out what a file would change, comparing keys the way the
// server does.
//
// Case-insensitively, because the server treats PATH and path as the same
// variable when it decides whether an assignment replaces one. Comparing
// case-sensitively here would report a change as new, send it, and leave the
// reader looking for a second variable that does not exist.
func diffEnv(entries []envfile.Var, current []core.EnvVar) (
	values map[string]string, added, changed, unchanged []string) {

	existing := make(map[string]core.EnvVar, len(current))
	for _, v := range current {
		existing[strings.ToUpper(strings.TrimSpace(v.Key))] = v
	}

	// Empty rather than nil, so that all three encode as [] and a consumer can
	// iterate them without checking for null first.
	added, changed, unchanged = []string{}, []string{}, []string{}

	values = make(map[string]string, len(entries))
	for _, e := range entries {
		before, ok := existing[strings.ToUpper(e.Key)]
		switch {
		case !ok:
			added = append(added, e.Key)
		case before.Value == e.Value:
			unchanged = append(unchanged, e.Key)
			continue
		default:
			changed = append(changed, e.Key)
		}
		values[e.Key] = e.Value
	}

	sort.Strings(added)
	sort.Strings(changed)
	sort.Strings(unchanged)
	return values, added, changed, unchanged
}

// readEnvFile reads and validates a file, before anything is asked of the API.
//
// Every rule the platform has is applied here, so a file with one bad name
// fails naming the line rather than after half of it has been sent.
func readEnvFile(path string) ([]envfile.Var, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, openError(path, err)
	}
	defer file.Close()

	// A directory opens and then fails on the first read, with a message about
	// reading rather than about the path being wrong.
	if info, err := file.Stat(); err == nil && info.IsDir() {
		return nil, clierr.New(clierr.KindUsage, "%s is a directory", path).
			WithCode("env.file_invalid").
			WithHint("Name the file to read, not the folder it is in.")
	}

	entries, err := envfile.Parse(file)
	if err != nil {
		var parseErr *envfile.ParseError
		if errors.As(err, &parseErr) {
			return nil, clierr.New(clierr.KindUsage, "%s could not be read: %s",
				path, parseErr.Reason).
				WithCode("env.file_invalid").
				WithDetail("line", parseErr.Line).
				WithHint("Line %d reads: %s", parseErr.Line, short(parseErr.Text))
		}
		return nil, clierr.New(clierr.KindUsage, "%s could not be read: %v", path, err).
			WithCode("env.file_invalid")
	}

	if len(entries) > core.MaxEnvVars {
		return nil, clierr.New(clierr.KindUsage,
			"%s holds %d variables, and the limit is %d", path, len(entries), core.MaxEnvVars).
			WithCode("env.too_many")
	}

	for _, e := range entries {
		if err := core.CheckEnvKey(e.Key); err != nil {
			return nil, withLine(err, path, e.Line)
		}
		if err := core.CheckEnvValue(e.Key, e.Value); err != nil {
			return nil, withLine(err, path, e.Line)
		}
	}
	return entries, nil
}

// withLine points a validation failure at the line that caused it.
//
// The rule belongs to the platform and the location belongs to the file, and a
// reader with a two hundred line file needs both. The rule's own hint is kept
// where there is one, because it explains why, and the location is added as its
// own sentence rather than folded into the message, which already reads well
// without it.
func withLine(err error, path string, line int) error {
	e := clierr.AsError(err)
	if e == nil {
		return err
	}

	location := fmt.Sprintf("It is on line %d of %s.", line, path)
	if e.Hint != "" {
		location = strings.TrimRight(e.Hint, " ") + " " + location
	}
	return e.WithHint("%s", location).
		WithDetail("file", path).
		WithDetail("line", line)
}

// checkOverwrite refuses to replace a file that is already there.
func checkOverwrite(req Request, path string) error {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return writeError(path, err)
	}
	if info.IsDir() {
		return clierr.New(clierr.KindUsage, "%s is a directory", path).
			WithCode("env.file_invalid").
			WithHint("Name the file to write, not the folder to write it in.")
	}
	if req.Flags.Bool("force") {
		return nil
	}

	return clierr.New(clierr.KindConflict, "%s already exists", path).
		WithCode("env.file_exists").
		WithHint("Whatever is in it would be lost, including variables that are only local.").
		WithStep("replace it", "outplane", "env", "pull", path, "--force").
		WithStep("or write somewhere else", "outplane", "env", "pull", ".env.remote").
		WithStep("or see what would change first", "outplane", "env", "push", path, "--dry-run")
}

// writePrivately writes the file so that only its owner can read it.
//
// Through a temporary file in the same directory and a rename, which is what
// makes the replacement atomic: a failure halfway through leaves the previous
// file intact rather than a half-written one that a deployment might read.
// Permissions are set on the temporary file, before it has any content, so the
// values are never briefly world-readable.
func writePrivately(path string, entries []envfile.Var, appName string) error {
	dir := filepath.Dir(path)
	temp, err := os.CreateTemp(dir, ".env-*")
	if err != nil {
		return writeError(path, err)
	}
	tempName := temp.Name()
	defer os.Remove(tempName) // no-op once the rename has succeeded

	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return writeError(path, err)
	}

	header := fmt.Sprintf("Written by `outplane env pull` from %s.\n"+
		"These are real values. Do not commit this file.", appName)
	if err := envfile.Format(temp, entries, header); err != nil {
		temp.Close()
		return writeError(path, err)
	}
	if err := temp.Close(); err != nil {
		return writeError(path, err)
	}
	if err := os.Rename(tempName, path); err != nil {
		return writeError(path, err)
	}
	return nil
}

// openError turns a filesystem failure into something with a next step. The
// standard library's message names the operation, which a reader does not need,
// and the path, which they do.
func openError(path string, err error) error {
	switch {
	case os.IsNotExist(err):
		return clierr.New(clierr.KindNotFound, "there is no file at %s", path).
			WithCode("env.file_not_found").
			WithStep("write one from the application", "outplane", "env", "pull", path)
	case os.IsPermission(err):
		return clierr.New(clierr.KindUsage, "%s cannot be read by this user", path).
			WithCode("env.file_unreadable")
	default:
		return clierr.New(clierr.KindInternal, "%s could not be used: %v", path, err).
			WithCode("env.file_unreadable")
	}
}

// writeError explains a file that could not be written.
//
// Reading and writing fail for different reasons, and openError's wording is
// about reading: "cannot be read" is the wrong sentence for a path this command
// was trying to create, and "there is no file" is exactly backwards when the
// thing that does not exist is the directory to put one in.
func writeError(path string, err error) error {
	switch {
	case os.IsNotExist(err):
		return clierr.New(clierr.KindNotFound, "there is no directory at %s", filepath.Dir(path)).
			WithCode("env.file_unwritable").
			WithHint("The file would go there, so it has to exist first.")
	case os.IsPermission(err):
		return clierr.New(clierr.KindUsage, "%s cannot be written by this user", path).
			WithCode("env.file_unwritable")
	default:
		return clierr.New(clierr.KindInternal, "%s could not be written: %v", path, err).
			WithCode("env.file_unwritable")
	}
}

// noteGroups says what the file does not contain.
//
// A pulled file looks like the whole environment and is not: variables from an
// assigned group reach the running container too, and they are not here because
// pushing them back would copy a shared value onto one application and quietly
// fork it. Saying so costs one request and prevents a puzzled hour.
func noteGroups(ctx context.Context, req Request, client *api.Client, app core.App) {
	groups, err := core.AppEnvGroups(ctx, client, app.ID)
	if err != nil || len(groups) == 0 {
		// Best effort. A failure here says nothing about the file that was
		// just written, so it is not worth turning a success into an error.
		return
	}

	total := 0
	names := make([]string, 0, len(groups))
	for _, g := range groups {
		total += g.Variables
		names = append(names, g.GroupName)
	}
	req.CLI.Out.Note("This file does not include the %s that %s gets from %s.",
		plural(total, "variable"), app.Name, strings.Join(names, ", "))
	req.CLI.Out.Note("Read those with: outplane env group get %s", names[0])
}

func noteKeys(req Request, label string, keys []string) {
	if len(keys) > 0 {
		req.CLI.Out.Note("  %s: %s", label, strings.Join(keys, ", "))
	}
}

// envFilePath reads the optional file argument.
func envFilePath(req Request) string {
	if len(req.Args) > 0 && strings.TrimSpace(req.Args[0]) != "" {
		return req.Args[0]
	}
	return defaultEnvFile
}

func envFileTable(action, path string, app core.App, keys []string, deploymentID int, done bool) output.Table {
	return output.Table{
		Single:  true,
		Columns: []string{"action", "file", "app", "variables", "written"},
		Total:   1,
		Rows: []map[string]any{{
			"action":       action,
			"file":         path,
			"app":          app.Name,
			"appId":        app.ID,
			"variables":    len(keys),
			"keys":         keys,
			"written":      done,
			"deploymentId": nilIfZero(deploymentID),
		}},
	}
}

// envPushTable reports the three groups separately, because "changed 3" and
// "added 3" are different facts and a caller deciding whether to deploy needs
// to know which happened.
func envPushTable(path string, app core.App, added, changed, unchanged []string,
	sent bool, deploymentID int) output.Table {

	return output.Table{
		Single:  true,
		Columns: []string{"action", "file", "app", "added", "changed", "unchanged", "sent"},
		Total:   1,
		Rows: []map[string]any{{
			"action":        "push",
			"file":          path,
			"app":           app.Name,
			"appId":         app.ID,
			"added":         len(added),
			"changed":       len(changed),
			"unchanged":     len(unchanged),
			"addedKeys":     added,
			"changedKeys":   changed,
			"unchangedKeys": unchanged,
			"sent":          sent,
			"deploymentId":  nilIfZero(deploymentID),
		}},
	}
}

// short keeps a quoted line from a file short enough to sit in a hint. The
// line may be a variable nobody wants on a screen, so it is only ever used for
// a line that failed to parse, which by definition is not a value yet.
func short(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= 60 {
		return s
	}
	return s[:60] + "…"
}
