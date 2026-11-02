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
	thirty, day, fortyFive := 30, 1440, 45

	cases := []struct {
		name string
		spec NewHangar
		code string
	}{
		{"a valid one", NewHangar{Name: "workbench", Agents: []string{"claude-code", "codex"}, IdleTimeoutMinutes: &thirty}, ""},
		{"never times out", NewHangar{Name: "workbench"}, ""},
		{"a day", NewHangar{Name: "workbench", IdleTimeoutMinutes: &day}, ""},
		{"terminal only", NewHangar{Name: "workbench", Agents: []string{}}, ""},
		{"too short", NewHangar{Name: "box"}, "hangar.name_invalid"},
		{"too long", NewHangar{Name: strings.Repeat("a", 46)}, "hangar.name_invalid"},
		{"not alphanumeric", NewHangar{Name: "work-bench"}, "hangar.name_invalid"},
		{"reserved", NewHangar{Name: "Admin"}, "hangar.name_reserved"},
		{"unknown agent", NewHangar{Name: "workbench", Agents: []string{"notepad"}}, "hangar.agent_invalid"},
		{"agent twice", NewHangar{Name: "workbench", Agents: []string{"codex", "codex"}}, "hangar.agent_duplicate"},
		{"timeout not offered", NewHangar{Name: "workbench", IdleTimeoutMinutes: &fortyFive}, "hangar.idle_timeout_invalid"},
		{"a repository", NewHangar{Name: "workbench", Repository: "acme/web.site", Branch: "main"}, ""},
		{"a repository with no branch", NewHangar{Name: "workbench", Repository: "acme/web"}, "hangar.branch_required"},
		{"a repository ending in a dot", NewHangar{Name: "workbench", Repository: "acme/web.", Branch: "main"}, "hangar.repository_invalid"},
		{"an owner with a dot", NewHangar{Name: "workbench", Repository: "ac.me/web", Branch: "main"}, "hangar.repository_invalid"},
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

// The disk id is what the disk's usage is looked up by, so it is compared in
// one case, and the empty GUID the server sends for no disk is no disk.
func TestDecodeHangarDiskAndLimits(t *testing.T) {
	var d hangarDTO
	raw := `{"name":"workbench","instanceType":"op-34","diskId":"0A1B2C3D-0000-4000-8000-00000000000A",` +
		`"cpuLimitMillicores":1000,"memoryLimitMb":2048}`
	if err := json.Unmarshal([]byte(raw), &d); err != nil {
		t.Fatal(err)
	}

	h := decodeHangar(d)
	if h.DiskID != "0a1b2c3d-0000-4000-8000-00000000000a" {
		t.Errorf("disk id %q, want it lower case", h.DiskID)
	}
	if h.CPULimitMillicores != 1000 || h.MemoryLimitMB != 2048 {
		t.Errorf("limits %d mc %d MB", h.CPULimitMillicores, h.MemoryLimitMB)
	}

	d.DiskID = noDiskID
	if got := decodeHangar(d).DiskID; got != "" {
		t.Errorf("disk id %q, want none for the empty GUID", got)
	}
}

// Every port has an internal address and only a public one has an outside
// one, whatever the server sent, and the set comes out lowest first and never
// as null.
func TestDecodeHangarPorts(t *testing.T) {
	var d hangarDTO
	raw := `{"name":"workbench","ports":[` +
		`{"port":5432,"scheme":30,"isPublic":true,"publicUrl":"gate.example.test:30001"},` +
		`{"port":3000,"scheme":10,"isPublic":false,"publicUrl":"https://stale.example.test"}]}`
	if err := json.Unmarshal([]byte(raw), &d); err != nil {
		t.Fatal(err)
	}

	got := decodeHangar(d).Ports
	want := []HangarPort{
		{Port: 3000, Scheme: "http", Internal: "workbench:3000"},
		{Port: 5432, Scheme: "tcp", Public: true, URL: "gate.example.test:30001", Internal: "workbench:5432"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d] got %+v, want %+v", i, got[i], want[i])
		}
	}

	if ports := decodeHangar(hangarDTO{Name: "workbench"}).Ports; ports == nil || len(ports) != 0 {
		t.Errorf("ports %#v, want an empty list", ports)
	}
}

func TestCheckHangarPorts(t *testing.T) {
	cases := []struct {
		name  string
		ports []NewPort
		code  string
	}{
		{"none at all", nil, ""},
		{"the most there can be", []NewPort{{Port: 3000, Scheme: "http"}, {Port: 5173, Scheme: "h2c"}, {Port: 5432, Scheme: "tcp", Public: true}}, ""},
		{"one too many", []NewPort{{Port: 3000, Scheme: "http"}, {Port: 3001, Scheme: "http"}, {Port: 3002, Scheme: "http"}, {Port: 3003, Scheme: "http"}}, "hangar.port_limit"},
		{"the Hangar's own", []NewPort{{Port: HangarOwnPort, Scheme: "http"}}, "hangar.port_reserved"},
		{"out of range", []NewPort{{Port: 65536, Scheme: "http"}}, "hangar.port_invalid"},
		{"zero", []NewPort{{Port: 0, Scheme: "http"}}, "hangar.port_invalid"},
		{"unknown scheme", []NewPort{{Port: 3000, Scheme: "https"}}, "hangar.scheme_invalid"},
		{"a scheme the server sent and this release does not know", []NewPort{{Port: 3000, Scheme: "unknown:40"}}, "hangar.scheme_invalid"},
		{"twice", []NewPort{{Port: 3000, Scheme: "http"}, {Port: 3000, Scheme: "tcp"}}, "hangar.port_duplicate"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckHangarPorts(tc.ports)
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
			if e.Code != tc.code || e.Kind != clierr.KindUsage {
				t.Errorf("code %q kind %q, want %q usage (%v)", e.Code, e.Kind, tc.code, e)
			}
		})
	}
}

// The choices are what --idle-timeout takes and what an error lists, so each one
// has to be written the way a person would type it.
func TestIdleTimeoutChoices(t *testing.T) {
	const want = "15m,30m,1h,2h,6h,12h,24h,never"
	if got := strings.Join(IdleTimeoutChoices(), ","); got != want {
		t.Errorf("choices are %s, want %s", got, want)
	}
}

// The registry publishes the agent catalog and the idle timeouts as enums, and
// cannot import this package to read them. This keeps the two in step, in
// create and in update.
func TestHangarCatalogMatchesTheRegistry(t *testing.T) {
	enums := map[string]map[string][]string{}
	for _, c := range registry.Commands {
		path := strings.Join(c.Path, " ")
		if path != "hangar create" && path != "hangar update" {
			continue
		}
		enums[path] = map[string][]string{}
		for _, f := range c.Flags {
			enums[path][f.Name] = f.Enum
		}
	}
	for _, path := range []string{"hangar create", "hangar update"} {
		if enums[path] == nil {
			t.Fatalf("%s is not declared", path)
		}
		if got, want := strings.Join(enums[path]["idle-timeout"], ","), strings.Join(IdleTimeoutChoices(), ","); got != want {
			t.Errorf("%s: --idle-timeout enum is %s, and the choices are %s", path, got, want)
		}
	}

	if got, want := strings.Join(enums["hangar create"]["agent"], ","), strings.Join(HangarAgents, ","); got != want {
		t.Errorf("--agent enum is %s, and the catalog is %s", got, want)
	}
}
