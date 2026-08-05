package core

import (
	"fmt"
	"testing"
)

// A state the server adds later has to reach an older CLI as something visibly
// unrecognised, carrying the number, rather than as a blank or a guess. This is
// the same contract `app list` keeps for an application's status.
func TestInstanceStateNamesWhatItKnowsAndAdmitsWhatItDoesNot(t *testing.T) {
	known := map[int]string{
		0: "unknown", 10: "pending", 20: "starting", 30: "running",
		40: "failing", 50: "terminating", 60: "terminated",
	}
	for code, want := range known {
		if got := instanceState(code); got != want {
			t.Errorf("instanceState(%d) = %q, want %q", code, got, want)
		}
	}
	for _, code := range []int{5, 35, 70, 999, -1} {
		want := fmt.Sprintf("unknown:%d", code)
		if got := instanceState(code); got != want {
			t.Errorf("instanceState(%d) = %q, want %q", code, got, want)
		}
	}
}
