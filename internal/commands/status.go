package commands

import (
	"context"
	"errors"
	"runtime"
	"time"

	"github.com/outplane/cli/internal/api"
	"github.com/outplane/cli/internal/clierr"
	"github.com/outplane/cli/internal/config"
	"github.com/outplane/cli/internal/core"
	"github.com/outplane/cli/internal/output"
)

func init() {
	register("status", status)
}

// statusTimeout is short on purpose. status is what somebody runs when things
// are already wrong, and waiting thirty seconds to be told the network is down
// is worse than being told in five.
const statusTimeout = 8 * time.Second

// status reports the resolved context and where each part of it came from.
//
// It never returns an error for the conditions it exists to describe. Not being
// signed in, holding an expired token and being unable to reach the API are all
// findings, reported in fields, with exit 0. A diagnostic that refuses to run
// when something is wrong is a diagnostic that is useless exactly when it is
// needed; `whoami` is the command that asserts and exits 3.
func status(ctx context.Context, req Request) (output.Table, error) {
	cfg := req.CLI.Config

	row := map[string]any{
		"signedIn":        false,
		"teamSlug":        nil,
		"teamId":          nil,
		"teamSource":      nil,
		"tokenSource":     nil,
		"expiresAt":       nil,
		"daysLeft":        nil,
		"expired":         false,
		"apiUrl":          cfg.APIURL.Value,
		"apiUrlSource":    string(cfg.APIURL.Source),
		"credentialValid": nil,
		"problem":         nil,
		"linkedDirectory": nil,
		"configDirectory": configDir(),
		"version":         req.CLI.Version(),
	}

	if cfg.Link != nil {
		row["linkedDirectory"] = cfg.Link.Path()
	}

	// No credential. Report why and stop: everything below needs one.
	if cfg.TeamError != nil {
		row["problem"] = cfg.TeamError.Error()
		return statusTable(row), nil
	}

	expiresAt, daysLeft := config.ExpiryOf(cfg.Token.Value)

	row["signedIn"] = true
	row["teamSlug"] = nilIfEmpty(cfg.TeamSlug.Value)
	row["teamId"] = nilIfEmpty(cfg.TeamID.Value)
	row["teamSource"] = string(cfg.TeamID.Source)
	row["tokenSource"] = string(cfg.Token.Source)
	row["expiresAt"] = nilIfEmpty(expiresAt)
	row["daysLeft"] = daysLeftValue(expiresAt, daysLeft)
	row["expired"] = expiresAt != "" && daysLeft < 0

	if req.Flags.Bool("offline") {
		// credentialValid stays null. Not false: nothing was checked, and
		// reporting an unchecked token as invalid would be a guess.
		return statusTable(row), nil
	}

	slug, err := verifyCredential(ctx, req)
	switch {
	case err == nil:
		row["credentialValid"] = true
		// A token from the environment carries a team id but no slug, and this
		// is the one command that goes and finds out. whoami cannot, because it
		// promises to make no request.
		if cfg.TeamSlug.Value == "" && slug != "" {
			row["teamSlug"] = slug
		}

	case isAuthFailure(err):
		row["credentialValid"] = false
		row["problem"] = "the token was rejected. It may have been revoked or expired"

	default:
		// Unreachable, not rejected. credentialValid stays null, because a
		// network failure is not evidence about a token, and treating the two
		// alike is how a pipeline discards a working credential during an
		// outage.
		row["problem"] = "could not reach the API, so the token was not checked: " + err.Error()
	}

	return statusTable(row), nil
}

// verifyCredential makes the one request status is allowed, with its own short
// deadline rather than the client default.
func verifyCredential(ctx context.Context, req Request) (string, error) {
	cfg := req.CLI.Config

	client := api.New(api.Config{
		BaseURL: cfg.APIURL.Value,
		Token:   cfg.Token.Value,
		TeamID:  cfg.TeamID.Value,
		Version: req.CLI.Version(),
		OSArch:  runtime.GOOS + "/" + runtime.GOARCH,
		Timeout: statusTimeout,
	})
	return core.VerifyToken(ctx, client)
}

// isAuthFailure distinguishes a rejected token from an unreachable server.
//
// The difference decides whether credentialValid is false or null, which is the
// difference between "replace this token" and "try again later".
func isAuthFailure(err error) bool {
	var e *clierr.Error
	if !errors.As(err, &e) {
		return false
	}
	return e.Kind == clierr.KindAuth
}

func statusTable(row map[string]any) output.Table {
	return output.Table{
		Single: true,
		Columns: []string{
			"signedIn", "teamSlug", "teamId", "teamSource", "tokenSource",
			"expiresAt", "daysLeft", "expired",
			"apiUrl", "apiUrlSource", "credentialValid", "problem",
			"linkedDirectory", "configDirectory", "version",
		},
		Total: 1,
		Rows:  []map[string]any{row},
	}
}

// configDir reports where credentials and preferences live, so that somebody
// debugging can go and look. A failure to resolve it is reported in band rather
// than raised: it is one line of a report, not a reason to abandon the report.
func configDir() string {
	dir, err := config.Dir()
	if err != nil {
		return "unavailable: " + err.Error()
	}
	return dir
}
