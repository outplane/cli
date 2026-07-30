package core

import (
	"context"

	"github.com/outplane/cli/internal/api"
)

// VerifyToken proves a token works and returns the slug of the team it belongs
// to.
//
// The endpoint choice needs explaining, because the obvious one is wrong.
// GET /User/GetUserTeams looks right and is not: under an API token the server
// resolves the caller's identity to the literal string "api-token:{guid}", so
// it returns an empty list rather than failing, which would have the CLI report
// success while having learned nothing.
//
// LogQueryVerify exists to tell the console which log tenant it may query, and
// a tenant is a team, so it returns exactly the slug we need. It authorises on
// bare team membership, which an API token satisfies. Calling it is therefore
// both the cheapest liveness check available and the only way to learn a team's
// slug from a credential that carries only its id.
func VerifyToken(ctx context.Context, c *api.Client) (teamSlug string, err error) {
	var slug string
	if err := c.Get(ctx, "/LogMonitor/LogQueryVerify", &slug); err != nil {
		return "", err
	}
	return slug, nil
}
