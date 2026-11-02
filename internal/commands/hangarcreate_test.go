package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/outplane/cli/internal/cli"
	"github.com/outplane/cli/internal/clierr"
	"github.com/outplane/cli/internal/config"
	"github.com/outplane/cli/internal/core"
	"github.com/outplane/cli/internal/execctx"
	"github.com/outplane/cli/internal/output"
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

// fakeCreateAPI answers the create as the API does, echoing the repository it
// was sent, or refusing it with refusal as a 400. It keeps every body it
// received, so a test can read what went out and whether anything did.
type fakeCreateAPI struct {
	refusal string
	bodies  []map[string]any
}

func (f *fakeCreateAPI) request(t *testing.T, flags Flags) (Request, *bytes.Buffer) {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method+" "+r.URL.Path != "POST /api/Hangar/CreateHangar" {
			http.NotFound(w, r)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Errorf("body is not JSON: %s", raw)
		}
		f.bodies = append(f.bodies, body)

		if f.refusal != "" {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, `{"data":null,"statusCode":400,"isSuccessful":false,"error":{"errors":[%q]}}`, f.refusal)
			return
		}
		repo, _ := body["sourceRepository"].(string)
		branch, _ := body["branch"].(string)
		public, _ := body["isPublicSource"].(bool)
		fmt.Fprintf(w, `{"data":{"id":%q,"name":"myhangar","status":30,"agents":["claude-code","codex"],`+
			`"ports":[],"sourceRepository":%q,"branch":%q,"isPublicSource":%t},"statusCode":200,"isSuccessful":true}`,
			testHangarID, repo, branch, public)
	}))
	t.Cleanup(server.Close)

	var stdout, stderr bytes.Buffer
	exec := execctx.Context{RequestedFormat: execctx.FormatJSON}
	return Request{
		CLI: &cli.Context{
			Exec: exec,
			Config: config.Resolved{
				APIURL: config.Value{Value: server.URL + "/api"},
				Token:  config.Value{Value: "test-token"},
				TeamID: config.Value{Value: "00000000-0000-4000-8000-0000000000c1"},
			},
			Out: output.New(&stdout, &stderr, exec),
		},
		Args:  []string{"myhangar"},
		Flags: flags,
	}, &stderr
}

// A repository goes out in an application's source fields, as GitHub, and
// comes back in the result with the note that says where it lands.
func TestHangarCreateSendsTheRepository(t *testing.T) {
	f := &fakeCreateAPI{}
	req, stderr := f.request(t, Flags{"repo": " acme/web ", "branch": "main"})

	table, err := hangarCreate(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.bodies) != 1 {
		t.Fatalf("%d creates sent, want 1", len(f.bodies))
	}

	body := f.bodies[0]
	want := map[string]any{"sourceProvider": 10.0, "sourceRepository": "acme/web", "branch": "main", "isPublicSource": false}
	for key, value := range want {
		if body[key] != value {
			t.Errorf("%s is %v, want %v", key, body[key], value)
		}
	}

	row := table.Rows[0]
	if row["repository"] != "acme/web" || row["branch"] != "main" || row["publicSource"] != false {
		t.Errorf("result %v, want acme/web on main", row)
	}
	if !strings.Contains(stderr.String(), "clones acme/web@main into ~/web") {
		t.Errorf("no clone note:\n%s", stderr)
	}
}

// Without --repo the source fields are left out, which is how the server reads
// no repository, and the result says there is none.
func TestHangarCreateWithoutARepositorySendsNoSource(t *testing.T) {
	f := &fakeCreateAPI{}
	req, stderr := f.request(t, Flags{})

	table, err := hangarCreate(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"sourceProvider", "sourceRepository", "branch", "isPublicSource"} {
		if _, ok := f.bodies[0][key]; ok {
			t.Errorf("body carries %s without a repository: %v", key, f.bodies[0])
		}
	}
	if row := table.Rows[0]; row["repository"] != nil || row["branch"] != nil {
		t.Errorf("result %v, want no repository", row)
	}
	if strings.Contains(stderr.String(), "clones") {
		t.Errorf("a clone note without a repository:\n%s", stderr)
	}
}

// --branch is required with --repo, and --branch or --public-repo without it
// describes nothing. All of them are refused before anything is sent.
func TestHangarCreateRepositoryFlags(t *testing.T) {
	cases := []struct {
		name  string
		flags Flags
		code  string
	}{
		{"public, with its branch", Flags{"repo": "acme/web", "branch": "main", "public-repo": true}, ""},
		{"no branch", Flags{"repo": "acme/web"}, "hangar.branch_required"},
		{"public without a repository", Flags{"public-repo": true}, "hangar.repository_required"},
		{"a branch without a repository", Flags{"branch": "main"}, "hangar.repository_required"},
		{"not owner/name", Flags{"repo": "web", "branch": "main"}, "hangar.repository_invalid"},
		{"a path", Flags{"repo": "acme/web/docs", "branch": "main"}, "hangar.repository_invalid"},
		{"a dot name", Flags{"repo": "acme/..", "branch": "main"}, "hangar.repository_invalid"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeCreateAPI{}
			req, _ := f.request(t, tc.flags)
			req.CLI.DryRun = true

			_, err := hangarCreate(context.Background(), req)
			if len(f.bodies) != 0 {
				t.Errorf("%d creates sent, want none", len(f.bodies))
			}
			if tc.code == "" {
				if err != nil {
					t.Fatalf("refused: %v", err)
				}
				return
			}
			if e := clierr.AsError(err); e == nil || e.Code != tc.code || e.Kind != clierr.KindUsage {
				t.Errorf("got %v, want %s", err, tc.code)
			}
		})
	}
}

// A repository the platform cannot see is refused with the server's app create
// message, and is explained as app create explains it.
func TestHangarCreateExplainsARepositoryItCannotSee(t *testing.T) {
	f := &fakeCreateAPI{refusal: "Repository not found in your GitHub installations"}
	req, _ := f.request(t, Flags{"repo": "acme/web", "branch": "main"})

	_, err := hangarCreate(context.Background(), req)
	e := clierr.AsError(err)
	if e == nil || e.Code != "hangar.repository_unavailable" {
		t.Fatalf("got %v, want hangar.repository_unavailable", err)
	}
	if len(e.NextSteps) == 0 || strings.Join(e.NextSteps[0].Argv, " ") != "outplane repos" {
		t.Errorf("steps %v, want outplane repos first", e.NextSteps)
	}
}
