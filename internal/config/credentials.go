package config

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// credentials.json holds tokens, and only tokens.
//
// Keeping them out of config.json means a user can share their configuration,
// paste it into an issue, or sync it in a dotfiles repository without leaking a
// credential. It also lets the two files carry different permissions.
//
// ── Why this file is keyed by team ──────────────────────────────────────
//
// An Out Plane API token is scoped to exactly one team, permanently: the team
// is a claim inside the token, and the server compares that claim against the
// team of whatever resource is being touched. A token for team A therefore
// cannot address team B, ever.
//
// The consequence runs all the way up to the user interface. `--team beta`
// does not select a header; it selects a stored credential. A user who works
// across three teams holds three tokens, and switching teams means switching
// which one is active. Modelling that here, rather than pretending one
// credential covers everything, is what keeps the rest of the CLI honest.
//
// A future step adds the OS keychain as the preferred store with this file as
// the fallback. The fallback is not a nicety: containers and headless Linux
// cannot reach a keychain, and CI is exactly that case.

type credentialFile struct {
	Version int `json:"version"`

	// ActiveTeam is the team slug used when nothing more specific is given.
	ActiveTeam string `json:"activeTeam,omitempty"`

	// Teams is keyed by team slug, which is what a person types and what
	// appears in application URLs. The team id is stored alongside because the
	// API wants the id, and resolving a slug to an id would otherwise need a
	// network call before the CLI knows which credential to authenticate with.
	Teams map[string]Credential `json:"teams"`
}

// Credential is one team's stored token plus what can be known about it
// without asking the server.
type Credential struct {
	Token    string `json:"token"`
	TeamID   string `json:"teamId"`
	TeamSlug string `json:"teamSlug"`

	// Name is the label shown in the console's token list, so that a user
	// looking at three tokens can tell which machine each belongs to.
	Name string `json:"name,omitempty"`

	// ExpiresAt is RFC 3339, or empty for a token that never expires. Stored
	// so `status` can warn before a CI pipeline stops working one morning.
	ExpiresAt string `json:"expiresAt,omitempty"`

	// AddedAt records when this CLI stored the credential.
	AddedAt string `json:"addedAt,omitempty"`
}

// Expired reports whether the stored expiry has passed.
//
// This is a local hint, not authorisation: the server decides. It exists so
// the CLI can say "the token for acme expired last Tuesday" instead of
// relaying an opaque 403.
func (c Credential) Expired() bool {
	if c.ExpiresAt == "" {
		return false
	}
	t, err := time.Parse(time.RFC3339, c.ExpiresAt)
	if err != nil {
		return false
	}
	return time.Now().After(t)
}

// DaysLeft returns whole days until expiry, or -1 when it never expires.
func (c Credential) DaysLeft() int {
	if c.ExpiresAt == "" {
		return -1
	}
	t, err := time.Parse(time.RFC3339, c.ExpiresAt)
	if err != nil {
		return -1
	}
	return int(time.Until(t).Hours() / 24)
}

// ExpiryOf reports when a token expires, reading the token itself.
//
// The token is the authority on its own lifetime. A stored credential keeps a
// copy of the expiry so that listing teams does not have to decode anything,
// but a token supplied through OUTPLANE_TOKEN has no stored copy at all, and
// answering "never expires" for one that expires next week is precisely the
// wrong answer to give somebody deciding whether to rotate it.
//
// daysLeft is -1 when the token carries no expiry, matching DaysLeft. A token
// that cannot be decoded reports the same, because a caller that reached this
// point holds a token every other command is about to fail on regardless.
func ExpiryOf(token string) (expiresAt string, daysLeft int) {
	info, err := InspectToken(token)
	if err != nil || info.ExpiresAt.IsZero() {
		return "", -1
	}
	return info.ExpiresAt.Format(time.RFC3339), int(time.Until(info.ExpiresAt).Hours() / 24)
}

func credentialsPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "credentials.json"), nil
}

// loadCredentials reads the store, returning an empty one when absent.
//
// A missing or unreadable file is not an error: plenty of commands need no
// credential, and `outplane schema` in particular must work on a machine that
// has never been logged in.
func loadCredentials() credentialFile {
	f := credentialFile{Version: 1, Teams: map[string]Credential{}}

	path, err := credentialsPath()
	if err != nil {
		return f
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return f
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		return credentialFile{Version: 1, Teams: map[string]Credential{}}
	}
	if f.Teams == nil {
		f.Teams = map[string]Credential{}
	}
	return f
}

func saveCredentials(f credentialFile) error {
	path, err := credentialsPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(path, append(raw, '\n'), 0o600)
}

// SignedInTeams lists every team the CLI holds a credential for, sorted by
// slug so that output is stable between runs.
func SignedInTeams() []Credential {
	f := loadCredentials()
	out := make([]Credential, 0, len(f.Teams))
	for _, c := range f.Teams {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TeamSlug < out[j].TeamSlug })
	return out
}

// ActiveTeamSlug returns the team used when nothing more specific is given.
//
// When exactly one credential is stored it is the active one whether or not
// anything was ever set: a single-team user should never have to run
// `team use`.
func ActiveTeamSlug() string {
	f := loadCredentials()
	if f.ActiveTeam != "" {
		if _, ok := f.Teams[f.ActiveTeam]; ok {
			return f.ActiveTeam
		}
	}
	if len(f.Teams) == 1 {
		for slug := range f.Teams {
			return slug
		}
	}
	return ""
}

// FindCredential returns the credential for a team, looked up by slug or id.
//
// Both are accepted because a person types the slug and a script is more
// likely to hold the id, and forcing either to convert would mean a network
// call before the CLI even knows which token to authenticate with.
func FindCredential(teamRef string) (Credential, bool) {
	f := loadCredentials()
	if c, ok := f.Teams[teamRef]; ok {
		return c, true
	}
	for _, c := range f.Teams {
		if c.TeamID == teamRef {
			return c, true
		}
	}
	return Credential{}, false
}

// StoreCredential saves a team's token and makes it active.
//
// Signing in to a second team ADDS a credential rather than replacing the
// first. A user who runs `outplane login --team beta` has not stopped working
// on acme, and silently discarding that token would be a surprise they only
// discover on their next deploy.
func StoreCredential(c Credential) error {
	if c.TeamSlug == "" {
		return errors.New("a credential needs a team slug")
	}
	if c.AddedAt == "" {
		c.AddedAt = time.Now().UTC().Format(time.RFC3339)
	}
	f := loadCredentials()
	f.Teams[c.TeamSlug] = c
	f.ActiveTeam = c.TeamSlug
	return saveCredentials(f)
}

// SetActiveTeam changes which stored credential is used by default.
func SetActiveTeam(slug string) error {
	f := loadCredentials()
	if _, ok := f.Teams[slug]; !ok {
		return fmt.Errorf("not signed in to team %q", slug)
	}
	f.ActiveTeam = slug
	return saveCredentials(f)
}

// ForgetCredential removes one team's token. An empty slug removes all of
// them, which is what `outplane logout --all` does.
func ForgetCredential(slug string) error {
	f := loadCredentials()
	if slug == "" {
		f.Teams = map[string]Credential{}
		f.ActiveTeam = ""
	} else {
		delete(f.Teams, slug)
		if f.ActiveTeam == slug {
			f.ActiveTeam = ""
			// Promote the only remaining credential, so that removing one of
			// two teams does not leave the CLI with nothing selected.
			if len(f.Teams) == 1 {
				for remaining := range f.Teams {
					f.ActiveTeam = remaining
				}
			}
		}
	}
	return saveCredentials(f)
}

// TokenInfo is what can be learned from a token without asking the server.
type TokenInfo struct {
	TeamID string

	// TeamSlug is the team's name as people type it. It is a claim rather than
	// something the CLI asks the server for, which is what makes signing in
	// work with no network at all.
	//
	// Embedding it is safe because a slug is written once, when a team is
	// created, and nothing ever changes it: renaming a team edits its Name and
	// leaves the Slug alone. The team's Name is deliberately NOT a claim, for
	// the mirror-image reason: it is editable, and a token outlives a rename by
	// up to 180 days.
	TeamSlug string

	TokenID    string
	ExpiresAt  time.Time
	IsAPIToken bool
}

// InspectToken decodes a token's claims locally.
//
// This is how the CLI knows which team a freshly pasted token belongs to,
// before it has made a single request. It is what makes a first run work with
// nothing but a paste.
//
// The signature is NOT verified, and does not need to be. That is usually a
// bug, so it is worth being explicit: nothing here is a trust decision. The
// server verifies the token on every request. All we are doing is reading
// which team a credential the user just handed us belongs to, so that we
// address the right one. A forged token buys nothing, because the server
// would reject it.
func InspectToken(token string) (TokenInfo, error) {
	var info TokenInfo

	claims, err := decodeJWTClaims(token)
	if err != nil {
		return info, fmt.Errorf("this does not look like an Out Plane token: %w", err)
	}
	if v, ok := claims["team_id"].(string); ok {
		info.TeamID = v
	}
	if v, ok := claims["team_slug"].(string); ok {
		info.TeamSlug = v
	}
	if v, ok := claims["jti"].(string); ok {
		info.TokenID = v
	}
	if v, ok := claims["exp"].(float64); ok {
		info.ExpiresAt = time.Unix(int64(v), 0).UTC()
	}
	// An API token's subject is the literal string "api-token:{guid}", which
	// is how the CLI knows not to call user-identity endpoints that would
	// return nonsense under one.
	if sub, ok := claims["nameid"].(string); ok {
		info.IsAPIToken = strings.HasPrefix(sub, "api-token:")
	}
	if info.TeamID == "" {
		return info, errors.New("this token carries no team, so it cannot be used")
	}
	return info, nil
}

// base64URLDecode handles JWT payloads, which use base64url without padding.
func base64URLDecode(s string) ([]byte, error) {
	if pad := len(s) % 4; pad != 0 {
		s += "===="[pad:]
	}
	return base64.URLEncoding.DecodeString(s)
}
