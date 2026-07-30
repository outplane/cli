package commands

import (
	"context"
	"strings"
	"time"

	"github.com/outplane/cli/internal/clierr"
	"github.com/outplane/cli/internal/config"
	"github.com/outplane/cli/internal/output"
)

func init() {
	register("team list", teamList)
	register("team use", teamUse)
}

// teamList reports the teams this machine holds a credential for.
//
// Not the teams the user belongs to: the API cannot answer that under an API
// token, and would answer "none" rather than failing. See the note at the top
// of registry/team.go. The distinction is carried through every string this
// command produces, because a list that quietly means something narrower than
// its name is the kind of thing a caller only discovers by being wrong.
func teamList(_ context.Context, req Request) (output.Table, error) {
	stored := config.SignedInTeams()

	// Which team this invocation would actually act as, which is not always the
	// one `team use` selected: a token in the environment outranks it, and so
	// does a linked directory.
	activeID := req.CLI.Config.TeamID.Value

	table := output.Table{
		Columns: []string{"slug", "active", "expiresAt", "tokenName"},
		Total:   len(stored),
	}
	for _, c := range stored {
		table.Rows = append(table.Rows, map[string]any{
			"slug":      c.TeamSlug,
			"teamId":    c.TeamID,
			"active":    activeID != "" && c.TeamID == activeID,
			"tokenName": nilIfEmpty(c.Name),
			"expiresAt": nilIfEmpty(c.ExpiresAt),
			"daysLeft":  daysLeftValue(c.ExpiresAt, c.DaysLeft()),
			"expired":   c.Expired(),
		})
	}

	// An environment token can name a team no credential is stored for, in
	// which case every row above says active:false and the reader is entitled
	// to wonder which team they are on. Say it rather than leaving the gap.
	if activeID != "" && !storedIncludes(stored, activeID) {
		req.CLI.Out.Note("Acting as team %s, from %s. It is not in this list.",
			activeID, req.CLI.Config.Token.Source)
	}

	if len(stored) == 0 {
		req.CLI.Out.Note("Not signed in to any team. Run `outplane login` to add one.")
	}
	return table, nil
}

func storedIncludes(stored []config.Credential, teamID string) bool {
	for _, c := range stored {
		if c.TeamID == teamID {
			return true
		}
	}
	return false
}

// teamUse records which stored credential later commands should use.
//
// It writes a preference and says plainly whether that preference is in effect,
// because it very often is not: a token in the environment, an explicit --team
// and a linked directory all outrank it. A command that silently saved a
// setting three other things override would produce the worst kind of bug
// report, the one where the user is certain they switched teams and the tool is
// certain they did not.
func teamUse(_ context.Context, req Request) (output.Table, error) {
	requested := req.Args[0]

	cred, ok := config.FindCredential(requested)
	if !ok {
		return output.Table{}, notSignedInTo(requested)
	}

	// Written by slug even when the caller passed an id, because the slug is
	// the key the store is organised by and the name everything else displays.
	changed := config.ActiveTeamSlug() != cred.TeamSlug
	if err := config.SetActiveTeam(cred.TeamSlug); err != nil {
		return output.Table{}, clierr.New(clierr.KindInternal, "could not save the active team: %v", err)
	}

	overriddenBy := whatOutranksActiveTeam(req)
	effective := overriddenBy == ""

	if effective {
		req.CLI.Out.Note("Now acting as %s.", cred.TeamSlug)
	} else {
		// Saved, and saying so, but not in effect. Both halves matter: the
		// first stops the user running it again, the second stops them
		// believing the next command will target the team they just chose.
		req.CLI.Out.Note("Saved %s as the default team, but %s takes precedence right now.",
			cred.TeamSlug, overriddenBy)
		req.CLI.Out.Note("Run `outplane status` to see what is in effect.")
	}

	if cred.Expired() {
		req.CLI.Out.Note("This team's token expired on %s. Run `outplane login` to replace it.",
			shortDate(cred.ExpiresAt))
	}

	return output.Table{
		Single:  true,
		Columns: []string{"slug", "teamId", "changed", "effective"},
		Total:   1,
		Rows: []map[string]any{{
			"slug":      cred.TeamSlug,
			"teamId":    cred.TeamID,
			"changed":   changed,
			"effective": effective,
		}},
	}, nil
}

// whatOutranksActiveTeam names the thing beating the saved preference, or an
// empty string when nothing does.
//
// The order matches config.resolveTeamAndToken, and has to keep matching it: a
// message that names the wrong culprit sends somebody looking in the wrong
// file. --team is not considered, because it applies to one invocation and this
// one is not it.
func whatOutranksActiveTeam(req Request) string {
	switch req.CLI.Config.Token.Source {
	case config.SourceEnv:
		return "OUTPLANE_TOKEN"
	case config.SourceFlag:
		return "--token"
	}
	if v := req.CLI.Config.TeamID.Source; v == config.SourceEnv {
		return "OUTPLANE_TEAM_ID"
	}
	if req.CLI.Config.Link != nil {
		return "this directory's link"
	}
	return ""
}

// notSignedInTo builds the failure for a team with no stored credential.
//
// It lists the teams that ARE available, because the question behind the error
// is always "then which ones do I have", and answering it here saves a round
// trip through another command.
func notSignedInTo(requested string) error {
	stored := config.SignedInTeams()

	err := clierr.New(clierr.KindAuth, "not signed in to team %q", requested).
		WithCode("auth.team_not_signed_in").
		WithStep("sign in and choose it in the console", "outplane", "login")

	if len(stored) == 0 {
		return err.WithHint("No teams are signed in on this machine yet.")
	}

	slugs := make([]string, 0, len(stored))
	for _, c := range stored {
		slugs = append(slugs, c.TeamSlug)
	}
	return err.
		WithHint("Signed in to: %s.", strings.Join(slugs, ", ")).
		WithDetail("signedInTeams", slugs).
		WithStep("see them in full", "outplane", "team", "list")
}

// shortDate renders a stored RFC 3339 expiry as a plain date, which is the only
// part anybody reads in a sentence about a token that has already expired.
func shortDate(rfc3339 string) string {
	t, err := time.Parse(time.RFC3339, rfc3339)
	if err != nil {
		return rfc3339
	}
	return t.Format("2 January 2006")
}
