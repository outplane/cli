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

// The env commands with --hangar are checked against a stand-in for the API
// rather than a live team. What matters is which requests go out, with which
// body, and which do not: the Hangar's id goes to the application's endpoints,
// a refused flag or a reserved key sends nothing at all, and on a paused
// Hangar --restart saves the change and sends no restart.

const (
	testHangarID     = "00000000-0000-4000-8000-000000000001"
	testGroupID      = "00000000-0000-4000-8000-0000000000a1"
	testAssignmentID = "00000000-0000-4000-8000-0000000000b1"
)

type sentRequest struct {
	method, path, body string
}

// fakeEnvAPI answers as the API does for one Hangar, myhangar, with two
// variables of its own and, when assigned is set, the group shared linked. Any
// other path is not found, so a request to an endpoint that does not exist
// fails the command.
type fakeEnvAPI struct {
	paused        bool
	assigned      bool
	refuseRestart bool
	sent          []sentRequest
}

func (f *fakeEnvAPI) request(t *testing.T, flags Flags, args ...string) (Request, *bytes.Buffer) {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.sent = append(f.sent, sentRequest{r.Method, r.URL.Path, string(body)})

		data := "null"
		switch r.Method + " " + r.URL.Path {
		case "GET /api/Hangar/GetHangarsByTeamId":
			data = fmt.Sprintf(`[{"id":%q,"name":"myhangar","status":50,"isPaused":%t,"ports":[]}]`,
				testHangarID, f.paused)
		case "GET /api/AppSetting/GetEnvironmentVariables/" + testHangarID:
			data = `[{"id":9,"key":"TIMEOUT","value":"30"},{"id":7,"key":"LOG_LEVEL","value":"debug"}]`
		case "GET /api/EnvVariableGroup/GetAssignedGroupsByAppId/" + testHangarID:
			data = "[]"
			if f.assigned {
				data = fmt.Sprintf(`[{"assignmentId":%q,"envVariableGroupId":%q,"envVariableGroupName":"shared",`+
					`"appId":%q,"appName":"myhangar","appType":20,"variableCount":2}]`,
					testAssignmentID, testGroupID, testHangarID)
			}
		case "GET /api/EnvVariableGroup/GetByTeamId":
			data = fmt.Sprintf(`[{"id":%q,"name":"shared","useInRuntime":true,"variableCount":2}]`, testGroupID)
		case "POST /api/Hangar/RestartHangar/" + testHangarID:
			if f.refuseRestart {
				w.WriteHeader(http.StatusConflict)
				fmt.Fprint(w, `{"data":null,"statusCode":409,"isSuccessful":false,"error":{"errors":["This Hangar is paused. Resume it instead."]}}`)
				return
			}
		case "POST /api/AppSetting/AddEnvironmentVariables/" + testHangarID,
			"DELETE /api/AppSetting/DeleteEnvironmentVariable/" + testHangarID + "/7",
			"DELETE /api/AppSetting/DeleteEnvironmentVariable/" + testHangarID + "/9",
			"POST /api/EnvVariableGroup/Assign",
			"DELETE /api/EnvVariableGroup/Unassign/" + testAssignmentID:
		default:
			http.NotFound(w, r)
			return
		}
		fmt.Fprintf(w, `{"data":%s,"statusCode":200,"isSuccessful":true}`, data)
	}))
	t.Cleanup(server.Close)

	var stdout, stderr bytes.Buffer
	exec := execctx.Context{RequestedFormat: execctx.FormatJSON}
	if flags == nil {
		flags = Flags{}
	}
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
		Args:  args,
		Flags: flags,
	}, &stderr
}

// writes are the requests that change something, in the order they were sent.
func (f *fakeEnvAPI) writes() []sentRequest {
	var out []sentRequest
	for _, r := range f.sent {
		if r.method != http.MethodGet {
			out = append(out, r)
		}
	}
	return out
}

func checkWrites(t *testing.T, f *fakeEnvAPI, want ...sentRequest) {
	t.Helper()
	got := f.writes()
	if len(got) != len(want) {
		t.Fatalf("sent %d writes, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i].method != want[i].method || got[i].path != want[i].path {
			t.Errorf("write %d is %s %s, want %s %s", i, got[i].method, got[i].path, want[i].method, want[i].path)
		}
		if want[i].body != "" && got[i].body != want[i].body {
			t.Errorf("write %d body is %s, want %s", i, got[i].body, want[i].body)
		}
	}
}

func hangarFlags(extra Flags) Flags {
	flags := Flags{"hangar": "myhangar"}
	for k, v := range extra {
		flags[k] = v
	}
	return flags
}

func TestEnvSetOnAHangarSendsOnlyTheNamedKeysAndRestarts(t *testing.T) {
	f := &fakeEnvAPI{}
	req, _ := f.request(t, hangarFlags(Flags{"restart": true}), "LOG_LEVEL=info", "URL=a=b")

	table, err := envSet(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}

	checkWrites(t, f,
		sentRequest{http.MethodPost, "/api/AppSetting/AddEnvironmentVariables/" + testHangarID,
			`{"environmentVariables":{"LOG_LEVEL":"info","URL":"a=b"}}`},
		sentRequest{http.MethodPost, "/api/Hangar/RestartHangar/" + testHangarID, ""})

	row := table.Rows[0]
	if row["app"] != "myhangar" || row["changed"] != true || row["restarted"] != true || row["deploymentId"] != nil {
		t.Errorf("result %v, want myhangar changed and restarted, with no deployment", row)
	}
}

func TestEnvSetOnAHangarWithoutRestartRestartsNothing(t *testing.T) {
	f := &fakeEnvAPI{}
	req, stderr := f.request(t, hangarFlags(nil), "LOG_LEVEL=info")

	table, err := envSet(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	checkWrites(t, f, sentRequest{http.MethodPost, "/api/AppSetting/AddEnvironmentVariables/" + testHangarID, ""})
	if table.Rows[0]["restarted"] != false {
		t.Errorf("restarted %v, want false", table.Rows[0]["restarted"])
	}
	if !strings.Contains(stderr.String(), "outplane hangar restart myhangar") {
		t.Errorf("no restart command in the notes:\n%s", stderr)
	}
}

// A paused Hangar is started by resuming it, and the server refuses a restart,
// so --restart saves the change, sends nothing more and does not fail.
func TestEnvSetOnAPausedHangarSavesAndPointsAtResume(t *testing.T) {
	f := &fakeEnvAPI{paused: true}
	req, stderr := f.request(t, hangarFlags(Flags{"restart": true}), "LOG_LEVEL=info")

	table, err := envSet(context.Background(), req)
	if err != nil {
		t.Fatalf("failed on a paused Hangar: %v", err)
	}
	checkWrites(t, f, sentRequest{http.MethodPost, "/api/AppSetting/AddEnvironmentVariables/" + testHangarID, ""})
	if row := table.Rows[0]; row["changed"] != true || row["restarted"] != false {
		t.Errorf("result %v, want changed and not restarted", row)
	}
	if !strings.Contains(stderr.String(), "outplane hangar resume myhangar") {
		t.Errorf("no resume command in the notes:\n%s", stderr)
	}
}

// A restart the server refuses leaves the change saved and is not a failure,
// as a deployment that could not start is not one.
func TestEnvSetOnAHangarWhoseRestartIsRefusedStillSucceeds(t *testing.T) {
	f := &fakeEnvAPI{refuseRestart: true}
	req, stderr := f.request(t, hangarFlags(Flags{"restart": true}), "LOG_LEVEL=info")

	table, err := envSet(context.Background(), req)
	if err != nil {
		t.Fatalf("failed after saving: %v", err)
	}
	if row := table.Rows[0]; row["changed"] != true || row["restarted"] != false {
		t.Errorf("result %v, want changed and not restarted", row)
	}
	if !strings.Contains(stderr.String(), "Resume it instead") {
		t.Errorf("the server's reason is not in the notes:\n%s", stderr)
	}
}

// One target per command, and each target's own way of applying a change. All
// of it is refused before anything is sent.
func TestEnvTargetFlagsThatDoNotGoTogether(t *testing.T) {
	cases := map[string]Flags{
		"--app and --hangar":   hangarFlags(Flags{"app": "myapp"}),
		"--deploy on a Hangar": hangarFlags(Flags{"deploy": true}),
		"--restart on the app": {"restart": true},
		"--restart with --app": {"app": "myapp", "restart": true},
		"--hangar given empty": {"hangar": " "},
	}
	for name, flags := range cases {
		f := &fakeEnvAPI{}
		req, _ := f.request(t, flags, "LOG_LEVEL=info")
		if name == "--hangar given empty" {
			req.GivenFlags = map[string]bool{"hangar": true}
		}

		_, err := envSet(context.Background(), req)
		e := clierr.AsError(err)
		if e == nil || e.Kind != clierr.KindUsage {
			t.Errorf("%s: got %v, want a usage error", name, err)
		}
		if len(f.sent) != 0 {
			t.Errorf("%s: sent %d requests, want none", name, len(f.sent))
		}
	}

	f := &fakeEnvAPI{}
	req, _ := f.request(t, hangarFlags(Flags{"app": "myapp"}), "shared")
	if _, err := envGroupAssign(context.Background(), req); clierr.AsError(err) == nil ||
		clierr.AsError(err).Code != "usage.conflicting_flags" || len(f.sent) != 0 {
		t.Errorf("env group assign with both targets: got %v after %d requests", err, len(f.sent))
	}
}

// Reserved keys are refused as on an application: before the Hangar is even
// looked up.
func TestEnvSetOnAHangarRefusesReservedKeys(t *testing.T) {
	for key, code := range map[string]string{
		"HANGAR_TOKEN": "env.reserved_prefix",
		"OP_DEBUG":     "env.reserved_prefix",
		"HOSTNAME":     "env.reserved_key",
	} {
		f := &fakeEnvAPI{}
		req, _ := f.request(t, hangarFlags(Flags{"restart": true}), "LOG_LEVEL=info", key+"=x")

		_, err := envSet(context.Background(), req)
		if e := clierr.AsError(err); e == nil || e.Code != code {
			t.Errorf("%s: got %v, want %s", key, err, code)
		}
		if len(f.sent) != 0 {
			t.Errorf("%s: sent %d requests, want none", key, len(f.sent))
		}
	}
}

func TestEnvUnsetOnAHangarDeletesByIDAfterResolvingEveryName(t *testing.T) {
	f := &fakeEnvAPI{}
	req, _ := f.request(t, hangarFlags(nil), "log_level", "TIMEOUT")

	table, err := envUnset(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	checkWrites(t, f,
		sentRequest{http.MethodDelete, "/api/AppSetting/DeleteEnvironmentVariable/" + testHangarID + "/7", ""},
		sentRequest{http.MethodDelete, "/api/AppSetting/DeleteEnvironmentVariable/" + testHangarID + "/9", ""})
	if keys := table.Rows[0]["keys"]; fmt.Sprint(keys) != "[LOG_LEVEL TIMEOUT]" {
		t.Errorf("keys %v, want the names as stored", keys)
	}

	f = &fakeEnvAPI{}
	req, _ = f.request(t, hangarFlags(nil), "LOG_LEVEL", "MISSING")
	_, err = envUnset(context.Background(), req)
	e := clierr.AsError(err)
	if e == nil || e.Code != "env.not_found" {
		t.Fatalf("got %v, want env.not_found", err)
	}
	if w := f.writes(); len(w) != 0 {
		t.Errorf("removed %v although one name was not set", w)
	}
	if raw, _ := json.Marshal(e); !strings.Contains(string(raw), "--hangar") {
		t.Errorf("the step to list the variables does not name the Hangar: %s", raw)
	}
}

// Values are masked unless asked for, as on an application.
func TestEnvListOnAHangarMasks(t *testing.T) {
	f := &fakeEnvAPI{}
	req, _ := f.request(t, hangarFlags(nil))

	table, err := envList(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(table.Rows)
	if strings.Contains(string(raw), `"debug"`) {
		t.Errorf("a value is shown without --reveal: %s", raw)
	}
	if len(table.Rows) != 2 || table.Rows[0]["key"] != "LOG_LEVEL" {
		t.Errorf("rows %s, want two variables by key", raw)
	}
}

func TestEnvGroupAssignSendsTheHangarAsTheApp(t *testing.T) {
	f := &fakeEnvAPI{}
	req, _ := f.request(t, hangarFlags(Flags{"restart": true}), "shared")

	table, err := envGroupAssign(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	checkWrites(t, f,
		sentRequest{http.MethodPost, "/api/EnvVariableGroup/Assign",
			fmt.Sprintf(`{"appId":%q,"envVariableGroupId":%q}`, testHangarID, testGroupID)},
		sentRequest{http.MethodPost, "/api/Hangar/RestartHangar/" + testHangarID, ""})
	if row := table.Rows[0]; row["app"] != "myhangar" || row["changed"] != true || row["restarted"] != true {
		t.Errorf("result %v, want changed and restarted", row)
	}

	// Already assigned: nothing to change, so nothing is restarted either.
	f = &fakeEnvAPI{assigned: true}
	req, _ = f.request(t, hangarFlags(Flags{"restart": true}), "shared")
	table, err = envGroupAssign(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	checkWrites(t, f)
	if row := table.Rows[0]; row["changed"] != false || row["restarted"] != false {
		t.Errorf("result %v, want nothing changed", row)
	}
}

func TestEnvGroupUnassignFromAPausedHangar(t *testing.T) {
	f := &fakeEnvAPI{assigned: true, paused: true}
	req, _ := f.request(t, hangarFlags(Flags{"restart": true}), "shared")

	table, err := envGroupUnassign(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	checkWrites(t, f, sentRequest{http.MethodDelete, "/api/EnvVariableGroup/Unassign/" + testAssignmentID, ""})
	if row := table.Rows[0]; row["changed"] != true || row["restarted"] != false {
		t.Errorf("result %v, want changed and, paused, not restarted", row)
	}
}

// restarted is how a result tells a Hangar from an application, which has
// nothing to restart.
func TestRestartedIsNullForAnApplication(t *testing.T) {
	app := envTarget{App: core.App{ID: "app-id", Name: "myapp"}}
	if got := envChangeTable(app, []string{"KEY"}, "set", true, 0, false).Rows[0]["restarted"]; got != nil {
		t.Errorf("restarted is %v for an application, want null", got)
	}
}
