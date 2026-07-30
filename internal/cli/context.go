// Package cli assembles everything a command needs to run.
//
// It sits between the cobra front end and the domain layer, and it exists so
// that no command has to know how a token is found, how a team is resolved, or
// which renderer the user's terminal deserves. A command receives a ready
// Context and returns data.
package cli

import (
	"io"
	"runtime"

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
	if !c.Config.Token.IsSet() {
		return nil, clierr.New(clierr.KindAuth, "not signed in").
			WithCode("auth.not_authenticated").
			WithHint("Sign in once and the token is stored for future commands.").
			WithStep("sign in interactively", "outplane", "login").
			WithStep("or supply a token without signing in", "OUTPLANE_TOKEN=<TOKEN>", "outplane", "app", "list")
	}

	if !c.Config.TeamID.IsSet() {
		// Reaching here means the credential carried no team claim and nothing
		// else supplied one, which is unusual enough to be worth its own
		// message rather than letting the server return a confusing 400.
		return nil, clierr.New(clierr.KindUsage, "no team was resolved for this command").
			WithCode("context.no_team").
			WithHint("Most commands operate on one team. The CLI could not work out which.").
			WithStep("link this directory to a team and app", "outplane", "link").
			WithStep("or name a team for this invocation", "outplane", "app", "list", "--team", "<TEAM_SLUG>")
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
