package commands

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/outplane/cli/internal/clierr"
	"github.com/outplane/cli/internal/config"
	"github.com/outplane/cli/internal/core"
	"github.com/outplane/cli/internal/output"
)

func init() {
	register("link", link)
	register("unlink", unlink)
}

// link writes .outplane/link.json for the current directory.
//
// The team is whatever this invocation already resolved to, which means
// `--team acme outplane link` records acme without link needing its own flag.
// The application, if named, is resolved through the same exact-match path
// every other command uses.
func link(ctx context.Context, req Request) (output.Table, error) {
	cli := req.CLI

	if cli.Config.TeamError != nil {
		return output.Table{}, cli.SignInError()
	}

	dir, err := os.Getwd()
	if err != nil {
		return output.Table{}, clierr.New(clierr.KindInternal, "could not read the working directory: %v", err)
	}

	next := config.Link{
		TeamID:   cli.Config.TeamID.Value,
		TeamSlug: cli.Config.TeamSlug.Value,
	}

	if len(req.Args) > 0 {
		app, err := resolveApp(ctx, req, req.Args[0])
		if err != nil {
			return output.Table{}, err
		}
		next.AppID = app.ID
		next.AppName = app.Name
	}

	// Compare against a link already in this exact directory, not against the
	// one in effect: a link inherited from a parent is a different file, and
	// reporting "no change" because a parent already says this would be wrong
	// about what just happened.
	existing := linkInDir(dir)
	changed := existing == nil || *existing != next

	path, err := config.SaveLink(dir, next)
	if err != nil {
		return output.Table{}, clierr.New(clierr.KindInternal, "could not write the link: %v", err)
	}

	describeLink(req, next, dir)

	return output.Table{
		Single:  true,
		Columns: []string{"path", "teamSlug", "teamId", "appName", "appId", "changed"},
		Total:   1,
		Rows: []map[string]any{{
			"path":     path,
			"teamSlug": nilIfEmpty(next.TeamSlug),
			"teamId":   next.TeamID,
			"appName":  nilIfEmpty(next.AppName),
			"appId":    nilIfEmpty(next.AppID),
			"changed":  changed,
		}},
	}, nil
}

// describeLink says what was linked, and warns about the one case that
// surprises people: a link further up the tree that this one now shadows.
func describeLink(req Request, l config.Link, dir string) {
	target := l.TeamSlug
	if l.AppName != "" {
		target = l.AppName + " in " + l.TeamSlug
	}
	req.CLI.Out.Note("This directory is linked to %s.", target)

	// FindLink walks up, so it will find the file just written. Look above this
	// directory instead.
	parent := filepath.Dir(dir)
	if parent == dir {
		return
	}
	if above, err := config.FindLink(parent); err == nil && above != nil {
		req.CLI.Out.Note("It shadows the link at %s, which still applies elsewhere.", above.Path())
	}
}

// linkInDir reads a link from one directory without walking up to its parents.
func linkInDir(dir string) *config.Link {
	l, err := config.FindLink(dir)
	if err != nil || l == nil {
		return nil
	}
	if filepath.Dir(filepath.Dir(l.Path())) != dir {
		return nil // inherited from a parent, not this directory's own
	}
	// Path is unexported and not part of the value comparison, so a copy of the
	// fields is what gets compared by the caller.
	return &config.Link{
		APIURL:   l.APIURL,
		TeamID:   l.TeamID,
		TeamSlug: l.TeamSlug,
		AppID:    l.AppID,
		AppName:  l.AppName,
	}
}

// resolveApp turns a reference into an application, translating the domain
// errors into messages that say what to do next.
//
// The translation lives here rather than in core because core must not know
// about commands, argv or exit codes. Core reports what happened; this decides
// how to say it.
func resolveApp(ctx context.Context, req Request, ref string) (core.App, error) {
	client, err := req.CLI.APIClient()
	if err != nil {
		return core.App{}, err
	}

	app, err := core.FindApp(ctx, client, ref)
	if err == nil {
		return app, nil
	}

	var notFound *core.AppNotFoundError
	if errors.As(err, &notFound) {
		e := clierr.New(clierr.KindNotFound, "%v", notFound).
			WithCode("app.not_found").
			WithStep("see what this team has", "outplane", "app", "list")
		if len(notFound.Available) > 0 {
			e = e.WithHint("This team has: %s.", strings.Join(notFound.Available, ", ")).
				WithDetail("availableApps", notFound.Available)
		} else {
			e = e.WithHint("This team has no applications yet.")
		}
		return core.App{}, e
	}

	var ambiguous *core.AmbiguousAppError
	if errors.As(err, &ambiguous) {
		return core.App{}, clierr.New(clierr.KindUsage, "%v", ambiguous).
			WithCode("app.ambiguous").
			WithHint("Display names are editable and need not be unique. Use one of these "+
				"names instead: %s.", strings.Join(ambiguous.Names(), ", ")).
			WithDetail("matchingApps", ambiguous.Names())
	}

	return core.App{}, err
}

// targetApp resolves the application a command acts on: the argument if one was
// given, otherwise the app this directory is linked to.
//
// The command's own words are passed in so the "nothing to act on" error can
// offer the reader their own command back with a name in it. Every command
// whose app argument is optional shares this, which is what keeps the
// resolution order from drifting between them.
func targetApp(ctx context.Context, req Request, command ...string) (core.App, error) {
	return targetAppRef(ctx, req, argAt(req.Args, 0), command...)
}

// argAt returns a positional argument, or nil when it was not given.
//
// The pointer carries the one distinction a plain string cannot: an argument
// that is absent is a request for the default, and an argument that is present
// but empty is an unset shell variable. Collapsing the two is how
// `outplane app get "$APP"` came to answer a question nobody asked.
func argAt(args []string, i int) *string {
	if i >= len(args) {
		return nil
	}
	return &args[i]
}

// targetAppRef is targetApp for a command whose application is not the first
// argument. `deploy get 42 checkout` is the case: the id comes first, so the
// reference has to be handed in rather than read from a fixed position.
func targetAppRef(ctx context.Context, req Request, ref *string, command ...string) (core.App, error) {
	if ref != nil {
		// See emptyAppArgument: an empty argument is an unset variable, not a
		// request for the default.
		if strings.TrimSpace(*ref) == "" {
			return core.App{}, emptyAppArgument()
		}
		return resolveApp(ctx, req, *ref)
	}

	if id := req.CLI.Config.AppID.Value; id != "" {
		return resolveApp(ctx, req, id)
	}

	named := append(append([]string{"outplane"}, command...), "<APP_NAME>")
	return core.App{}, clierr.New(clierr.KindUsage, "no application given, and this directory is not linked to one").
		WithCode("context.no_app").
		WithHint("Name the application, or link the directory once and omit it afterwards.").
		WithStep("name an application", named...).
		WithStep("or link this directory", "outplane", "link", "<APP_NAME>").
		WithStep("see what this team has", "outplane", "app", "list")
}

// unlink deletes the link that is in effect.
//
// It reports the path it deleted because the file may live in a parent
// directory and may have been serving sibling directories too. Removing
// something on somebody's behalf without saying which file is how a person ends
// up unable to work out why their other project stopped resolving.
func unlink(_ context.Context, req Request) (output.Table, error) {
	l, err := config.FindLink("")

	// A file that will not parse is the main reason somebody reaches for this
	// command, so removing it is the job rather than an obstacle to it. The
	// error carries the path, which is all that deleting needs; refusing here
	// and saying "delete it by hand" would leave the CLI unable to repair the
	// one thing it broke.
	var unreadable *config.LinkUnreadableError
	if errors.As(err, &unreadable) {
		if err := config.RemoveLink(unreadable.Path); err != nil {
			return output.Table{}, clierr.New(clierr.KindInternal,
				"could not remove %s: %v", unreadable.Path, err)
		}
		req.CLI.Out.Note("Removed %s, which could not be parsed.", unreadable.Path)
		return unlinkResult(unreadable.Path, true), nil
	}
	if err != nil {
		return output.Table{}, clierr.New(clierr.KindInternal, "could not read the link: %v", err)
	}

	if l == nil {
		// Nothing to do is not a failure: a teardown script should be able to
		// run this whether or not anything was ever linked.
		req.CLI.Out.Note("No link found; nothing to remove.")
		return unlinkResult("", false), nil
	}

	path := l.Path()
	if err := config.RemoveLink(path); err != nil {
		return output.Table{}, clierr.New(clierr.KindInternal, "could not remove %s: %v", path, err)
	}

	req.CLI.Out.Note("Removed %s.", path)
	return unlinkResult(path, true), nil
}

func unlinkResult(path string, removed bool) output.Table {
	return output.Table{
		Single:  true,
		Columns: []string{"removed", "path", "changed"},
		Total:   1,
		Rows: []map[string]any{{
			"removed": removed,
			"path":    nilIfEmpty(path),
			"changed": removed,
		}},
	}
}
