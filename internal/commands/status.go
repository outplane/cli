package commands

import (
	"context"
	"errors"

	"github.com/outplane/cli/internal/config"
	"github.com/outplane/cli/internal/output"
)

func init() {
	register("status", status)
}

// status reports the resolved context and where each part of it came from.
//
// Entirely local, and fast enough to run without thinking about it. Everything
// it reports is either a claim inside the token or a decision the CLI made
// itself, so there is nothing to ask the server.
//
// It used to make one request to confirm the credential still worked. That was
// dropped: the answer arrives anyway on the first real command, and a
// diagnostic that hangs for eight seconds when the network is the problem is a
// diagnostic people stop running.
//
// It never returns an error for the conditions it exists to describe. Not
// signed in, an expired token and two inputs that contradict each other are all
// findings, reported in fields, with exit 0. A diagnostic that refuses to run
// when something is wrong is useless exactly when it is wanted; `whoami` is the
// command that asserts, and it exits 3.
func status(_ context.Context, req Request) (output.Table, error) {
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
		"problem":         nil,
		"linkedDirectory": nil,
		"configDirectory": configDir(),
		"version":         req.CLI.Version(),
	}

	if cfg.Link != nil {
		row["linkedDirectory"] = cfg.Link.Path()
	}
	// A link that would not parse still has a path, and naming it is the whole
	// value of this command in that situation: the file is the problem, and the
	// reader needs to know which one.
	var badLink *config.LinkUnreadableError
	if errors.As(cfg.LinkError, &badLink) {
		row["linkedDirectory"] = badLink.Path
	}

	// No credential resolved. The reason is the whole answer, so report it and
	// stop: every field below needs a credential to describe.
	if cfg.TeamError != nil {
		row["problem"] = cfg.TeamError.Error()
		return statusTable(row), nil
	}

	expiresAt, daysLeft := config.ExpiryOf(cfg.Token.Value)
	expired := expiresAt != "" && daysLeft < 0

	row["signedIn"] = true
	row["teamSlug"] = nilIfEmpty(cfg.TeamSlug.Value)
	row["teamId"] = nilIfEmpty(cfg.TeamID.Value)
	row["teamSource"] = string(cfg.TeamID.Source)
	row["tokenSource"] = string(cfg.Token.Source)
	row["expiresAt"] = nilIfEmpty(expiresAt)
	row["daysLeft"] = daysLeftValue(expiresAt, daysLeft)
	row["expired"] = expired

	// Expiry is the one failure that can be seen without asking, so it is the
	// one this command can name. A revoked token still reads as fine here and
	// will announce itself on the next request.
	if expired {
		row["problem"] = "this token expired on " + shortDate(expiresAt)
	}

	return statusTable(row), nil
}

func statusTable(row map[string]any) output.Table {
	return output.Table{
		Single: true,
		Columns: []string{
			"signedIn", "teamSlug", "teamId", "teamSource", "tokenSource",
			"expiresAt", "daysLeft", "expired", "problem",
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
