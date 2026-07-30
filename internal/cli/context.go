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

// signInError turns a credential-resolution failure into a message that says
// what to do next.
//
// Team-scoped tokens make this worth care: "not signed in" is ambiguous when a
// user is signed in to two teams and asked for a third. The message names the
// teams that ARE available, so the fix is visible without running another
// command.
func signInError(err error) error {
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
	e := clierr.New(clierr.KindAuth, "not signed in to team %q", notSignedIn.Requested).
		WithCode("auth.team_not_signed_in").
		// Passed as an argument rather than concatenated into the format
		// string: a team slug is user-supplied, and a slug containing a percent
		// verb would otherwise corrupt the message.
		WithHint("Signed in to: %s", strings.Join(available, ", ")).
		WithDetail("signedInTeams", available)

	if notSignedIn.Requested != "" {
		e = e.WithStep("sign in to this team", "outplane", "login", "--team", notSignedIn.Requested)
	}
	return e.WithStep("see every team you are signed into", "outplane", "team", "list")
}
