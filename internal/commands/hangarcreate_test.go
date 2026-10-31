package commands

import (
	"testing"

	"github.com/outplane/cli/internal/clierr"
	"github.com/outplane/cli/internal/core"
)

// Every idle timeout a Hangar can have is offered as it is written in the
// choices, and each one has to read back as the minutes the server stores.
func TestParseIdleTimeoutReadsEveryChoice(t *testing.T) {
	choices := core.IdleTimeoutChoices()
	if len(choices) != len(core.HangarIdleTimeouts)+1 {
		t.Fatalf("%d choices for %d timeouts and never", len(choices), len(core.HangarIdleTimeouts))
	}

	for i, minutes := range core.HangarIdleTimeouts {
		got, err := parseIdleTimeout(choices[i])
		if err != nil {
			t.Errorf("%s refused: %v", choices[i], err)
			continue
		}
		if got == nil || *got != minutes {
			t.Errorf("%s read as %v, want %d minutes", choices[i], got, minutes)
		}
		if err := core.CheckHangarIdleTimeout(got); err != nil {
			t.Errorf("%s refused by the check: %v", choices[i], err)
		}
	}

	never, err := parseIdleTimeout("never")
	if err != nil || never != nil {
		t.Errorf("never read as %v, %v; want nil", never, err)
	}
}

func TestParseIdleTimeoutSpellings(t *testing.T) {
	cases := []struct {
		raw     string
		minutes int
	}{
		{"60m", 60},
		{"1h", 60},
		{"1440m", 1440},
		{"24h", 1440},
		{" 6h ", 360},
	}
	for _, tc := range cases {
		got, err := parseIdleTimeout(tc.raw)
		if err != nil || got == nil || *got != tc.minutes {
			t.Errorf("%q read as %v, %v; want %d minutes", tc.raw, got, err, tc.minutes)
		}
	}

	for _, raw := range []string{"", "30", "0m", "-1h", "90s", "1d", "soon"} {
		_, err := parseIdleTimeout(raw)
		if e := clierr.AsError(err); e == nil || e.Code != "hangar.idle_timeout_invalid" {
			t.Errorf("%q: got %v, want hangar.idle_timeout_invalid", raw, err)
		}
	}

	// A whole number of minutes the server does not offer passes the parse and
	// is refused by the check, before anything is sent.
	odd, err := parseIdleTimeout("45m")
	if err != nil {
		t.Fatalf("45m refused by the parse: %v", err)
	}
	if e := clierr.AsError(core.CheckHangarIdleTimeout(odd)); e == nil || e.Code != "hangar.idle_timeout_invalid" {
		t.Errorf("45m: got %v, want hangar.idle_timeout_invalid", e)
	}
}
