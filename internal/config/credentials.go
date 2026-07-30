package config

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// credentials.json holds tokens, and only tokens.
//
// Keeping them out of config.json means a user can share their configuration,
// paste it into an issue, or sync it in a dotfiles repository without leaking a
// credential. It also means the two files can have different permissions.
//
// A future step adds the OS keychain as the preferred store, with this file as
// the fallback. The fallback is not a nicety: containers and headless Linux
// have no keychain to reach, and CI is exactly that case.

type credentialFile struct {
	Version int                   `json:"version"`
	Tokens  map[string]credential `json:"tokens"`
}

type credential struct {
	Token string `json:"token"`

	// TeamSlug and Name are cached from the moment the token was stored, so
	// that `whoami` can say something useful before any request is made.
	TeamSlug string `json:"teamSlug,omitempty"`
	Name     string `json:"name,omitempty"`
}

func credentialsPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "credentials.json"), nil
}

// credentialValue loads the stored token for a profile.
//
// A missing or unreadable file is not an error here: plenty of commands need
// no credential, and `outplane schema` in particular must work on a machine
// that has never been logged in.
func credentialValue(profile string) Value {
	path, err := credentialsPath()
	if err != nil {
		return Value{}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return Value{}
	}
	var f credentialFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return Value{}
	}
	if c, ok := f.Tokens[profile]; ok && c.Token != "" {
		return Value{c.Token, SourceFile}
	}
	return Value{}
}

// StoreToken saves a token for a profile.
func StoreToken(profile, token, teamSlug, name string) error {
	path, err := credentialsPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}

	f := credentialFile{Version: 1, Tokens: map[string]credential{}}
	if raw, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(raw, &f)
		if f.Tokens == nil {
			f.Tokens = map[string]credential{}
		}
	}
	f.Tokens[profile] = credential{Token: token, TeamSlug: teamSlug, Name: name}

	raw, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(path, append(raw, '\n'), 0o600)
}

// ForgetToken removes a profile's token.
func ForgetToken(profile string) error {
	path, err := credentialsPath()
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil // already gone
	}
	if err != nil {
		return err
	}
	var f credentialFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return err
	}
	delete(f.Tokens, profile)

	out, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(path, append(out, '\n'), 0o600)
}

// TokenInfo is what can be learned about a token without asking the server.
type TokenInfo struct {
	TeamID     string
	TokenID    string
	ExpiresAt  int64 // Unix seconds, 0 when absent
	IsAPIToken bool
}

// InspectToken decodes a token's claims locally.
//
// This is how `whoami` answers without a round trip, and it is also why the
// CLI can address the right team on a first run with nothing but a pasted
// token. The signature is not verified, and does not need to be: the server
// verifies it on every request, and nothing here is a trust decision.
func InspectToken(token string) (TokenInfo, error) {
	var info TokenInfo
	claims, err := decodeJWTClaims(token)
	if err != nil {
		return info, fmt.Errorf("this does not look like an Out Plane token: %w", err)
	}
	if v, ok := claims["team_id"].(string); ok {
		info.TeamID = v
	}
	if v, ok := claims["jti"].(string); ok {
		info.TokenID = v
	}
	if v, ok := claims["exp"].(float64); ok {
		info.ExpiresAt = int64(v)
	}
	// An API token's subject is the literal string "api-token:{guid}", which
	// is how the CLI knows not to call user-identity endpoints that would
	// return nonsense under one.
	if sub, ok := claims["nameid"].(string); ok {
		info.IsAPIToken = len(sub) > 10 && sub[:10] == "api-token:"
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
