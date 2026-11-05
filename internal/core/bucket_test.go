package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/outplane/cli/internal/api"
	"github.com/outplane/cli/internal/clierr"
	"github.com/outplane/cli/internal/registry"
)

// bucketClient is an API client talking to a stand-in that answers every request
// with data, as the API's envelope.
func bucketClient(t *testing.T, data string) *api.Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{"data":%s,"statusCode":200,"isSuccessful":true}`, data)
	}))
	t.Cleanup(server.Close)
	return api.New(api.Config{BaseURL: server.URL + "/api", Token: "test-token"})
}

func codeOf(err error) string {
	if e := clierr.AsError(err); e != nil {
		return e.Code
	}
	return ""
}

func TestCheckBucketName(t *testing.T) {
	cases := []struct {
		name string
		code string
	}{
		{"abc", ""},
		{strings.Repeat("a", 38), ""},
		{"my-uploads-2", ""},
		{"", "usage.empty_argument"},
		{"   ", "usage.empty_argument"},
		{"ab", "bucket.name_invalid"},
		{strings.Repeat("a", 39), "bucket.name_invalid"},
		{"Uploads", "bucket.name_invalid"},
		{"-abc", "bucket.name_invalid"},
		{"abc-", "bucket.name_invalid"},
		{"a--b", "bucket.name_invalid"},
		{"my_uploads", "bucket.name_invalid"},
		{"abc\n", "bucket.name_invalid"},
	}
	for _, tc := range cases {
		if got := codeOf(CheckBucketName(tc.name)); got != tc.code {
			t.Errorf("%q: code %q, want %q", tc.name, got, tc.code)
		}
	}
}

func TestCheckBucketKeyName(t *testing.T) {
	cases := []struct {
		name string
		code string
	}{
		{"a", ""},
		{strings.Repeat("a", 100), ""},
		{"a_b-c", ""},
		{"Web1", ""},
		{"", "usage.empty_argument"},
		{" ", "usage.empty_argument"},
		{strings.Repeat("a", 101), "bucket.key_name_invalid"},
		{"_a", "bucket.key_name_invalid"},
		{"-a", "bucket.key_name_invalid"},
		{"a b", "bucket.key_name_invalid"},
	}
	for _, tc := range cases {
		if got := codeOf(CheckBucketKeyName(tc.name)); got != tc.code {
			t.Errorf("%q: code %q, want %q", tc.name, got, tc.code)
		}
	}
}

// A typo in a permission has to be refused rather than encoded as the first
// value, which would grant write access nobody asked for. An unknown value from
// the server is reported as it is.
func TestBucketPermissions(t *testing.T) {
	for permission, want := range map[string]int{"read-write": 10, "read-only": 20} {
		got, err := bucketPermissionValue(permission)
		if err != nil || got != want {
			t.Errorf("%s encodes as %d, %v; want %d", permission, got, err, want)
		}
		if err := CheckBucketPermission(permission); err != nil {
			t.Errorf("%s refused: %v", permission, err)
		}
	}

	for _, permission := range []string{"write", "", "READ-ONLY", "read_only"} {
		value, err := bucketPermissionValue(permission)
		if codeOf(err) != "bucket.permission_invalid" {
			t.Errorf("%q: got %v, want bucket.permission_invalid", permission, err)
		}
		if value != 0 {
			t.Errorf("%q encodes as %d, want nothing", permission, value)
		}
	}

	if got := decodeBucketKey(bucketKeyDTO{Permission: 30}).Permission; got != "unknown:30" {
		t.Errorf("30 decodes as %s, want unknown:30", got)
	}
}

// The wire names stop at the DTO: a command sees bucketName and public, and
// never the fields they came from.
func TestDecodeBucket(t *testing.T) {
	raw := `{"id":"00000000-0000-4000-8000-0000000000d1","name":"uploads",` +
		`"providerBucketName":"uploads-a1b2c3d4","isPublic":true,` +
		`"teamId":"00000000-0000-4000-8000-0000000000c1","endpoint":"https://bucket.example.test",` +
		`"region":"auto","url":"https://uploads-a1b2c3d4.bucket.example.test",` +
		`"createdDate":"2026-06-15T17:17:26"}`

	var d bucketDTO
	if err := json.Unmarshal([]byte(raw), &d); err != nil {
		t.Fatal(err)
	}

	got := decodeBucket(d)
	want := Bucket{
		ID:         "00000000-0000-4000-8000-0000000000d1",
		Name:       "uploads",
		BucketName: "uploads-a1b2c3d4",
		Public:     true,
		Endpoint:   "https://bucket.example.test",
		Region:     "auto",
		URL:        "https://uploads-a1b2c3d4.bucket.example.test",
		CreatedAt:  "2026-06-15T17:17:26Z",
	}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestFindBucket(t *testing.T) {
	const idLikeName = "00000000-0000-4000-8000-0000000000d2"
	data := fmt.Sprintf(`[{"id":"00000000-0000-4000-8000-0000000000d1","name":%q},`+
		`{"id":%q,"name":"reports"},{"id":"00000000-0000-4000-8000-0000000000d3","name":"uploads",`+
		`"providerBucketName":"uploads-a1b2c3d4"}]`,
		idLikeName, idLikeName)
	client := bucketClient(t, data)
	ctx := context.Background()

	// An id beats a name, even a name that looks like another bucket's id.
	byID, err := FindBucket(ctx, client, idLikeName)
	if err != nil || byID.Name != "reports" {
		t.Errorf("by id: got %+v, %v; want reports", byID, err)
	}

	byName, err := FindBucket(ctx, client, "uploads")
	if err != nil || byName.ID != "00000000-0000-4000-8000-0000000000d3" {
		t.Errorf("by name: got %+v, %v; want uploads", byName, err)
	}

	// A prefix is a near miss, and the command being resolved for might be a
	// deletion.
	for _, ref := range []string{"upload", "UPLOADS", "uploads-a1b2c3d4"} {
		_, err := FindBucket(ctx, client, ref)
		var notFound *BucketNotFoundError
		if !errors.As(err, &notFound) {
			t.Errorf("%s: got %v, want not found", ref, err)
			continue
		}
		if !slices.Contains(notFound.Available, "uploads") {
			t.Errorf("%s: available %v does not list uploads", ref, notFound.Available)
		}
	}
}

func TestFindBucketKey(t *testing.T) {
	keys := []BucketKey{
		{AccessKeyID: "AKEY1", Name: "web"},
		{AccessKeyID: "AKEY2", Name: "worker"},
		{AccessKeyID: "AKEY3", Name: "worker"},
		{AccessKeyID: "AKEY4", Name: "AKEY1"},
	}

	if got, err := FindBucketKey(keys, "AKEY3"); err != nil || got.AccessKeyID != "AKEY3" {
		t.Errorf("by id: got %+v, %v", got, err)
	}
	if got, err := FindBucketKey(keys, "AKEY1"); err != nil || got.AccessKeyID != "AKEY1" {
		t.Errorf("an id has to beat a name: got %+v, %v", got, err)
	}
	if got, err := FindBucketKey(keys, "web"); err != nil || got.AccessKeyID != "AKEY1" {
		t.Errorf("by name: got %+v, %v", got, err)
	}

	_, err := FindBucketKey(keys, "worker")
	var ambiguous *AmbiguousBucketKeyError
	if !errors.As(err, &ambiguous) {
		t.Fatalf("a tie: got %v, want ambiguous", err)
	}
	if strings.Join(ambiguous.IDs, ",") != "AKEY2,AKEY3" {
		t.Errorf("a tie lists %v, want both ids", ambiguous.IDs)
	}

	_, err = FindBucketKey(keys, "wor")
	var notFound *BucketKeyNotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("a prefix: got %v, want not found", err)
	}
	if !slices.Contains(notFound.Available, "web") {
		t.Errorf("available %v does not list web", notFound.Available)
	}
}

// The API lists keys in no particular order, so the sort is what makes two runs
// agree, even when two keys share a name.
func TestListBucketKeysSortsByNameThenID(t *testing.T) {
	client := bucketClient(t, `[{"accessKeyId":"AKEY3","name":"worker","permission":20},`+
		`{"accessKeyId":"AKEY2","name":"worker","permission":10},`+
		`{"accessKeyId":"AKEY1","name":"web","permission":10}]`)

	keys, err := ListBucketKeys(context.Background(), client, "00000000-0000-4000-8000-0000000000d1")
	if err != nil {
		t.Fatal(err)
	}

	ids := make([]string, 0, len(keys))
	for _, k := range keys {
		ids = append(ids, k.AccessKeyID)
	}
	if got := strings.Join(ids, ","); got != "AKEY1,AKEY2,AKEY3" {
		t.Errorf("order %s, want AKEY1,AKEY2,AKEY3", got)
	}
	if keys[0].Permission != "read-write" || keys[2].Permission != "read-only" {
		t.Errorf("permissions not decoded: %+v", keys)
	}
}

// The registry publishes the permissions as an enum and cannot import this
// package to read them. The same test holds the refusal of --fields on the
// command that returns a secret, which a typo in the declaration would lose.
func TestBucketKeyCreateMatchesTheRegistry(t *testing.T) {
	for _, c := range registry.Commands {
		if strings.Join(c.Path, " ") != "bucket key create" {
			continue
		}

		var enum []string
		for _, f := range c.Flags {
			if f.Name == "permission" {
				enum = f.Enum
			}
		}
		if got, want := strings.Join(enum, ","), strings.Join(BucketPermissions, ","); got != want {
			t.Errorf("--permission enum is %s, and the permissions are %s", got, want)
		}
		if !slices.Contains(c.SuppressGlobals, "fields") {
			t.Errorf("SuppressGlobals is %v, and narrowing the result can discard the secret", c.SuppressGlobals)
		}
		return
	}
	t.Fatal("bucket key create is not declared")
}
