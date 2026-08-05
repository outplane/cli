package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/zalando/go-keyring"
)

// The store decides where a token lives, and getting it wrong either loses
// somebody's credentials or leaves a plaintext copy of them next to an
// encrypted one. The keychain cannot be exercised here, because a test machine
// may have none and a headless runner certainly does not, so what is held to
// account is everything around it: the file store, and the migration that runs
// once per upgrade.

// fake is a keychain that works, so that migration can be tested on a machine
// where the real one cannot be reached.
type fake struct {
	document []byte
	failSave bool
}

func (f *fake) load() ([]byte, error) {
	if len(f.document) == 0 {
		return nil, errors.New("not found")
	}
	return f.document, nil
}

func (f *fake) save(d []byte) error {
	if f.failSave {
		return errors.New("locked")
	}
	f.document = d
	return nil
}

func (f *fake) clear() error  { f.document = nil; return nil }
func (f *fake) where() string { return "keychain" }

// inTempHome points the configuration directory at a directory of this test's
// own, so that nothing here can touch the credentials of whoever is running it.
func inTempHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("OUTPLANE_HOME", dir)
	if resolved, err := Dir(); err != nil || resolved != dir {
		t.Skipf("the configuration directory is not redirectable here (got %q, %v)", resolved, err)
	}
	return dir
}

func TestFileStoreRoundTrip(t *testing.T) {
	inTempHome(t)
	f := fileStore{}

	if _, err := f.load(); err == nil {
		t.Fatal("an absent file loaded without an error")
	}

	if err := f.save([]byte(`{"version":1}`)); err != nil {
		t.Fatalf("save: %v", err)
	}

	raw, err := f.load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if string(raw) != "{\"version\":1}\n" {
		t.Errorf("got %q", raw)
	}

	path, _ := credentialsPath()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	// The whole point of the fallback is that it is not readable by anybody
	// else on the machine.
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("permissions are %04o, want 0600", perm)
	}

	if err := f.clear(); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("the file survived being cleared")
	}
	// Clearing what is already gone is how signing out twice ends up here.
	if err := f.clear(); err != nil {
		t.Errorf("clearing nothing failed: %v", err)
	}
}

func TestMigration(t *testing.T) {
	document := []byte(`{"version":1,"activeTeam":"acme","teams":{}}`)

	t.Run("moves the file into the keychain and removes it", func(t *testing.T) {
		inTempHome(t)
		if err := (fileStore{}).save(document); err != nil {
			t.Fatal(err)
		}

		target := &fake{}
		migrateFileToKeychain(target)

		if len(target.document) == 0 {
			t.Fatal("nothing reached the keychain")
		}
		path, _ := credentialsPath()
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Error("the plaintext file was left behind")
		}
	})

	t.Run("a keychain that already holds credentials wins", func(t *testing.T) {
		inTempHome(t)
		if err := (fileStore{}).save([]byte(`{"version":1,"activeTeam":"stale","teams":{}}`)); err != nil {
			t.Fatal(err)
		}

		target := &fake{document: document}
		migrateFileToKeychain(target)

		if string(target.document) != string(document) {
			t.Error("a stale file overwrote the keychain")
		}
		path, _ := credentialsPath()
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Error("the stale file was left behind")
		}
	})

	t.Run("a keychain that cannot be written leaves the file alone", func(t *testing.T) {
		dir := inTempHome(t)
		if err := (fileStore{}).save(document); err != nil {
			t.Fatal(err)
		}

		migrateFileToKeychain(&fake{failSave: true})

		if _, err := os.Stat(filepath.Join(dir, "credentials.json")); err != nil {
			t.Error("the file was removed even though the keychain refused it")
		}
	})

	t.Run("nothing to move is not an error", func(t *testing.T) {
		inTempHome(t)
		target := &fake{}
		migrateFileToKeychain(target)
		if len(target.document) != 0 {
			t.Error("something appeared from nowhere")
		}
	})

	t.Run("a file store target is left entirely alone", func(t *testing.T) {
		inTempHome(t)
		if err := (fileStore{}).save(document); err != nil {
			t.Fatal(err)
		}

		migrateFileToKeychain(fileStore{})

		raw, err := (fileStore{}).load()
		if err != nil || len(raw) == 0 {
			t.Error("the file was removed with nowhere to move it to")
		}
	})
}

// ── keychain chunking ───────────────────────────────────────────────────────
//
// The document is split across keychain entries because every keychain has a
// ceiling: Windows refuses a value over 2560 bytes, and macOS refuses the whole
// shell command it builds over 4096. A document holding a few teams' tokens
// clears both, right up until one more sign-in does not, which is how this was
// found in the first place. These hold the split to the two things that matter:
// what goes in comes back out, and no single entry is ever near a ceiling.

// The lower of the two real ceilings. Anything written must clear it on every
// platform, not just the one the tests happen to run on.
const smallestKeychainCeiling = 2560

// keychainSetup points the library at its in-memory provider and clears it.
func keychainSetup(t *testing.T) {
	t.Helper()
	keyring.MockInit()
	t.Cleanup(func() { _ = keyringStore{}.clear() })
	_ = keyringStore{}.clear()
}

// document builds a payload of a known size, filled so a misjoined document is
// visibly wrong rather than plausibly wrong.
func document(size int) []byte {
	out := make([]byte, size)
	for i := range out {
		out[i] = byte('a' + i%26)
	}
	return out
}

// entrySizes reports the length of every entry the store wrote.
func entrySizes(t *testing.T) []int {
	t.Helper()
	var sizes []int
	for n := 0; ; n++ {
		value, err := keyring.Get(keyringService, chunkAccount(n))
		if err != nil {
			return sizes
		}
		sizes = append(sizes, len(value))
	}
}

func TestKeychainRoundTripsEverySize(t *testing.T) {
	// Around the boundary in both directions, plus a document far past it.
	for _, size := range []int{
		0, 1, 100,
		keyringChunk - 1, keyringChunk, keyringChunk + 1,
		2 * keyringChunk, 2*keyringChunk + 1,
		10 * keyringChunk,
	} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			keychainSetup(t)
			want := document(size)

			if err := (keyringStore{}).save(want); err != nil {
				t.Fatalf("save: %v", err)
			}
			got, err := keyringStore{}.load()
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("round trip lost data: got %d bytes, want %d", len(got), len(want))
			}

			// The invariant that broke. Every entry has to clear the lowest
			// ceiling of any platform, not the one running the test.
			for i, size := range entrySizes(t) {
				if size > smallestKeychainCeiling {
					t.Errorf("entry %d is %d bytes, over the %d ceiling", i, size, smallestKeychainCeiling)
				}
			}
		})
	}
}

// A real document is a few teams' tokens, and it was exactly this size that
// stopped being writable.
func TestKeychainHoldsAPlausibleNumberOfTeams(t *testing.T) {
	keychainSetup(t)

	// Ten teams, each with a token far longer than one really is.
	want := document(10 * 1200)
	if err := (keyringStore{}).save(want); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := keyringStore{}.load()
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("load = (%d bytes, %v), want %d bytes", len(got), err, len(want))
	}
	for i, size := range entrySizes(t) {
		if size > smallestKeychainCeiling {
			t.Errorf("entry %d is %d bytes, over the %d ceiling", i, size, smallestKeychainCeiling)
		}
	}
}

// A document written by a release that knew nothing about chunking is one
// entry, and has to load without a migration.
func TestKeychainReadsALegacySingleEntry(t *testing.T) {
	keychainSetup(t)

	legacy := document(3679) // the size that first failed to save
	if err := keyring.Set(keyringService, keyringUser, string(legacy)); err != nil {
		t.Fatalf("seeding the legacy entry: %v", err)
	}

	got, err := keyringStore{}.load()
	if err != nil || !bytes.Equal(got, legacy) {
		t.Fatalf("load = (%d bytes, %v), want %d bytes", len(got), err, len(legacy))
	}

	// Saving over it splits it, and the oversized entry is gone.
	if err := (keyringStore{}).save(legacy); err != nil {
		t.Fatalf("save: %v", err)
	}
	for i, size := range entrySizes(t) {
		if size > smallestKeychainCeiling {
			t.Errorf("entry %d is still %d bytes", i, size)
		}
	}
}

// Signing out of a team shortens the document. A shorter document must not
// leave the tail of a longer one behind, or the next load reads both.
func TestKeychainDoesNotLeaveAStaleTail(t *testing.T) {
	keychainSetup(t)

	long := document(5 * keyringChunk)
	if err := (keyringStore{}).save(long); err != nil {
		t.Fatalf("save long: %v", err)
	}
	if n := len(entrySizes(t)); n != 5 {
		t.Fatalf("wrote %d entries for a document of 5 chunks", n)
	}

	short := document(keyringChunk / 2)
	if err := (keyringStore{}).save(short); err != nil {
		t.Fatalf("save short: %v", err)
	}
	if n := len(entrySizes(t)); n != 1 {
		t.Errorf("%d entries remain after shortening to one chunk", n)
	}

	got, err := keyringStore{}.load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !bytes.Equal(got, short) {
		t.Fatalf("read back %d bytes, want %d: the tail of the longer document survived", len(got), len(short))
	}
}

func TestKeychainClearRemovesEveryEntry(t *testing.T) {
	keychainSetup(t)

	if err := (keyringStore{}).save(document(4 * keyringChunk)); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := (keyringStore{}).clear(); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if n := len(entrySizes(t)); n != 0 {
		t.Errorf("%d entries survived clear", n)
	}
	if _, err := (keyringStore{}).load(); !errors.Is(err, keyring.ErrNotFound) {
		t.Errorf("load after clear = %v, want ErrNotFound", err)
	}
	// Clearing nothing is not an error, so a teardown can run unconditionally.
	if err := (keyringStore{}).clear(); err != nil {
		t.Errorf("clear on an empty keychain: %v", err)
	}
}

// "Stored, and empty" and "never stored" are different answers, and the
// difference decides whether the CLI thinks somebody is signed in.
func TestKeychainTellsEmptyFromAbsent(t *testing.T) {
	keychainSetup(t)

	if _, err := (keyringStore{}).load(); !errors.Is(err, keyring.ErrNotFound) {
		t.Fatalf("load on an empty keychain = %v, want ErrNotFound", err)
	}
	if err := (keyringStore{}).save(nil); err != nil {
		t.Fatalf("save empty: %v", err)
	}
	got, err := keyringStore{}.load()
	if err != nil {
		t.Fatalf("load after saving empty: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("read back %d bytes after saving an empty document", len(got))
	}
}
