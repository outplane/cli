// Package cli assembles everything a command needs to run.
//
// It sits between the cobra front end and the domain layer, and it exists so
// that no command has to know how a token is found, how a team is resolved, or
// which renderer the user's terminal deserves. A command receives a ready
// Context and returns data.
package cli

import (
	"errors"
	"io"
	"runtime"
	"strings"

	"github.com/outplane/cli/internal/api"
	"github.com/outplane/cli/internal/clierr"
	"github.com/outplane/cli/internal/config"
	"github.com/outplane/cli/internal/execctx"
	"github.com/outplane/cli/internal/output"
)

// Context is the resolved environment for one invocation.
type Context struct {
	Exec   execctx.Context
	Config config.Resolved
	Out    *output.Writer

	// Fields is --fields, already split. Empty means every field.
	Fields []string

	// DryRun is --dry-run.
	DryRun bool

	version string
}

// Build resolves configuration and prepares the output writer.
//
// It does not require a credential: whether one is needed is a per-command
// decision, and `schema`, `version` and `help` must work on a machine that has
// never logged in.
func Build(exec execctx.Context, ov config.Overrides, version string, out, errw io.Writer) (*Context, error) {
	resolved, err := config.Resolve(ov)
	if err != nil {
		return nil, clierr.New(clierr.KindInternal, "%v", err)
	}
	return &Context{
		Exec:    exec,
		Config:  resolved,
		Out:     output.New(out, errw, exec),
		version: version,
	}, nil
}

// APIClient returns a client for the Out Plane API, or an actionable error
// explaining how to authenticate.
//
// The error is written carefully: an agent that receives it should be able to
// act without asking a human, and a human should not have to search the docs.
func (c *Context) APIClient() (*api.Client, error) {
	if c.Config.TeamError != nil {
		return nil, signInError(c.Config.TeamError)
	}

	return api.New(api.Config{
		BaseURL: c.Config.APIURL.Value,
		Token:   c.Config.Token.Value,
		TeamID:  c.Config.TeamID.Value,
		Version: c.version,
		OSArch:  runtime.GOOS + "/" + runtime.GOARCH,
	}), nil
}

// Version reports the CLI version, for the User-Agent and `outplane version`.
func (c *Context) Version() string { return c.version }

// SignInError is the credential-resolution failure for this invocation, phrased
// for a human and for an agent.
//
// Exposed so that commands which read the credential without making a request,
// `whoami` and `status`, produce the same message as one that does. Two
// different explanations of the same condition would be worse than either.
func (c *Context) SignInError() error {
	if c.Config.TeamError == nil {
		return nil
	}
	return signInError(c.Config.TeamError)
}

// signInError turns a credential-resolution failure into a message that says
// what to do next.
//
// Team-scoped tokens make this worth care: "not signed in" is ambiguous when a
// user is signed in to two teams and asked for a third. The message names the
// teams that ARE available, so the fix is visible without running another
// command.
func signInError(err error) error {
	// An explicit token and an explicit --team that name different teams. This
	// is a usage error, not an auth failure: both inputs are valid, they just
	// contradict each other, and only the caller can say which they meant.
	var conflict *config.TeamTokenConflictError
	if errors.As(err, &conflict) {
		return clierr.New(clierr.KindUsage, "%v", conflict).
			WithCode("context.team_token_conflict").
			WithHint("A token belongs to exactly one team, so --team cannot redirect it.").
			WithStep("use the token's own team", "outplane", "app", "list").
			WithStep("or use the stored credential for that team instead",
				"unset", "OUTPLANE_TOKEN")
	}

	// A link file that exists but will not parse. Not an authentication
	// problem at all: the credential may be perfectly good, but the file that
	// would have chosen a team is unreadable, and guessing which team it meant
	// is how a command acts on the wrong one.
	var badLink *config.LinkUnreadableError
	if errors.As(err, &badLink) {
		return clierr.New(clierr.KindUsage, "%v", badLink).
			WithCode("link.unreadable").
			WithHint("A link file names the team for this directory. While it cannot be "+
				"read, no command can know which team you meant.").
			WithStep("remove it and start again", "outplane", "unlink").
			WithDetail("linkPath", badLink.Path)
	}

	var notSignedIn *config.TeamNotSignedInError
	if !errors.As(err, &notSignedIn) {
		return clierr.New(clierr.KindAuth, "%v", err).WithCode("auth.not_authenticated")
	}

	available := notSignedIn.AvailableSlugs()

	// Nothing stored at all: a first run.
	if len(available) == 0 {
		return clierr.New(clierr.KindAuth, "not signed in").
			WithCode("auth.not_authenticated").
			WithHint("Signing in stores a token for one team. Teams are separate credentials.").
			WithStep("sign in", "outplane", "login").
			WithStep("or supply a token directly", "OUTPLANE_TOKEN=<TOKEN>", "outplane", "app", "list")
	}

	// Signed in, but not to the team that was asked for. Naming the teams that
	// are available is the whole value of this branch.
	//
	// The suggested fix is a bare `outplane login`, deliberately without a team
	// flag. The team is chosen in the console, which is the only place the
	// user's list of teams exists; a flag would require them to already know a
	// slug they are trying to discover.
	return clierr.New(clierr.KindAuth, "not signed in to team %q", notSignedIn.Requested).
		WithCode("auth.team_not_signed_in").
		// Passed as an argument rather than concatenated into the format
		// string: a team slug is user-supplied, and a slug containing a percent
		// verb would otherwise corrupt the message.
		WithHint("Signed in to: %s. Signing in again lets you pick another team.",
			strings.Join(available, ", ")).
		WithDetail("signedInTeams", available).
		WithStep("sign in and choose this team in the console", "outplane", "login").
		WithStep("see every team you are signed into", "outplane", "team", "list")
}
