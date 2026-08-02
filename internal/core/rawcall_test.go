package core

import "testing"

// NewRawRequest is the only thing standing between a typed path and a request
// to somewhere nobody meant, so the shapes it accepts and rejects are pinned
// here rather than discovered against a live API.

func TestRawPath(t *testing.T) {
	cases := []struct {
		name  string
		path  string
		query []string
		want  string
	}{
		{"as written", "/App/GetAppsByTeamId", nil, "/App/GetAppsByTeamId"},
		{"a missing leading slash", "App/GetAppsByTeamId", nil, "/App/GetAppsByTeamId"},
		{"the api prefix copied from a browser", "/api/App/GetAppsByTeamId", nil, "/App/GetAppsByTeamId"},
		{"a path that is only /api-ish", "/apixyz/thing", nil, "/apixyz/thing"},
		{"a query in the path", "/Log/Search?q=error", nil, "/Log/Search?q=error"},
		{"a query in the flag", "/Log/Search", []string{"q=error"}, "/Log/Search?q=error"},
		{"both at once", "/Log/Search?q=error", []string{"limit=10"}, "/Log/Search?limit=10&q=error"},
		{"a value containing an equals sign", "/X", []string{"filter=a=b"}, "/X?filter=a%3Db"},
		{"an empty value", "/X", []string{"flag="}, "/X?flag="},
		{"a value needing encoding", "/X", []string{"q=a b&c"}, "/X?q=a+b%26c"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := NewRawRequest("GET", c.path, c.query, nil)
			if err != nil {
				t.Fatalf("refused: %v", err)
			}
			if got.Path != c.want {
				t.Errorf("got %q, want %q", got.Path, c.want)
			}
		})
	}
}

func TestNewRawRequestRejects(t *testing.T) {
	cases := []struct {
		name   string
		method string
		path   string
		query  []string
		body   []byte
	}{
		{"an unknown method", "FETCH", "/X", nil, nil},
		{"an empty method", "", "/X", nil, nil},
		{"an empty path", "GET", "", nil, nil},
		{"a full address", "GET", "https://example.com/X", nil, nil},
		{"a full address in any case", "GET", "HTTPS://example.com/X", nil, nil},
		{"a query parameter with no equals sign", "GET", "/X", []string{"broken"}, nil},
		{"a query parameter with no name", "GET", "/X", []string{"=value"}, nil},
		{"a body on a read", "GET", "/X", nil, []byte(`{"a":1}`)},
		{"a body that is not JSON", "POST", "/X", nil, []byte(`{"a":1,}`)},
		{"a body that is bare text", "POST", "/X", nil, []byte(`hello`)},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := NewRawRequest(c.method, c.path, c.query, c.body); err == nil {
				t.Fatal("accepted")
			}
		})
	}
}

func TestNewRawRequestAccepts(t *testing.T) {
	if _, err := NewRawRequest("post", "/X", nil, []byte(`{"a":1}`)); err != nil {
		t.Errorf("a lowercase method was refused: %v", err)
	}
	// A JSON document does not have to be an object, and the API has endpoints
	// that take a bare value.
	for _, body := range []string{`[1,2]`, `"text"`, `42`, `null`} {
		if _, err := NewRawRequest("POST", "/X", nil, []byte(body)); err != nil {
			t.Errorf("%s was refused: %v", body, err)
		}
	}
}

func TestReads(t *testing.T) {
	read, _ := NewRawRequest("GET", "/X", nil, nil)
	if !read.Reads() {
		t.Error("GET does not read")
	}
	for _, m := range []string{"POST", "PUT", "PATCH", "DELETE"} {
		r, _ := NewRawRequest(m, "/X", nil, nil)
		if r.Reads() {
			t.Errorf("%s reads", m)
		}
	}
}

func TestReadBody(t *testing.T) {
	file := func(name string) ([]byte, error) {
		if name == "body.json" {
			return []byte(`{"from":"file"}`), nil
		}
		return nil, errNotFound{}
	}
	stdin := func() ([]byte, error) { return []byte(`{"from":"stdin"}`), nil }

	cases := map[string]string{
		"":                "",
		`{"from":"argv"}`: `{"from":"argv"}`,
		"@body.json":      `{"from":"file"}`,
		"@-":              `{"from":"stdin"}`,
	}
	for in, want := range cases {
		got, err := ReadBody(in, file, stdin)
		if err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		if string(got) != want {
			t.Errorf("%q gave %q, want %q", in, got, want)
		}
	}

	if _, err := ReadBody("@missing.json", file, stdin); err == nil {
		t.Error("a missing file was accepted")
	}
}

type errNotFound struct{}

func (errNotFound) Error() string { return "no such file" }
