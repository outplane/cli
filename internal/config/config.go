// Package config resolves everything the CLI needs to know before it can make
// a request: which API to talk to, which credential to use, and which team and
// application are in play.
//
// Three files, three jobs, and they are kept apart on purpose:
//
//	config.json       preferences. Never contains a secret.
//	credentials.json  the token. Mode 0600, in a 0700 directory.
//	.outplane/link.json   which app this directory belongs to. Per-project.
//
// Resolution order is fixed and identical for every setting: an explicit flag,
// then an environment variable, then the project link, then the config file,
// then a built-in default. `outplane config list` prints each value together
// with where it came from, so that "why is it using that team" is answerable
// in one command instead of by guesswork.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// DefaultAPIURL is where the CLI talks unless told otherwise.
const DefaultAPIURL = "https://api.outplane.com/api"

// Source says where a resolved value came from. It exists so that every
// setting can explain itself, which turns a class of confusing support
// questions into a single command.
type Source string

const (
	SourceFlag    Source = "flag"
	SourceEnv     Source = "environment"
	SourceLink    Source = "link file"
	SourceFile    Source = "config file"
	SourceToken   Source = "token claim"
	SourceDefault Source = "default"
	SourceUnset   Source = "unset"
)

// Value is a resolved setting together with its origin.
type Value struct {
	Value  string
	Source Source
}

func (v Value) String() string { return v.Value }
func (v Value) IsSet() bool    { return v.Value != "" }

// File is the on-disk preferences file. It never holds a secret; the token
// lives in a separate file with tighter permissions so that a stray `cat` of
// the config, or a screen share, cannot leak a credential.
//
// Profiles is a map even though the CLI currently exposes only one. Shaping it
// this way costs nothing today and is the difference between adding
// multi-account support later as a feature or as a migration.
type File struct {
	Version       int                `json:"version"`
	ActiveProfile string             `json:"activeProfile"`
	Profiles      map[string]Profile `json:"profiles"`
	Output        OutputPrefs        `json:"output,omitempty"`
	Update        UpdatePrefs        `json:"update,omitempty"`
}

// Profile is one account context.
type Profile struct {
	APIURL   string `json:"apiUrl,omitempty"`
	TeamID   string `json:"teamId,omitempty"`
	TeamSlug string `json:"teamSlug,omitempty"`
}

type OutputPrefs struct {
	Color  string `json:"color,omitempty"`  // auto | always | never
	Format string `json:"format,omitempty"` // auto | text | json
}

type UpdatePrefs struct {
	AutoCheck bool `json:"autoCheck"`
}

// Link ties a directory to an application. It is written by `outplane link`
// and read by walking up from the working directory, so that subdirectories of
// a repository inherit it.
//
// It is gitignored rather than committed: teamId is per-user in a
// multi-team organisation, and committing it means a colleague's CLI silently
// targets someone else's team.
type Link struct {
	APIURL   string `json:"apiUrl,omitempty"`
	TeamID   string `json:"teamId,omitempty"`
	TeamSlug string `json:"teamSlug,omitempty"`
	AppID    string `json:"appId,omitempty"`
	AppName  string `json:"appName,omitempty"`

	// path is where this was loaded from, for `outplane status`. Not persisted.
	path string
}

// Path reports the file this link was read from.
func (l Link) Path() string { return l.path }

// Resolved is the complete answer to "what should this invocation do".
type Resolved struct {
	APIURL   Value
	Token    Value
	TeamID   Value
	TeamSlug Value
	AppID    Value

	// TeamError explains why no team could be resolved, when none could. It is
	// carried rather than returned so that commands which need no credential
	// still run: `schema`, `version` and `help` must work on a machine that has
	// never been logged in.
	TeamError error

	// File and Link are the underlying sources, exposed so that `status` and
	// `config list` can report detail without re-reading anything.
	File File
	Link *Link
}

// Overrides are the values a flag supplied. Empty means "not given".
type Overrides struct {
	APIURL string
	Token  string
	Team   string
	App    string
}

// Resolve works out the effective settings.
//
// It never fails on a missing credential: whether one is required is a
// per-command decision, and a command that does not need auth should not be
// blocked by its absence.
func Resolve(ov Overrides) (Resolved, error) {
	var r Resolved

	file, err := Load()
	if err != nil {
		return r, err
	}
	r.File = file
	profile := file.Profiles[file.ActiveProfile]

	link, err := FindLink("")
	if err != nil {
		return r, err
	}
	r.Link = link

	r.APIURL = pick(
		Value{ov.APIURL, SourceFlag},
		Value{os.Getenv("OUTPLANE_API_URL"), SourceEnv},
		linkValue(link, func(l *Link) string { return l.APIURL }),
		Value{profile.APIURL, SourceFile},
		Value{DefaultAPIURL, SourceDefault},
	)

	// Team and credential are resolved together, because with team-scoped
	// tokens they are one decision rather than two. Asking "which team" and
	// then "which token" separately is how a CLI ends up sending team A's
	// header with team B's token and reporting a confusing 403.
	r.TeamID, r.TeamSlug, r.Token, r.TeamError = resolveTeamAndToken(ov, link, profile)

	// COLLISION 2: an explicit team that is not the linked directory's team.
	//
	// The link file records an app id AND the team it belongs to. If the caller
	// names a different team, that app id is meaningless: an app belongs to one
	// team, so carrying it across would send a stranger's id to the server and
	// produce a 404 that blames the wrong thing.
	//
	// The link's app is therefore dropped, not translated. An explicit --team
	// says "operate on that team", and the honest reading of that is "and not
	// on this directory's app".
	linkAppUsable := link != nil && teamsAgree(link, r.TeamID.Value, r.TeamSlug.Value)

	appCandidates := []Value{
		{ov.App, SourceFlag},
		{os.Getenv("OUTPLANE_APP_ID"), SourceEnv},
	}
	if linkAppUsable {
		appCandidates = append(appCandidates, linkValue(link, func(l *Link) string { return l.AppID }))
	}
	r.AppID = pick(appCandidates...)

	return r, nil
}

// pick returns the first candidate that has a value.
func pick(candidates ...Value) Value {
	for _, c := range candidates {
		if c.Value != "" {
			return c
		}
	}
	return Value{Source: SourceUnset}
}

func linkValue(l *Link, get func(*Link) string) Value {
	if l == nil {
		return Value{}
	}
	return Value{get(l), SourceLink}
}

// decodeJWTClaims reads a JWT payload WITHOUT verifying the signature.
//
// That is correct here and worth stating plainly, because unverified JWT
// parsing is usually a bug: we are not making a trust decision. The server
// verifies the token on every request. All we want is to read the team out of
// a credential the user gave us, so that we can address the right team. A
// forged token buys nothing, because the server would reject it.
func decodeJWTClaims(token string) (map[string]any, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errors.New("not a JWT")
	}
	payload, err := base64URLDecode(parts[1])
	if err != nil {
		return nil, err
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, err
	}
	return claims, nil
}

// ── file locations ──────────────────────────────────────────────────────

// Dir is where preferences live. XDG on Unix from the very first release:
// adding it later would be a migration, and honouring it costs one function.
func Dir() (string, error) {
	if custom := os.Getenv("OUTPLANE_HOME"); custom != "" {
		return custom, nil
	}
	if runtime.GOOS == "windows" {
		if local := os.Getenv("LOCALAPPDATA"); local != "" {
			return filepath.Join(local, "outplane"), nil
		}
	}
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "outplane"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("could not determine the home directory: %w", err)
	}
	// ~/.config rather than ~/Library/Application Support on macOS, so that a
	// dotfiles repository works unchanged across machines.
	return filepath.Join(home, ".config", "outplane"), nil
}

func configPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

// Load reads the preferences file, returning defaults when it does not exist.
// A first run is not an error.
func Load() (File, error) {
	f := File{
		Version:       1,
		ActiveProfile: "default",
		Profiles:      map[string]Profile{"default": {}},
		Update:        UpdatePrefs{AutoCheck: true},
	}

	path, err := configPath()
	if err != nil {
		return f, err
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return f, nil
	}
	if err != nil {
		return f, fmt.Errorf("could not read %s: %w", path, err)
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		return f, fmt.Errorf("%s is not valid JSON: %w", path, err)
	}
	if f.ActiveProfile == "" {
		f.ActiveProfile = "default"
	}
	if f.Profiles == nil {
		f.Profiles = map[string]Profile{"default": {}}
	}
	return f, nil
}

// Save writes the preferences file atomically.
func Save(f File) error {
	path, err := configPath()
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

// FindLink walks up from dir looking for .outplane/link.json.
//
// Walking upward is what lets a monorepo subdirectory inherit its repository's
// link, so that `cd services/api && outplane deploy create` works.
func FindLink(dir string) (*Link, error) {
	if dir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return nil, nil
		}
		dir = wd
	}
	for {
		candidate := filepath.Join(dir, ".outplane", "link.json")
		raw, err := os.ReadFile(candidate)
		if err == nil {
			var l Link
			if err := json.Unmarshal(raw, &l); err != nil {
				return nil, fmt.Errorf("%s is not valid JSON: %w", candidate, err)
			}
			l.path = candidate
			return &l, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return nil, nil // reached the filesystem root
		}
		dir = parent
	}
}

// SaveLink writes .outplane/link.json in dir.
func SaveLink(dir string, l Link) (string, error) {
	linkDir := filepath.Join(dir, ".outplane")
	if err := os.MkdirAll(linkDir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(linkDir, "link.json")
	raw, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return "", err
	}
	if err := writeAtomic(path, append(raw, '\n'), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// writeAtomic writes via a temporary file and a rename, so that an interrupted
// write cannot leave a truncated file behind.
//
// The mode is set when the temporary file is CREATED, not afterwards. Setting
// it after writing leaves a window in which the file exists with default
// permissions, and for a credential file that window is the whole problem.
func writeAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename succeeds

	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// teamsAgree reports whether a link file refers to the team that was resolved
// for this invocation.
//
// A link written before the team was ever recorded (no teamId, no teamSlug) is
// treated as agreeing: it predates this check, and refusing to use it would
// break directories linked by an earlier version for no safety gain.
func teamsAgree(link *Link, teamID, teamSlug string) bool {
	if link.TeamID == "" && link.TeamSlug == "" {
		return true
	}
	if link.TeamID != "" && link.TeamID == teamID {
		return true
	}
	if link.TeamSlug != "" && teamSlug != "" && link.TeamSlug == teamSlug {
		return true
	}
	return false
}
