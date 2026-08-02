package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"github.com/zalando/go-keyring"
)

// Where tokens live.
//
// Three places, in this order, and the order is the whole design:
//
//	OUTPLANE_TOKEN   an environment variable, read by Resolve. It never touches
//	                 disk and it wins over everything, which is what makes CI
//	                 work: a runner sets it, uses it, and leaves nothing behind.
//	the OS keychain  macOS Keychain, Windows Credential Manager, or the Linux
//	                 Secret Service. This is where a person's tokens belong. It
//	                 is encrypted at rest, unlocked with the account, and not
//	                 something a misdirected `cat` or a dotfiles repository can
//	                 leak.
//	a file           0600, next to the configuration. Only when there is no
//	                 keychain to reach, which is a real case rather than a
//	                 theoretical one: a container, a headless server, an SSH
//	                 session with no D-Bus.
//
// The store keeps one document rather than one entry per team, because the
// document also records which team is active, and two secrets that have to
// agree are worse than one that cannot disagree with itself.
//
// A file written by an older release is imported into the keychain on first
// use and then deleted. Leaving it would mean a plaintext copy of every token
// sitting next to an encrypted one, which is the worst of both.

const (
	// keyringService is the name a person sees in Keychain Access or
	// seahorse, so it is the product's name rather than a package path.
	keyringService = "outplane"

	// keyringUser names the one entry. It is not a username: this store holds
	// every team's credential in one document.
	keyringUser = "credentials"
)

// store is where the credential document is read from and written to.
type store interface {
	load() ([]byte, error)
	save([]byte) error
	clear() error

	// where names the store for a person, because "signed in" is a different
	// fact depending on which one answered.
	where() string
}

// storeOnce resolves the store once per process. Reaching a keychain costs a
// round trip to another service, and several commands ask for credentials more
// than once.
var (
	storeOnce sync.Once
	resolved  store
)

// credentialStore returns the store to use, preferring the keychain.
//
// The probe is a read rather than a capability check, because a keychain can be
// present and locked, present and refused by policy, or absent entirely, and
// only trying tells them apart. A read that fails for any reason means the
// keychain cannot serve this process, and the file takes over.
func credentialStore() store {
	storeOnce.Do(func() {
		k := keyringStore{}
		if _, err := k.load(); err == nil || errors.Is(err, keyring.ErrNotFound) {
			resolved = k
			return
		}
		resolved = fileStore{}
	})
	return resolved
}

// keyringStore keeps the document in the operating system's own secret store.
type keyringStore struct{}

func (keyringStore) load() ([]byte, error) {
	secret, err := keyring.Get(keyringService, keyringUser)
	if err != nil {
		return nil, err
	}
	return []byte(secret), nil
}

func (keyringStore) save(document []byte) error {
	return keyring.Set(keyringService, keyringUser, string(document))
}

func (keyringStore) clear() error {
	err := keyring.Delete(keyringService, keyringUser)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return err
}

func (keyringStore) where() string { return "keychain" }

// fileStore keeps the document in a file readable only by its owner.
//
// It exists for the machines a keychain cannot reach, and those machines are
// not unusual: every container is one.
type fileStore struct{}

func (fileStore) load() ([]byte, error) {
	path, err := credentialsPath()
	if err != nil {
		return nil, err
	}
	return os.ReadFile(path)
}

func (fileStore) save(document []byte) error {
	path, err := credentialsPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return writeAtomic(path, append(document, '\n'), 0o600)
}

func (fileStore) clear() error {
	path, err := credentialsPath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (fileStore) where() string { return "file" }

// CredentialStore names where this machine's tokens are kept, for `status` to
// report. A reader deciding whether a token is safe on this machine needs to
// know which of the two answered.
func CredentialStore() string { return credentialStore().where() }

// migrateFileToKeychain moves an older release's plaintext file into the
// keychain and removes it.
//
// Called on every load, and free after the first: with no file there is nothing
// to do. It runs before the keychain is read, so that the first command after
// an upgrade sees the same credentials it did before.
//
// A failure is silent on purpose. The file is still readable, and refusing to
// run because a copy could not be moved would turn a working installation into
// a broken one over a housekeeping detail.
func migrateFileToKeychain(target store) {
	if target.where() != "keychain" {
		return
	}

	raw, err := fileStore{}.load()
	if err != nil || len(raw) == 0 {
		return
	}

	// Only when the keychain has nothing. A keychain that already holds
	// credentials is the newer truth, and a stale file must not overwrite it.
	if existing, err := target.load(); err == nil && len(existing) > 0 {
		_ = fileStore{}.clear()
		return
	}

	var f credentialFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return
	}
	if err := target.save(raw); err != nil {
		return
	}
	_ = fileStore{}.clear()
}
