package clierr

import "testing"

// The exit code table is a published contract, and three things read it: the
// help topic, the schema, and Kind.ExitCode. It was a contract with a copy of
// itself in two of those places, and the copies had already drifted: exit code
// 9 existed in the table and nowhere else, so a caller reading the schema
// could not learn about a failure the help printed. These tests are what makes
// the single source of truth stay single.

// everyKind is every Kind this package declares. Go cannot enumerate the
// constants of a named string type, so the list is written out, and the test
// below is what makes forgetting to add one to the table visible.
var everyKind = []Kind{
	KindUsage,
	KindAuth,
	KindConfirmation,
	KindNotFound,
	KindConflict,
	KindQuota,
	KindUpstream,
	KindUpgradeRequired,
	KindTimeout,
	KindInterrupted,
	KindInternal,
}

func TestEveryKindIsInTheTable(t *testing.T) {
	inTable := map[string]bool{}
	for _, e := range ExitCodes() {
		inTable[e.Kind] = true
	}

	for _, k := range everyKind {
		if !inTable[string(k)] {
			t.Errorf("kind %q has no entry in ExitCodes(), so it exits 1 and the schema never mentions it", k)
		}
	}
}

func TestEveryTableEntryIsDocumented(t *testing.T) {
	for _, e := range ExitCodes() {
		if e.What == "" {
			t.Errorf("exit code %d has no short description, so `help exit-codes` prints a blank line", e.Code)
		}
		// Code 0 is a success rather than a kind, and the schema skips it.
		if e.Kind != "" && e.Detail == "" {
			t.Errorf("exit code %d (%s) has no Detail, so the schema publishes an empty description", e.Code, e.Kind)
		}
	}
}

func TestCodesAreUniqueAndKindsAreUnique(t *testing.T) {
	codes, kinds := map[int]bool{}, map[string]bool{}
	for _, e := range ExitCodes() {
		if codes[e.Code] {
			t.Errorf("exit code %d appears twice; a number means one thing forever", e.Code)
		}
		codes[e.Code] = true

		if e.Kind == "" {
			continue
		}
		if kinds[e.Kind] {
			t.Errorf("kind %q appears twice, so its exit code depends on which row is read first", e.Kind)
		}
		kinds[e.Kind] = true
	}
}

func TestKindMethodsAgreeWithTheTable(t *testing.T) {
	for _, e := range ExitCodes() {
		if e.Kind == "" {
			continue
		}
		k := Kind(e.Kind)
		if got := k.ExitCode(); got != e.Code {
			t.Errorf("%s exits %d but the table says %d", k, got, e.Code)
		}
		if got := k.Retryable(); got != e.Retryable {
			t.Errorf("%s reports retryable=%v but the table says %v", k, got, e.Retryable)
		}
	}
}

// An unknown kind is the one case the table cannot answer, and the answer has
// to be the code for an unanticipated failure rather than a panic or a zero.
func TestUnknownKindExitsInternal(t *testing.T) {
	unknown := Kind("something-nobody-declared")
	if got := unknown.ExitCode(); got != 1 {
		t.Errorf("an undeclared kind exits %d, want 1", got)
	}
	if unknown.Retryable() {
		t.Error("an undeclared kind reports itself retryable, which loops a caller forever")
	}
}
