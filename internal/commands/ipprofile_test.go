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
	"github.com/outplane/cli/internal/execctx"
	"github.com/outplane/cli/internal/output"
)

// Attaching a profile is checked against a stand-in for the API. Applications
// and databases share the assign and unassign endpoints, so what matters is
// that the request names the right target and only that one, that --db is
// refused beside --app, and that detaching sends the assignment's id, for an
// application exactly as before databases existed.

const (
	testProfileID       = "00000000-0000-4000-8000-0000000000a1"
	testDatabaseID      = "00000000-0000-4000-8000-0000000000b1"
	testAppID           = "00000000-0000-4000-8000-0000000000d1"
	testDBAssignmentID  = "00000000-0000-4000-8000-0000000000e1"
	testAppAssignmentID = "00000000-0000-4000-8000-0000000000f1"

	routeProfiles    = "GET /api/IPAccessProfile/GetByTeamId"
	routeProfile     = "GET /api/IPAccessProfile/GetById/" + testProfileID
	routeApps        = "GET /api/App/GetAppsByTeamId"
	routeDatabases   = "GET /api/DataStorage/GetAllDataSourcesByTeamId"
	routeDBContents  = "GET /api/DataStorage/GetDataStorageRolesAndDatabases/" + testDatabaseID
	routeDBProfiles  = "GET /api/IPAccessProfile/GetByDataStorageId/" + testDatabaseID
	routeAssign      = "POST /api/IPAccessProfile/Assign"
	routeUnassignDB  = "DELETE /api/IPAccessProfile/Unassign/" + testDBAssignmentID
	routeUnassignApp = "DELETE /api/IPAccessProfile/Unassign/" + testAppAssignmentID
)

// fakeIPAPI answers as the API does for a team with one profile, office, with
// two rules, one application, checkout, and one database, orders. attached
// links the profile to the database, appAttached to the application.
type fakeIPAPI struct {
	attached    bool
	appAttached bool
	sent        []sentRequest
}

func (f *fakeIPAPI) request(t *testing.T, flags Flags, args ...string) (Request, *bytes.Buffer) {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		route := r.Method + " " + r.URL.EscapedPath()
		f.sent = append(f.sent, sentRequest{r.Method, r.URL.EscapedPath(), string(raw)})

		data := "null"
		switch route {
		case routeProfiles:
			data = fmt.Sprintf(`[{"id":%q,"name":"office","ruleCount":2,"assignmentCount":%d,`+
				`"dataStorageAssignmentCount":%d}]`, testProfileID, oneIf(f.appAttached), oneIf(f.attached))
		case routeProfile:
			data = fmt.Sprintf(`{"id":%q,"name":"office","ruleCount":2,"assignmentCount":%d,`+
				`"dataStorageAssignmentCount":%d,`+
				`"rules":[{"cidr":"203.0.113.0/24","mode":10},{"cidr":"198.51.100.7/32","mode":10}],`+
				`"assignedResources":%s,"assignedDataStorages":%s}`,
				testProfileID, oneIf(f.appAttached), oneIf(f.attached), f.assignedResources(), f.assignedDataStorages())
		case routeApps:
			data = fmt.Sprintf(`[{"id":%q,"name":"checkout","status":0}]`, testAppID)
		case routeDatabases:
			data = fmt.Sprintf(`[{"id":%q,"name":"orders","status":20,"version":"17",`+
				`"region":"aws-eu-central-1","ipRestricted":%t}]`, testDatabaseID, f.attached)
		case routeDBContents:
			data = `{"roles":[{"name":"app"}],"databases":[{"name":"main","owner_name":"app"}]}`
		case routeDBProfiles:
			data = "[]"
			if f.attached {
				data = fmt.Sprintf(`[{"assignmentId":%q,"profile":{"id":%q,"name":"office",`+
					`"ruleCount":2,"assignmentCount":0,"dataStorageAssignmentCount":1}}]`,
					testDBAssignmentID, testProfileID)
			}
		case routeAssign:
			w.WriteHeader(http.StatusCreated)
		case routeUnassignDB, routeUnassignApp:
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

func oneIf(attached bool) int {
	if attached {
		return 1
	}
	return 0
}

func (f *fakeIPAPI) assignedResources() string {
	if !f.appAttached {
		return "null"
	}
	return fmt.Sprintf(`[{"assignmentId":%q,"appId":%q,"appName":"checkout"}]`, testAppAssignmentID, testAppID)
}

func (f *fakeIPAPI) assignedDataStorages() string {
	if !f.attached {
		return "[]"
	}
	return fmt.Sprintf(`[{"assignmentId":%q,"dataStorageId":%q,"dataStorageName":"orders"}]`,
		testDBAssignmentID, testDatabaseID)
}

// wrote lists the requests other than reads.
func (f *fakeIPAPI) wrote() []string {
	var out []string
	for _, s := range f.sent {
		if s.method != http.MethodGet {
			out = append(out, s.method+" "+s.path)
		}
	}
	return out
}

// assignBody is what the assign request carried.
func (f *fakeIPAPI) assignBody() map[string]any {
	var body map[string]any
	for _, s := range f.sent {
		if s.method == http.MethodPost {
			_ = json.Unmarshal([]byte(s.body), &body)
		}
	}
	return body
}

// An application is assigned exactly as before databases existed: the body
// names the application and nothing else.
func TestIPProfileAssignApp(t *testing.T) {
	f := &fakeIPAPI{}
	req, _ := f.request(t, Flags{"app": "checkout"}, "office")

	table, err := ipProfileAssign(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	wantDeclared(t, "ip-profile assign", table)

	if writes := f.wrote(); len(writes) != 1 || writes[0] != routeAssign {
		t.Fatalf("wrote %v, want only %s", writes, routeAssign)
	}
	body := f.assignBody()
	if body["ipAccessProfileId"] != testProfileID || body["appId"] != testAppID || len(body) != 2 {
		t.Errorf("sent %v", body)
	}

	row := table.Rows[0]
	if row["app"] != "checkout" || row["appId"] != testAppID || row["db"] != nil || row["changed"] != true {
		t.Errorf("result %v", row)
	}
}

func TestIPProfileAssignDB(t *testing.T) {
	f := &fakeIPAPI{}
	req, _ := f.request(t, Flags{"db": "orders"}, "office")

	table, err := ipProfileAssign(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	wantDeclared(t, "ip-profile assign", table)

	writes := f.wrote()
	if len(writes) != 1 || writes[0] != routeAssign {
		t.Fatalf("wrote %v, want only %s", writes, routeAssign)
	}

	body := f.assignBody()
	if body["ipAccessProfileId"] != testProfileID || body["dataStorageId"] != testDatabaseID || len(body) != 2 {
		t.Errorf("sent %v", body)
	}

	row := table.Rows[0]
	if row["db"] != "orders" || row["dbId"] != testDatabaseID || row["app"] != nil || row["changed"] != true {
		t.Errorf("result %v", row)
	}
}

func TestIPProfileAssignDBAlreadyAttached(t *testing.T) {
	f := &fakeIPAPI{attached: true}
	req, _ := f.request(t, Flags{"db": "orders"}, "office")

	table, err := ipProfileAssign(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if writes := f.wrote(); len(writes) != 0 {
		t.Fatalf("wrote %v, want nothing", writes)
	}
	if table.Rows[0]["changed"] != false {
		t.Errorf("changed %v, want false", table.Rows[0]["changed"])
	}
}

func TestIPProfileAssignDBDryRun(t *testing.T) {
	f := &fakeIPAPI{}
	req, _ := f.request(t, Flags{"db": "orders"}, "office")
	req.CLI.DryRun = true

	if _, err := ipProfileAssign(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if writes := f.wrote(); len(writes) != 0 {
		t.Fatalf("wrote %v, want nothing", writes)
	}
}

func TestIPProfileTargetIsOneOrTheOther(t *testing.T) {
	for name, run := range map[string]Handler{"assign": ipProfileAssign, "unassign": ipProfileUnassign} {
		t.Run(name, func(t *testing.T) {
			f := &fakeIPAPI{attached: true}
			req, _ := f.request(t, Flags{"db": "orders", "app": "checkout"}, "office")
			_, err := run(context.Background(), req)
			wantCode(t, err, clierr.KindUsage, "usage.conflicting_flags")
			if len(f.sent) != 0 {
				t.Fatalf("sent %v, want nothing", f.sent)
			}

			f = &fakeIPAPI{attached: true}
			req, _ = f.request(t, Flags{"db": " "}, "office")
			req.GivenFlags = map[string]bool{"db": true}
			_, err = run(context.Background(), req)
			wantCode(t, err, clierr.KindUsage, "usage.empty_argument")
			if len(f.sent) != 0 {
				t.Fatalf("sent %v, want nothing", f.sent)
			}
		})
	}
}

// An application is unassigned exactly as before databases existed: the only
// write is the delete of its assignment.
func TestIPProfileUnassignApp(t *testing.T) {
	f := &fakeIPAPI{appAttached: true}
	req, _ := f.request(t, Flags{"app": "checkout"}, "office")

	table, err := ipProfileUnassign(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	wantDeclared(t, "ip-profile unassign", table)

	if writes := f.wrote(); len(writes) != 1 || writes[0] != routeUnassignApp {
		t.Fatalf("wrote %v, want only %s", writes, routeUnassignApp)
	}
	row := table.Rows[0]
	if row["app"] != "checkout" || row["db"] != nil || row["changed"] != true {
		t.Errorf("result %v", row)
	}
}

func TestIPProfileUnassignDB(t *testing.T) {
	f := &fakeIPAPI{attached: true}
	req, _ := f.request(t, Flags{"db": testDatabaseID}, "office")

	table, err := ipProfileUnassign(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	wantDeclared(t, "ip-profile unassign", table)

	writes := f.wrote()
	if len(writes) != 1 || writes[0] != routeUnassignDB {
		t.Fatalf("wrote %v, want only %s", writes, routeUnassignDB)
	}
	if table.Rows[0]["changed"] != true || table.Rows[0]["db"] != "orders" {
		t.Errorf("result %v", table.Rows[0])
	}
}

func TestIPProfileUnassignDBNotAttached(t *testing.T) {
	f := &fakeIPAPI{}
	req, _ := f.request(t, Flags{"db": "orders"}, "office")

	table, err := ipProfileUnassign(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if writes := f.wrote(); len(writes) != 0 {
		t.Fatalf("wrote %v, want nothing", writes)
	}
	if table.Rows[0]["changed"] != false {
		t.Errorf("changed %v, want false", table.Rows[0]["changed"])
	}
}

func TestIPProfileGetShowsDatabases(t *testing.T) {
	f := &fakeIPAPI{attached: true}
	req, _ := f.request(t, nil, "office")

	table, err := ipProfileGet(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	wantDeclared(t, "ip-profile get", table)

	row := table.Rows[0]
	if row["databaseAssignments"] != 1 {
		t.Errorf("databaseAssignments %v, want 1", row["databaseAssignments"])
	}
	dbs, _ := row["assignedDatabases"].([]map[string]any)
	if len(dbs) != 1 || dbs[0]["db"] != "orders" || dbs[0]["id"] != testDBAssignmentID {
		t.Errorf("assignedDatabases %v", row["assignedDatabases"])
	}
}

func TestDBGetShowsIPAccess(t *testing.T) {
	f := &fakeIPAPI{attached: true}
	req, _ := f.request(t, nil, "orders")

	table, err := dbGet(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	wantDeclared(t, "db get", table)

	row := table.Rows[0]
	if row["ipRestricted"] != true {
		t.Errorf("ipRestricted %v, want true", row["ipRestricted"])
	}
	profiles, _ := row["ipProfiles"].([]map[string]any)
	if len(profiles) != 1 || profiles[0]["profile"] != "office" || profiles[0]["id"] != testDBAssignmentID {
		t.Errorf("ipProfiles %v", row["ipProfiles"])
	}
	if !strings.Contains(table.Footer, "office") || !strings.Contains(table.Footer, "Out Plane") {
		t.Errorf("footer %q names neither the profile nor the platform's applications", table.Footer)
	}
}
