package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
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
