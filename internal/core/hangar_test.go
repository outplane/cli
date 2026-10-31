package core

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/outplane/cli/internal/clierr"
	"github.com/outplane/cli/internal/registry"
)

// The create request is checked here rather than against a live team. The
// server refuses a body whose collections arrive as null, and reads a null idle
// timeout as never, so the difference between nil and empty is the whole
// contract and is invisible until a request fails.

func TestNewHangarBodySendsEveryCollection(t *testing.T) {
	body := NewHangar{Name: "workbench"}.body()

	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}

	want := []string{"name", "agents", "environmentVariables", "envVariableGroupIds", "idleTimeoutMinutes"}
	if len(got) != len(want) {
		t.Errorf("body has %d keys, want exactly %v: %s", len(got), want, raw)
	}
	for _, key := range want {
		if _, ok := got[key]; !ok {
			t.Errorf("body has no %s: %s", key, raw)
		}
	}

	for _, key := range []string{"agents", "environmentVariables", "envVariableGroupIds"} {
		if got[key] == nil {
			t.Errorf("%s is null, and the server refuses a missing collection: %s", key, raw)
		}
	}
	if got["idleTimeoutMinutes"] != nil {
		t.Errorf("idleTimeoutMinutes is %v, want null for never", got["idleTimeoutMinutes"])
	}
}

func TestNewHangarBodyCarriesWhatWasAskedFor(t *testing.T) {
	thirty := 30
	body := NewHangar{
		Name:               "workbench",
		Agents:             []string{"codex"},
		Env:                map[string]string{"LOG_LEVEL": "debug"},
		EnvGroupIDs:        []string{"group-b", "group-a"},
		IdleTimeoutMinutes: &thirty,
	}.body()

	raw, _ := json.Marshal(body)
	const want = `{"agents":["codex"],"envVariableGroupIds":["group-b","group-a"],` +
		`"environmentVariables":{"LOG_LEVEL":"debug"},"idleTimeoutMinutes":30,"name":"workbench"}`
	if string(raw) != want {
		t.Errorf("got  %s\nwant %s", raw, want)
	}
}

func TestNewHangarCheck(t *testing.T) {
	thirty, sixty := 30, 60

	cases := []struct {
		name string
		spec NewHangar
		code string
	}{
		{"a valid one", NewHangar{Name: "workbench", Agents: []string{"claude-code", "codex"}, IdleTimeoutMinutes: &thirty}, ""},
		{"never times out", NewHangar{Name: "workbench"}, ""},
		{"terminal only", NewHangar{Name: "workbench", Agents: []string{}}, ""},
		{"too short", NewHangar{Name: "box"}, "hangar.name_invalid"},
		{"too long", NewHangar{Name: strings.Repeat("a", 46)}, "hangar.name_invalid"},
		{"not alphanumeric", NewHangar{Name: "work-bench"}, "hangar.name_invalid"},
		{"reserved", NewHangar{Name: "Admin"}, "hangar.name_reserved"},
		{"unknown agent", NewHangar{Name: "workbench", Agents: []string{"notepad"}}, "hangar.agent_invalid"},
		{"agent twice", NewHangar{Name: "workbench", Agents: []string{"codex", "codex"}}, "hangar.agent_duplicate"},
		{"timeout not offered", NewHangar{Name: "workbench", IdleTimeoutMinutes: &sixty}, "hangar.idle_timeout_invalid"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.spec.Check()
			if tc.code == "" {
				if err != nil {
					t.Fatalf("refused: %v", err)
				}
				return
			}
			e := clierr.AsError(err)
			if e == nil {
				t.Fatalf("accepted, want %s", tc.code)
			}
			if e.Code != tc.code {
				t.Errorf("code %q, want %q (%v)", e.Code, tc.code, e)
			}
			if e.Kind != clierr.KindUsage {
				t.Errorf("kind %q, want usage", e.Kind)
			}
		})
	}
}

// A paused Hangar reports paused whatever its deployment ended as, as an
// application does, and a terminal-only one reports [] rather than null.
func TestDecodeHangar(t *testing.T) {
	var d hangarDTO
	raw := `{"id":"00000000-0000-0000-0000-000000000001","name":"workbench","displayName":"WorkBench",` +
		`"status":50,"isPaused":true,"agents":null,"instanceType":"op-34","diskSizeInGb":10,` +
		`"idleTimeoutMinutes":null,"createdDate":"2026-01-02T03:04:05"}`
	if err := json.Unmarshal([]byte(raw), &d); err != nil {
		t.Fatal(err)
	}

	h := decodeHangar(d)
	if h.Status != "paused" || !h.Paused {
		t.Errorf("status %q paused %v, want paused", h.Status, h.Paused)
	}
	if h.Agents == nil || len(h.Agents) != 0 {
		t.Errorf("agents %#v, want an empty list", h.Agents)
	}
	if h.IdleTimeoutMinutes != nil {
		t.Errorf("idle timeout %v, want nil for never", *h.IdleTimeoutMinutes)
	}
	if h.Size != "op-34" || h.DiskGB != 10 {
		t.Errorf("size %q disk %d", h.Size, h.DiskGB)
	}
	if h.CreatedAt != "2026-01-02T03:04:05Z" {
		t.Errorf("createdAt %q", h.CreatedAt)
	}

	d.IsPaused = false
	d.Status = 7
	if got := decodeHangar(d).Status; got != "unknown:7" {
		t.Errorf("status %q, want unknown:7", got)
	}
}

// The registry publishes the agent catalog and the idle timeouts as enums, and
// cannot import this package to read them. This keeps the two in step.
func TestHangarCatalogMatchesTheRegistry(t *testing.T) {
	var create *registry.Command
	for i, c := range registry.Commands {
		if strings.Join(c.Path, " ") == "hangar create" {
			create = &registry.Commands[i]
		}
	}
	if create == nil {
		t.Fatal("hangar create is not declared")
	}

	enums := map[string][]string{}
	for _, f := range create.Flags {
		enums[f.Name] = f.Enum
	}

	if got, want := strings.Join(enums["agent"], ","), strings.Join(HangarAgents, ","); got != want {
		t.Errorf("--agent enum is %s, and the catalog is %s", got, want)
	}
	if got, want := strings.Join(enums["idle-timeout"], ","), strings.Join(IdleTimeoutChoices(), ","); got != want {
		t.Errorf("--idle-timeout enum is %s, and the choices are %s", got, want)
	}
}
