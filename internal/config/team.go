package config

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

// resolveTeamAndToken decides, in one step, which team this invocation targets
// and which credential authenticates it.
//
// These are one decision rather than two because an Out Plane token is scoped
// to exactly one team, permanently: the team is a claim inside the token and
// the server checks it against the team of whatever resource is touched.
// Resolving them separately is how a CLI ends up sending team A's header with
// team B's token and then reporting a 403 that explains nothing.
//
// Order of preference, and why each step is where it is:
//
//  1. OUTPLANE_TOKEN. A supplied credential settles both questions at once: its
//     own claim says which team it belongs to. This is the CI path, and it must
//     win outright so that a pipeline needs no other configuration.
//  2. --token. Same reasoning, for a one-off invocation.
//  3. --team. The user named a team; find the stored credential for it.
//  4. OUTPLANE_TEAM_ID. Same, from the environment.
//  5. The directory link. Working inside a project should target that project's
//     team without anyone having to say so.
//  6. The active team, set by `outplane team use`, or the only stored one.
//
// A failure here is returned rather than thrown, because commands that need no
// credential must still run.
func resolveTeamAndToken(ov Overrides, link *Link, profile Profile) (teamID, teamSlug, token Value, err error) {
	// 1 and 2: an explicit token answers everything.
	if raw := firstNonEmpty(ov.Token, os.Getenv("OUTPLANE_TOKEN")); raw != "" {
		source := SourceEnv
		if ov.Token != "" {
			source = SourceFlag
		}
		info, decodeErr := InspectToken(raw)
		if decodeErr != nil {
			return Value{}, Value{}, Value{}, decodeErr
		}
		// The slug is not in the token, only the id. If a credential for the
		// same team is already stored, borrow its slug so that output can name
		// the team rather than showing a bare GUID.
		slug := ""
		if c, ok := FindCredential(info.TeamID); ok {
			slug = c.TeamSlug
		}
		return Value{info.TeamID, SourceToken},
			Value{slug, SourceToken},
			Value{raw, source},
			nil
	}

	// 3 to 6: a named team, resolved against the stored credentials.
	requested, source := requestedTeam(ov, link, profile)
	if requested == "" {
		return Value{}, Value{}, Value{}, errNotSignedIn()
	}

	cred, ok := FindCredential(requested)
	if !ok {
		return Value{}, Value{}, Value{}, errNoCredentialFor(requested)
	}

	return Value{cred.TeamID, source},
		Value{cred.TeamSlug, source},
		Value{cred.Token, SourceFile},
		nil
}

// requestedTeam returns the team the invocation is asking for, and where that
// request came from, so that `outplane status` can explain itself.
func requestedTeam(ov Overrides, link *Link, profile Profile) (string, Source) {
	if ov.Team != "" {
		return ov.Team, SourceFlag
	}
	if v := os.Getenv("OUTPLANE_TEAM_ID"); v != "" {
		return v, SourceEnv
	}
	if link != nil {
		if link.TeamSlug != "" {
			return link.TeamSlug, SourceLink
		}
		if link.TeamID != "" {
			return link.TeamID, SourceLink
		}
	}
	if active := ActiveTeamSlug(); active != "" {
		return active, SourceFile
	}
	if profile.TeamSlug != "" {
		return profile.TeamSlug, SourceFile
	}
	return "", SourceUnset
}

// TeamNotSignedInError says a team was named that the CLI holds no credential
// for. It carries the list of teams that ARE available so the message can show
// them, which is the difference between an error a user can act on and one
// that sends them to the documentation.
type TeamNotSignedInError struct {
	Requested string
	Available []Credential
}

func (e *TeamNotSignedInError) Error() string {
	if e.Requested == "" {
		return "not signed in"
	}
	return fmt.Sprintf("not signed in to team %q", e.Requested)
}

// AvailableSlugs lists the teams a credential exists for.
func (e *TeamNotSignedInError) AvailableSlugs() []string {
	out := make([]string, 0, len(e.Available))
	for _, c := range e.Available {
		out = append(out, c.TeamSlug)
	}
	sort.Strings(out)
	return out
}

func errNotSignedIn() error {
	return &TeamNotSignedInError{Available: SignedInTeams()}
}

func errNoCredentialFor(team string) error {
	return &TeamNotSignedInError{Requested: team, Available: SignedInTeams()}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}
