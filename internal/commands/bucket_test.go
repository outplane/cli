package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/outplane/cli/internal/api"
	"github.com/outplane/cli/internal/cli"
	"github.com/outplane/cli/internal/clierr"
	"github.com/outplane/cli/internal/config"
	"github.com/outplane/cli/internal/core"
	"github.com/outplane/cli/internal/execctx"
	"github.com/outplane/cli/internal/output"
	"github.com/outplane/cli/internal/registry"
)

// The bucket commands are checked against a stand-in for the API rather than a
// live team. What matters is which requests go out, with which body, and which
// do not, and that a secret reaches the result and nothing else.

const (
	testBucketID = "00000000-0000-4000-8000-0000000000d1"
	testSecret   = "test-secret-not-a-real-one"

	routeList      = "GET /api/Bucket/GetBucketsByTeamId"
	routeCreate    = "POST /api/Bucket/CreateBucket"
	routeUpdate    = "PUT /api/Bucket/UpdateBucket/" + testBucketID
	routeDelete    = "DELETE /api/Bucket/DeleteBucket/" + testBucketID
	routeKeys      = "GET /api/Bucket/GetAccessKeys/" + testBucketID
	routeKeyCreate = "POST /api/Bucket/CreateAccessKey/" + testBucketID
	routeKeyDelete = "DELETE /api/Bucket/DeleteAccessKey/" + testBucketID + "/"
)

type bucketRefusal struct {
	status  int
	message string
}

// fakeBucketAPI answers as the API does for a team with one bucket, uploads, and
// the keys given as JSON objects. A refusal named for a route answers instead.
// Paths are kept as they travelled, so escaping can be read back.
type fakeBucketAPI struct {
	public   bool
	keys     []string
	refusals map[string]bucketRefusal
	dropKey  bool
	stallKey bool
	// answerKey writes the whole answer to a key's create, for one the API would
	// not give.
	answerKey func(http.ResponseWriter)
	sent      []sentRequest
}

func (f *fakeBucketAPI) request(t *testing.T, flags Flags, args ...string) (Request, *bytes.Buffer) {
	t.Helper()

	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		route := r.Method + " " + r.URL.EscapedPath()
		f.sent = append(f.sent, sentRequest{r.Method, r.URL.EscapedPath(), string(raw)})

		if refusal, ok := f.refusals[route]; ok {
			w.WriteHeader(refusal.status)
			fmt.Fprintf(w, `{"data":null,"statusCode":%d,"isSuccessful":false,"error":{"errors":[%q]}}`,
				refusal.status, refusal.message)
			return
		}

		var body map[string]any
		_ = json.Unmarshal(raw, &body)

		data := "null"
		switch {
		case route == routeList:
			data = "[" + bucketJSON("uploads", f.public) + "]"
		case route == routeCreate:
			name, _ := body["name"].(string)
			public, _ := body["isPublic"].(bool)
			w.WriteHeader(http.StatusCreated)
			data = bucketJSON(name, public)
		case route == routeUpdate:
			public, _ := body["isPublic"].(bool)
			data = bucketJSON("uploads", public)
		case route == routeDelete, strings.HasPrefix(route, routeKeyDelete):
		case route == routeKeys:
			data = "[" + strings.Join(f.keys, ",") + "]"
		case route == routeKeyCreate:
			if f.answerKey != nil {
				f.answerKey(w)
				return
			}
			if f.stallKey {
				select {
				case <-r.Context().Done():
				case <-release:
				}
				return
			}
			if f.dropKey {
				conn, _, err := w.(http.Hijacker).Hijack()
				if err == nil {
					conn.Close()
				}
				return
			}
			name, _ := body["name"].(string)
			permission, _ := body["permission"].(float64)
			w.WriteHeader(http.StatusCreated)
			data = fmt.Sprintf(`{"accessKeyId":"AKEYNEW","name":%q,"permission":%d,"secretAccessKey":%q}`,
				name, int(permission), testSecret)
		default:
			http.NotFound(w, r)
			return
		}
		fmt.Fprintf(w, `{"data":%s,"statusCode":200,"isSuccessful":true}`, data)
	}))
	t.Cleanup(server.Close)
	t.Cleanup(func() { close(release) })

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

func bucketJSON(name string, public bool) string {
	return fmt.Sprintf(`{"id":%q,"name":%q,"providerBucketName":"%s-a1b2c3d4","isPublic":%t,`+
		`"endpoint":"https://bucket.example.test","region":"auto",`+
		`"url":"https://%s-a1b2c3d4.bucket.example.test","createdDate":"2026-06-15T17:17:26"}`,
		testBucketID, name, name, public, name)
}

func keyJSON(id, name string, permission int) string {
	return fmt.Sprintf(`{"accessKeyId":%q,"name":%q,"permission":%d}`, id, name, permission)
}

// wrote counts the requests other than the reads that resolve a name.
func (f *fakeBucketAPI) wrote() int {
	n := 0
	for _, s := range f.sent {
		if s.method != http.MethodGet {
			n++
		}
	}
	return n
}

func wantCode(t *testing.T, err error, kind clierr.Kind, code string) *clierr.Error {
	t.Helper()
	e := clierr.AsError(err)
	if e == nil {
		t.Fatalf("no error, want %s", code)
	}
	if e.Kind != kind || e.Code != code {
		t.Fatalf("got %s %s (%v), want %s %s", e.Kind, e.Code, err, kind, code)
	}
	return e
}

func hasStep(e *clierr.Error, argv string) bool {
	for _, s := range e.NextSteps {
		if strings.Join(s.Argv, " ") == argv {
			return true
		}
	}
	return false
}

// A row is rendered through --fields, which refuses anything undeclared, so
// every key and column a handler returns has to be in the declaration.
func wantDeclared(t *testing.T, path string, table output.Table) {
	t.Helper()
	for _, c := range registry.Commands {
		if strings.Join(c.Path, " ") != path {
			continue
		}
		declared := map[string]bool{}
		for _, f := range c.OutputFields {
			declared[f.Name] = true
		}
		for _, column := range table.Columns {
			if !declared[column] {
				t.Errorf("%s: column %s is not declared", path, column)
			}
		}
		for _, row := range table.Rows {
			for key := range row {
				if !declared[key] {
					t.Errorf("%s: field %s is not declared", path, key)
				}
			}
		}
		return
	}
	t.Fatalf("%s is not declared", path)
}

func TestEveryBucketCommandHasAHandler(t *testing.T) {
	for _, c := range registry.Commands {
		if c.Path[0] != "bucket" {
			continue
		}
		if _, ok := Lookup(c.Path); !ok {
			t.Errorf("%s is declared with no handler", strings.Join(c.Path, " "))
		}
	}
}

func TestBucketListAndGet(t *testing.T) {
	f := &fakeBucketAPI{public: true}
	req, _ := f.request(t, nil)

	table, err := bucketList(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	wantDeclared(t, "bucket list", table)
	row := table.Rows[0]
	if row["bucketName"] != "uploads-a1b2c3d4" || row["public"] != true || row["createdAt"] != "2026-06-15T17:17:26Z" {
		t.Errorf("row %v", row)
	}
	if _, leaked := row["providerBucketName"]; leaked {
		t.Errorf("the wire name reached the row: %v", row)
	}

	req, _ = f.request(t, nil, "uploads")
	table, err = bucketGet(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	wantDeclared(t, "bucket get", table)
	if !table.Single || table.Rows[0]["id"] != testBucketID || table.Rows[0]["region"] != "auto" {
		t.Errorf("got %+v", table)
	}

	req, _ = f.request(t, nil, "missing")
	_, err = bucketGet(context.Background(), req)
	e := wantCode(t, err, clierr.KindNotFound, "bucket.not_found")
	if !strings.Contains(e.Hint, "uploads") || !hasStep(e, "outplane bucket list") {
		t.Errorf("hint %q and steps %v, want the names and a list step", e.Hint, e.NextSteps)
	}

	req, _ = f.request(t, nil, "  ")
	_, err = bucketGet(context.Background(), req)
	wantCode(t, err, clierr.KindUsage, "usage.empty_argument")

	req, _ = f.request(t, nil)
	_, err = bucketGet(context.Background(), req)
	wantCode(t, err, clierr.KindUsage, "usage.missing_argument")
}

func TestBucketCreateSendsWhatWasAsked(t *testing.T) {
	for _, public := range []bool{false, true} {
		f := &fakeBucketAPI{}
		req, stderr := f.request(t, Flags{"public": public}, "uploads")

		table, err := bucketCreate(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		if len(f.sent) != 1 || f.sent[0].method != http.MethodPost {
			t.Fatalf("sent %+v, want one POST", f.sent)
		}

		var body map[string]any
		if err := json.Unmarshal([]byte(f.sent[0].body), &body); err != nil {
			t.Fatal(err)
		}
		if body["name"] != "uploads" || body["isPublic"] != public {
			t.Errorf("body %v, want uploads and %t", body, public)
		}

		wantDeclared(t, "bucket create", table)
		row := table.Rows[0]
		if row["changed"] != true || row["bucketName"] != "uploads-a1b2c3d4" || row["public"] != public {
			t.Errorf("row %v", row)
		}
		if !strings.Contains(stderr.String(), "outplane bucket key create uploads <KEY_NAME> --permission read-write") {
			t.Errorf("no note about a key:\n%s", stderr)
		}
		if !strings.Contains(stderr.String(), "a person at their own terminal") {
			t.Errorf("the note about a key does not say who runs it:\n%s", stderr)
		}
	}
}

func TestBucketCreateSendsNothingWhenItShouldNot(t *testing.T) {
	f := &fakeBucketAPI{}
	req, _ := f.request(t, nil, "uploads")
	req.CLI.DryRun = true

	table, err := bucketCreate(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.sent) != 0 {
		t.Errorf("a dry run sent %+v", f.sent)
	}
	wantDeclared(t, "bucket create", table)
	row := table.Rows[0]
	for _, key := range []string{"id", "bucketName", "endpoint", "region", "url", "createdAt"} {
		if value, ok := row[key]; !ok || value != nil {
			t.Errorf("%s is %v, want null", key, value)
		}
	}
	if row["changed"] != false || row["name"] != "uploads" {
		t.Errorf("row %v", row)
	}

	for name, code := range map[string]string{"A": "bucket.name_invalid", "a--b": "bucket.name_invalid", " ": "usage.empty_argument"} {
		f := &fakeBucketAPI{}
		req, _ := f.request(t, nil, name)
		_, err := bucketCreate(context.Background(), req)
		wantCode(t, err, clierr.KindUsage, code)
		if len(f.sent) != 0 {
			t.Errorf("%q sent %+v", name, f.sent)
		}
	}
}

func TestBucketCreateExplainsRefusals(t *testing.T) {
	refuse := func(status int, message string) error {
		f := &fakeBucketAPI{refusals: map[string]bucketRefusal{routeCreate: {status, message}}}
		req, _ := f.request(t, nil, "uploads")
		_, err := bucketCreate(context.Background(), req)
		return err
	}

	err := refuse(http.StatusTooManyRequests, "Bucket limit reached (5/5).")
	e := wantCode(t, err, clierr.KindQuota, "bucket.limit_reached")
	if e.Message != "Bucket limit reached (5/5)." {
		t.Errorf("message %q, the server's is kept", e.Message)
	}
	if !hasStep(e, "outplane bucket list") || hasStep(e, "outplane app list") {
		t.Errorf("steps %v, want bucket list and no app list", e.NextSteps)
	}
	if strings.Contains(e.Hint, "raised in the console") {
		t.Errorf("hint %q is the plan limit's", e.Hint)
	}
	if clierr.ExitCodeOf(err) != 7 || e.Retryable {
		t.Errorf("exit %d, retryable %t", clierr.ExitCodeOf(err), e.Retryable)
	}

	err = refuse(http.StatusPaymentRequired, "Your subscription is inactive.")
	e = wantCode(t, err, clierr.KindQuota, "bucket.subscription_inactive")
	if strings.Contains(e.Hint, "Hobby") || len(e.NextSteps) != 0 {
		t.Errorf("hint %q and steps %v, want neither the plan nor a step", e.Hint, e.NextSteps)
	}

	err = refuse(http.StatusConflict, "A bucket named 'uploads' already exists.")
	e = wantCode(t, err, clierr.KindConflict, "bucket.name_taken")
	if !hasStep(e, "outplane bucket get uploads") {
		t.Errorf("steps %v, want bucket get", e.NextSteps)
	}

	if e := clierr.AsError(refuse(http.StatusNotFound, "not found")); e.Code != "" {
		t.Errorf("a 404 labelled %s", e.Code)
	}
}

func TestBucketUpdateReadsFlagsByValue(t *testing.T) {
	cases := []struct {
		name  string
		flags Flags
		code  string
	}{
		{"neither", Flags{}, "usage.no_change"},
		{"public false", Flags{"public": false}, "usage.no_change"},
		{"private false", Flags{"private": false}, "usage.no_change"},
		{"both false", Flags{"public": false, "private": false}, "usage.no_change"},
		{"both", Flags{"public": true, "private": true}, "usage.conflicting_flags"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeBucketAPI{}
			req, _ := f.request(t, tc.flags, "uploads")
			// As the flag parser reports it, every written flag counts as given.
			req.GivenFlags = map[string]bool{"public": true, "private": true}

			_, err := bucketUpdate(context.Background(), req)
			wantCode(t, err, clierr.KindUsage, tc.code)
			if len(f.sent) != 0 {
				t.Errorf("sent %+v, want nothing at all", f.sent)
			}
		})
	}
}

func TestBucketUpdate(t *testing.T) {
	// The field goes out both ways, because the server reads an omitted one as
	// false.
	t.Run("private keeps the field", func(t *testing.T) {
		f := &fakeBucketAPI{public: true}
		req, stderr := f.request(t, Flags{"private": true}, "uploads")
		table, err := bucketUpdate(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		if f.wrote() != 1 || f.sent[len(f.sent)-1].body != `{"isPublic":false}` {
			t.Errorf("sent %+v", f.sent)
		}
		wantDeclared(t, "bucket update", table)
		if row := table.Rows[0]; row["public"] != false || row["changed"] != true {
			t.Errorf("row %v", row)
		}
		if !strings.Contains(stderr.String(), "few minutes") {
			t.Errorf("no note about the delay:\n%s", stderr)
		}
	})

	t.Run("a flag set to false beside the other", func(t *testing.T) {
		f := &fakeBucketAPI{}
		req, _ := f.request(t, Flags{"public": true, "private": false}, "uploads")
		if _, err := bucketUpdate(context.Background(), req); err != nil {
			t.Fatal(err)
		}
		if f.sent[len(f.sent)-1].body != `{"isPublic":true}` {
			t.Errorf("sent %+v", f.sent)
		}
	})

	t.Run("the same value sends nothing", func(t *testing.T) {
		f := &fakeBucketAPI{public: true}
		req, _ := f.request(t, Flags{"public": true}, "uploads")
		table, err := bucketUpdate(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		if f.wrote() != 0 {
			t.Errorf("sent %+v", f.sent)
		}
		if table.Rows[0]["changed"] != false {
			t.Errorf("row %v", table.Rows[0])
		}
	})

	t.Run("a dry run reports the requested value", func(t *testing.T) {
		f := &fakeBucketAPI{}
		req, _ := f.request(t, Flags{"public": true}, "uploads")
		req.CLI.DryRun = true
		table, err := bucketUpdate(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		if f.wrote() != 0 {
			t.Errorf("sent %+v", f.sent)
		}
		if row := table.Rows[0]; row["public"] != true || row["changed"] != false {
			t.Errorf("row %v", row)
		}
	})

	t.Run("refusals", func(t *testing.T) {
		update := func(status int) error {
			f := &fakeBucketAPI{refusals: map[string]bucketRefusal{routeUpdate: {status, "refused"}}}
			req, _ := f.request(t, Flags{"public": true}, "uploads")
			_, err := bucketUpdate(context.Background(), req)
			return err
		}
		e := wantCode(t, update(http.StatusPaymentRequired), clierr.KindQuota, "bucket.subscription_inactive")
		if strings.Contains(e.Hint, "Hobby") {
			t.Errorf("hint %q", e.Hint)
		}
		e = wantCode(t, update(http.StatusNotFound), clierr.KindNotFound, "bucket.not_found")
		if e.Message != "the bucket no longer exists" {
			t.Errorf("message %q, the server's is not written for a reader", e.Message)
		}
		wantCode(t, update(http.StatusConflict), clierr.KindConflict, "")
	})
}

func TestBucketDelete(t *testing.T) {
	both := Flags{"yes": true, "confirm-name": "uploads"}

	t.Run("confirmed", func(t *testing.T) {
		f := &fakeBucketAPI{}
		req, stderr := f.request(t, both, "uploads")
		table, err := bucketDelete(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		if f.wrote() != 1 || f.sent[len(f.sent)-1].method != http.MethodDelete {
			t.Errorf("sent %+v", f.sent)
		}
		wantDeclared(t, "bucket delete", table)
		if table.Rows[0]["changed"] != true {
			t.Errorf("row %v", table.Rows[0])
		}
		if !strings.Contains(stderr.String(), "some minutes") {
			t.Errorf("no note about the spread:\n%s", stderr)
		}
	})

	t.Run("a harness is refused with both flags", func(t *testing.T) {
		f := &fakeBucketAPI{}
		req, _ := f.request(t, both, "uploads")
		req.CLI.Exec.AgentHarness = "an-agent"
		_, err := bucketDelete(context.Background(), req)
		wantCode(t, err, clierr.KindConfirmation, "confirmation.required")
		if f.wrote() != 0 {
			t.Errorf("sent %+v", f.sent)
		}
		if clierr.ExitCodeOf(err) != 4 {
			t.Errorf("exit %d, want 4", clierr.ExitCodeOf(err))
		}
	})

	t.Run("one flag is not enough", func(t *testing.T) {
		for _, flags := range []Flags{{}, {"yes": true}, {"confirm-name": "uploads"}} {
			f := &fakeBucketAPI{}
			req, _ := f.request(t, flags, "uploads")
			_, err := bucketDelete(context.Background(), req)
			wantCode(t, err, clierr.KindConfirmation, "confirmation.required")
			if f.wrote() != 0 {
				t.Errorf("%v sent %+v", flags, f.sent)
			}
		}
	})

	t.Run("a wrong name", func(t *testing.T) {
		f := &fakeBucketAPI{}
		req, _ := f.request(t, Flags{"yes": true, "confirm-name": "other"}, "uploads")
		_, err := bucketDelete(context.Background(), req)
		wantCode(t, err, clierr.KindUsage, "bucket.confirm_name_mismatch")
		if f.wrote() != 0 {
			t.Errorf("sent %+v", f.sent)
		}
	})

	t.Run("a dry run comes before confirmation", func(t *testing.T) {
		f := &fakeBucketAPI{}
		req, _ := f.request(t, Flags{}, "uploads")
		req.CLI.DryRun = true
		req.CLI.Exec.AgentHarness = "an-agent"
		table, err := bucketDelete(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		if f.wrote() != 0 || table.Rows[0]["changed"] != false {
			t.Errorf("sent %+v, row %v", f.sent, table.Rows[0])
		}
	})

	t.Run("the replay repeats a team from the flag", func(t *testing.T) {
		f := &fakeBucketAPI{}
		req, _ := f.request(t, Flags{}, "uploads")
		req.CLI.Config.TeamSlug = config.Value{Value: "acme", Source: config.SourceFlag}
		_, err := bucketDelete(context.Background(), req)
		e := wantCode(t, err, clierr.KindConfirmation, "confirmation.required")
		want := "outplane bucket delete uploads --yes --confirm-name uploads --team acme"
		if got := strings.Join(e.ConfirmCommand, " "); got != want {
			t.Errorf("replay %q, want %q", got, want)
		}
	})
}

func TestBucketKeyList(t *testing.T) {
	f := &fakeBucketAPI{keys: []string{keyJSON("AKEY2", "worker", 20), keyJSON("AKEY1", "web", 10), keyJSON("AKEY3", "odd", 30)}}
	req, _ := f.request(t, nil, "uploads")

	table, err := bucketKeyList(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	wantDeclared(t, "bucket key list", table)
	if table.Footer != "" {
		t.Errorf("footer %q on a bucket with keys", table.Footer)
	}

	got := map[string]string{}
	for _, row := range table.Rows {
		got[row["name"].(string)], _ = row["permission"].(string)
		if _, ok := row["secretAccessKey"]; ok {
			t.Errorf("a secret field in a list row: %v", row)
		}
		if row["bucket"] != "uploads" || row["bucketId"] != testBucketID {
			t.Errorf("row %v does not name its bucket", row)
		}
	}
	want := map[string]string{"web": "read-write", "worker": "read-only", "odd": "unknown:30"}
	for name, permission := range want {
		if got[name] != permission {
			t.Errorf("%s is %s, want %s", name, got[name], permission)
		}
	}

	empty := &fakeBucketAPI{}
	req, _ = empty.request(t, nil, "uploads")
	table, err = bucketKeyList(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(table.Footer, "outplane bucket key create uploads <KEY_NAME> --permission read-write") {
		t.Errorf("footer %q", table.Footer)
	}
}

func TestBucketKeyCreateSendsAnInteger(t *testing.T) {
	for permission, want := range map[string]string{"read-write": `"permission":10`, "read-only": `"permission":20`} {
		f := &fakeBucketAPI{}
		req, _ := f.request(t, Flags{"permission": permission}, "uploads", "web")
		table, err := bucketKeyCreate(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		if f.wrote() != 1 || !strings.Contains(f.sent[len(f.sent)-1].body, want) {
			t.Errorf("%s: sent %+v, want %s", permission, f.sent, want)
		}
		if table.Rows[0]["permission"] != permission {
			t.Errorf("%s: row %v", permission, table.Rows[0])
		}
	}
}

func TestBucketKeyCreateRefusesBeforeAnyRequest(t *testing.T) {
	cases := []struct {
		name  string
		args  []string
		flags Flags
		code  string
	}{
		{"no permission", []string{"uploads", "web"}, Flags{}, "usage.missing_argument"},
		{"blank permission", []string{"uploads", "web"}, Flags{"permission": " "}, "usage.missing_argument"},
		{"unknown permission", []string{"uploads", "web"}, Flags{"permission": "write"}, "bucket.permission_invalid"},
		{"no name", []string{"uploads"}, Flags{"permission": "read-only"}, "usage.missing_argument"},
		{"empty name", []string{"uploads", ""}, Flags{"permission": "read-only"}, "usage.empty_argument"},
		{"bad name", []string{"uploads", "_web"}, Flags{"permission": "read-only"}, "bucket.key_name_invalid"},
		{"empty bucket", []string{"", "web"}, Flags{"permission": "read-only"}, "usage.empty_argument"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeBucketAPI{}
			req, _ := f.request(t, tc.flags, tc.args...)
			_, err := bucketKeyCreate(context.Background(), req)
			wantCode(t, err, clierr.KindUsage, tc.code)
			if len(f.sent) != 0 {
				t.Errorf("sent %+v", f.sent)
			}
		})
	}
}

// A step repeats what the caller gave, so it can be run once what is missing is
// filled in, and it never picks a permission for them.
func TestBucketKeyCreateStepsKeepWhatWasGiven(t *testing.T) {
	cases := []struct {
		name  string
		args  []string
		flags Flags
		want  string
	}{
		{"no permission", []string{"uploads", "web"}, Flags{}, "outplane bucket key create uploads web --permission <PERMISSION>"},
		{"no name", []string{"uploads"}, Flags{"permission": "read-only"}, "outplane bucket key create uploads <KEY_NAME> --permission read-only"},
		{"nothing", nil, Flags{}, "outplane bucket key create <BUCKET_NAME> <KEY_NAME> --permission <PERMISSION>"},
		{"an empty bucket", []string{" ", "web"}, Flags{}, "outplane bucket key create <BUCKET_NAME> web --permission <PERMISSION>"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeBucketAPI{}
			req, _ := f.request(t, tc.flags, tc.args...)
			_, err := bucketKeyCreate(context.Background(), req)
			e := wantCode(t, err, clierr.KindUsage, "usage.missing_argument")
			if !hasStep(e, tc.want) {
				t.Errorf("steps %v, want %q", e.NextSteps, tc.want)
			}
		})
	}
}

// The secret reaches the result, in the columns a text table prints, whatever
// --quiet says, and it reaches nothing else.
func TestBucketKeyCreateSecret(t *testing.T) {
	f := &fakeBucketAPI{}
	req, stderr := f.request(t, Flags{"permission": "read-write"}, "uploads", "web")

	table, err := bucketKeyCreate(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	wantDeclared(t, "bucket key create", table)

	row := table.Rows[0]
	if row["secretAccessKey"] != testSecret || !slices.Contains(table.Columns, "secretAccessKey") {
		t.Errorf("row %v, columns %v, want the secret in both", row, table.Columns)
	}
	for _, key := range []string{"bucketName", "endpoint", "region"} {
		if row[key] == nil || !slices.Contains(table.Columns, key) {
			t.Errorf("%s is missing: one response has to be enough to connect", key)
		}
	}
	if row["changed"] != true || row["accessKeyId"] != "AKEYNEW" {
		t.Errorf("row %v", row)
	}
	if strings.Contains(stderr.String(), testSecret) {
		t.Errorf("the secret reached stderr:\n%s", stderr)
	}
	if !strings.Contains(stderr.String(), "once") {
		t.Errorf("no note that it is shown once:\n%s", stderr)
	}

	var stdout, errOut bytes.Buffer
	quiet := execctx.Context{RequestedFormat: execctx.FormatText, Quiet: true}
	if err := output.New(&stdout, &errOut, quiet).Result(table, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), testSecret) {
		t.Errorf("text mode under --quiet dropped the secret:\n%s", stdout.String())
	}
	if strings.Contains(errOut.String(), testSecret) {
		t.Errorf("the secret reached stderr:\n%s", errOut.String())
	}
}

func TestBucketKeyCreateDryRun(t *testing.T) {
	f := &fakeBucketAPI{}
	req, _ := f.request(t, Flags{"permission": "read-only"}, "uploads", "web")
	req.CLI.DryRun = true

	table, err := bucketKeyCreate(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if f.wrote() != 0 {
		t.Errorf("sent %+v", f.sent)
	}
	row := table.Rows[0]
	if value, ok := row["secretAccessKey"]; !ok || value != nil {
		t.Errorf("secret is %v, want null", value)
	}
	if value, ok := row["accessKeyId"]; !ok || value != nil {
		t.Errorf("id is %v, want null", value)
	}
	if row["changed"] != false || row["permission"] != "read-only" {
		t.Errorf("row %v", row)
	}
}

func TestBucketKeyCreateExplainsRefusals(t *testing.T) {
	refuse := func(status int, message string) error {
		f := &fakeBucketAPI{refusals: map[string]bucketRefusal{routeKeyCreate: {status, message}}}
		req, _ := f.request(t, Flags{"permission": "read-write"}, "uploads", "web")
		_, err := bucketKeyCreate(context.Background(), req)
		return err
	}

	err := refuse(http.StatusConflict, "This bucket name is not available.")
	e := wantCode(t, err, clierr.KindConflict, "bucket.key_conflict")
	if clierr.ExitCodeOf(err) != 6 {
		t.Errorf("exit %d, want 6", clierr.ExitCodeOf(err))
	}
	if strings.Contains(e.Message, "bucket name") || !strings.Contains(e.Message, "web") {
		t.Errorf("message %q, want it about the key name", e.Message)
	}
	if !hasStep(e, "outplane bucket key list uploads") {
		t.Errorf("steps %v", e.NextSteps)
	}

	wantCode(t, refuse(http.StatusNotFound, "not found"), clierr.KindNotFound, "bucket.not_found")

	e = wantCode(t, refuse(http.StatusPaymentRequired, "inactive"), clierr.KindQuota, "bucket.subscription_inactive")
	if strings.Contains(e.Hint, "Hobby") {
		t.Errorf("hint %q", e.Hint)
	}

	e = clierr.AsError(refuse(http.StatusInternalServerError, "boom"))
	if e.Kind != clierr.KindUpstream || !e.Retryable {
		t.Errorf("a 500 is %s, retryable %t; a real server failure stays retryable", e.Kind, e.Retryable)
	}
}

// A request whose answer never arrived may have made a key whose secret nobody
// received, so a retry is not offered, and the way to find out is.
func TestBucketKeyCreateWithoutAnAnswerIsNotRetryable(t *testing.T) {
	t.Run("a timeout", func(t *testing.T) {
		f := &fakeBucketAPI{stallKey: true}
		req, _ := f.request(t, Flags{"permission": "read-write"}, "uploads", "web")

		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()

		_, err := bucketKeyCreate(ctx, req)
		e := wantCode(t, err, clierr.KindTimeout, "")
		if e.Retryable {
			t.Error("retryable, and a retry makes another key")
		}
		if !hasStep(e, "outplane bucket key list uploads") {
			t.Errorf("steps %v", e.NextSteps)
		}
	})

	t.Run("a dropped connection", func(t *testing.T) {
		f := &fakeBucketAPI{dropKey: true}
		req, _ := f.request(t, Flags{"permission": "read-write"}, "uploads", "web")

		_, err := bucketKeyCreate(context.Background(), req)
		e := wantCode(t, err, clierr.KindUpstream, "")
		if e.Retryable {
			t.Errorf("%q is retryable, and a retry makes another key", e.Message)
		}
		if !hasStep(e, "outplane bucket key list uploads") {
			t.Errorf("steps %v", e.NextSteps)
		}
	})

	created := func(body string) func(http.ResponseWriter) {
		return func(w http.ResponseWriter) {
			w.WriteHeader(http.StatusCreated)
			fmt.Fprint(w, body)
		}
	}
	answers := []struct {
		name   string
		kind   clierr.Kind
		answer func(http.ResponseWriter)
	}{
		{"a proxy's plain text 502", clierr.KindUpstream, func(w http.ResponseWriter) {
			w.WriteHeader(http.StatusBadGateway)
			fmt.Fprint(w, "Bad Gateway")
		}},
		{"a proxy's empty 504", clierr.KindUpstream, func(w http.ResponseWriter) {
			w.WriteHeader(http.StatusGatewayTimeout)
		}},
		{"a proxy's page with a 200", clierr.KindUpstream, func(w http.ResponseWriter) {
			fmt.Fprint(w, "<html>Sign in</html>")
		}},
		{"a body cut off", clierr.KindUpstream, func(w http.ResponseWriter) {
			w.Header().Set("Content-Length", "4096")
			w.WriteHeader(http.StatusCreated)
			fmt.Fprint(w, `{"data":{"accessKeyId":"AKEYNEW"`)
		}},
		{"a success that does not decode", clierr.KindInternal,
			created(`{"data":{"accessKeyId":"AKEYNEW","name":"web","permission":"read-write","secretAccessKey":"` + testSecret + `"}}`)},
		{"a success with a null secret", clierr.KindUpstream,
			created(`{"data":{"accessKeyId":"AKEYNEW","name":"web","permission":10,"secretAccessKey":null}}`)},
		{"a success with null data", clierr.KindUpstream, created(`{"data":null}`)},
		{"a success with no body", clierr.KindUpstream, created("")},
	}
	for _, tc := range answers {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeBucketAPI{answerKey: tc.answer}
			req, _ := f.request(t, Flags{"permission": "read-write"}, "uploads", "web")

			table, err := bucketKeyCreate(context.Background(), req)
			if err == nil {
				t.Fatalf("no error, and got %+v", table)
			}
			e := wantCode(t, err, tc.kind, "")
			if e.Retryable {
				t.Errorf("%q is retryable, and a retry makes another key", e.Message)
			}
			if !strings.Contains(e.Hint, "Running this again makes another key") {
				t.Errorf("hint %q does not warn about the key", e.Hint)
			}
			if !hasStep(e, "outplane bucket key list uploads") {
				t.Errorf("steps %v", e.NextSteps)
			}
			if strings.Contains(e.Message, testSecret) {
				t.Errorf("the secret reached the message: %q", e.Message)
			}
		})
	}

	// Nothing was sent to a host that could not be reached, so no key can exist.
	t.Run("a host that could not be reached", func(t *testing.T) {
		server := httptest.NewServer(http.NotFoundHandler())
		client := api.New(api.Config{BaseURL: server.URL + "/api"})
		server.Close()
		_, err := core.CreateBucketKey(context.Background(), client, testBucketID, "web", "read-write")

		f := &fakeBucketAPI{}
		req, _ := f.request(t, nil)
		e := clierr.AsError(explainBucketKeyCreateRefusal(req, err, "web", core.Bucket{Name: "uploads"}))
		if !e.Retryable || hasStep(e, "outplane bucket key list uploads") {
			t.Errorf("%q is retryable %t with steps %v, and no key can exist", e.Message, e.Retryable, e.NextSteps)
		}
	})
}

func TestBucketKeyDelete(t *testing.T) {
	keys := []string{keyJSON("AKEY1", "web", 10), keyJSON("AKEY2", "worker", 20), keyJSON("AKEY3", "worker", 10), keyJSON("a/b", "slashed", 10)}
	flags := func(name string) Flags { return Flags{"yes": true, "confirm-name": name} }

	t.Run("by name", func(t *testing.T) {
		f := &fakeBucketAPI{keys: keys}
		req, stderr := f.request(t, flags("web"), "uploads", "web")
		table, err := bucketKeyDelete(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		if last := f.sent[len(f.sent)-1]; last.method != http.MethodDelete || !strings.HasSuffix(last.path, "/"+testBucketID+"/AKEY1") {
			t.Errorf("sent %+v", f.sent)
		}
		wantDeclared(t, "bucket key delete", table)
		if table.Rows[0]["changed"] != true || table.Rows[0]["accessKeyId"] != "AKEY1" {
			t.Errorf("row %v", table.Rows[0])
		}
		if !strings.Contains(stderr.String(), "15 minutes") {
			t.Errorf("no note about the delay:\n%s", stderr)
		}
	})

	t.Run("by id, confirmed with the name", func(t *testing.T) {
		f := &fakeBucketAPI{keys: keys}
		req, _ := f.request(t, flags("worker"), "uploads", "AKEY3")
		if _, err := bucketKeyDelete(context.Background(), req); err != nil {
			t.Fatal(err)
		}
		if last := f.sent[len(f.sent)-1]; !strings.HasSuffix(last.path, "/AKEY3") {
			t.Errorf("sent %+v", f.sent)
		}
	})

	t.Run("a tie sends no delete", func(t *testing.T) {
		f := &fakeBucketAPI{keys: keys}
		req, _ := f.request(t, flags("worker"), "uploads", "worker")
		_, err := bucketKeyDelete(context.Background(), req)
		e := wantCode(t, err, clierr.KindUsage, "bucket.key_ambiguous")
		if !strings.Contains(e.Hint, "AKEY2") || !strings.Contains(e.Hint, "AKEY3") {
			t.Errorf("hint %q does not list the ids", e.Hint)
		}
		if f.wrote() != 0 {
			t.Errorf("sent %+v", f.sent)
		}
	})

	t.Run("an id with a slash arrives escaped", func(t *testing.T) {
		f := &fakeBucketAPI{keys: keys}
		req, _ := f.request(t, flags("slashed"), "uploads", "a/b")
		if _, err := bucketKeyDelete(context.Background(), req); err != nil {
			t.Fatal(err)
		}
		if last := f.sent[len(f.sent)-1]; !strings.HasSuffix(last.path, "/"+testBucketID+"/a%2Fb") {
			t.Errorf("path %q, want it escaped", last.path)
		}
	})

	t.Run("none by that reference", func(t *testing.T) {
		f := &fakeBucketAPI{keys: keys}
		req, _ := f.request(t, flags("web"), "uploads", "wor")
		_, err := bucketKeyDelete(context.Background(), req)
		e := wantCode(t, err, clierr.KindNotFound, "bucket.key_not_found")
		if !strings.Contains(e.Hint, "web") || !hasStep(e, "outplane bucket key list uploads") {
			t.Errorf("hint %q, steps %v", e.Hint, e.NextSteps)
		}
	})

	t.Run("a harness is refused with both flags", func(t *testing.T) {
		f := &fakeBucketAPI{keys: keys}
		req, _ := f.request(t, flags("web"), "uploads", "web")
		req.CLI.Exec.AgentHarness = "an-agent"
		_, err := bucketKeyDelete(context.Background(), req)
		wantCode(t, err, clierr.KindConfirmation, "confirmation.required")
		if f.wrote() != 0 {
			t.Errorf("sent %+v", f.sent)
		}
	})

	t.Run("one flag is not enough", func(t *testing.T) {
		for _, given := range []Flags{{}, {"yes": true}, {"confirm-name": "web"}} {
			f := &fakeBucketAPI{keys: keys}
			req, _ := f.request(t, given, "uploads", "web")
			_, err := bucketKeyDelete(context.Background(), req)
			wantCode(t, err, clierr.KindConfirmation, "confirmation.required")
			if f.wrote() != 0 {
				t.Errorf("%v sent %+v", given, f.sent)
			}
		}
	})

	t.Run("a wrong name", func(t *testing.T) {
		f := &fakeBucketAPI{keys: keys}
		req, _ := f.request(t, flags("worker"), "uploads", "web")
		_, err := bucketKeyDelete(context.Background(), req)
		wantCode(t, err, clierr.KindUsage, "bucket.confirm_name_mismatch")
		if f.wrote() != 0 {
			t.Errorf("sent %+v", f.sent)
		}
	})

	t.Run("a dry run comes before confirmation", func(t *testing.T) {
		f := &fakeBucketAPI{keys: keys}
		req, _ := f.request(t, Flags{}, "uploads", "web")
		req.CLI.DryRun = true
		req.CLI.Exec.AgentHarness = "an-agent"
		table, err := bucketKeyDelete(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		if f.wrote() != 0 || table.Rows[0]["changed"] != false {
			t.Errorf("sent %+v, row %v", f.sent, table.Rows[0])
		}
	})

	t.Run("the replay names the key by id and repeats the team", func(t *testing.T) {
		f := &fakeBucketAPI{keys: keys}
		req, _ := f.request(t, Flags{}, "uploads", "web")
		req.CLI.Config.TeamSlug = config.Value{Value: "acme", Source: config.SourceFlag}
		_, err := bucketKeyDelete(context.Background(), req)
		e := wantCode(t, err, clierr.KindConfirmation, "confirmation.required")
		want := "outplane bucket key delete uploads AKEY1 --yes --confirm-name web --team acme"
		if got := strings.Join(e.ConfirmCommand, " "); got != want {
			t.Errorf("replay %q, want %q", got, want)
		}
	})

	t.Run("missing and empty arguments send nothing", func(t *testing.T) {
		f := &fakeBucketAPI{keys: keys}
		req, _ := f.request(t, Flags{}, "uploads")
		_, err := bucketKeyDelete(context.Background(), req)
		wantCode(t, err, clierr.KindUsage, "usage.missing_argument")

		req, _ = f.request(t, Flags{}, "uploads", " ")
		_, err = bucketKeyDelete(context.Background(), req)
		wantCode(t, err, clierr.KindUsage, "usage.empty_argument")

		req, _ = f.request(t, Flags{})
		_, err = bucketKeyDelete(context.Background(), req)
		wantCode(t, err, clierr.KindUsage, "usage.missing_argument")

		if len(f.sent) != 0 {
			t.Errorf("sent %+v", f.sent)
		}
	})

	// The server answers one way for a key that went and another for a bucket.
	for _, message := range []string{"Access key not found.", "TEntity  not found"} {
		t.Run("gone since it was resolved: "+message, func(t *testing.T) {
			f := &fakeBucketAPI{keys: keys, refusals: map[string]bucketRefusal{
				routeKeyDelete + "AKEY1": {http.StatusNotFound, message},
			}}
			req, _ := f.request(t, flags("web"), "uploads", "web")
			_, err := bucketKeyDelete(context.Background(), req)
			e := wantCode(t, err, clierr.KindNotFound, "bucket.key_not_found")
			if e.Message != "the key web or its bucket no longer exists" {
				t.Errorf("message %q", e.Message)
			}
			if e.Hint == "" || !hasStep(e, "outplane bucket key list uploads") || !hasStep(e, "outplane bucket list") {
				t.Errorf("hint %q, steps %v", e.Hint, e.NextSteps)
			}
		})
	}
}
