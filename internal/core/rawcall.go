package core

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/outplane/cli/internal/api"
	"github.com/outplane/cli/internal/clierr"
)

// The escape hatch's rules.
//
// Everything here is about what a request has to look like before it is worth
// sending, because this is the one command with no idea what it is asking for.
// It cannot check that an endpoint exists, that a body has the right fields or
// that a method is allowed; the server answers all three. What it can do is
// refuse the mistakes that would otherwise come back as a confusing 400 or, far
// worse, as a request to somewhere nobody meant.

// Methods are what this command will send. Anything else is a typo: the API
// serves no other verb, and sending one produces a 405 that reads like a
// missing endpoint.
var Methods = []string{"GET", "POST", "PUT", "PATCH", "DELETE"}

// ReadMethods change nothing on the server, which is what decides whether the
// invocation needs acknowledging.
var ReadMethods = []string{"GET", "HEAD"}

// RawRequest is one arbitrary call, already checked.
type RawRequest struct {
	Method string
	Path   string
	Body   []byte
}

// Reads reports a request that cannot change anything.
func (r RawRequest) Reads() bool { return contains(ReadMethods, r.Method) }

// NewRawRequest turns what somebody typed into a request, or explains why it is
// not one.
//
// Query parameters arrive separately from the path because a shell eats an
// ampersand: `--query a=1 --query b=2` needs no quoting, and a path that
// already carries a query string still works. The two are merged rather than
// one winning, so neither form is a trap.
func NewRawRequest(method, path string, query []string, body []byte) (RawRequest, error) {
	m := strings.ToUpper(strings.TrimSpace(method))
	if !contains(Methods, m) {
		return RawRequest{}, usage(fmt.Sprintf("%q is not an HTTP method", method),
			"api.method_invalid", "Use one of: %s.", strings.Join(Methods, ", "))
	}

	p, err := rawPath(path, query)
	if err != nil {
		return RawRequest{}, err
	}

	if len(body) > 0 && contains(ReadMethods, m) {
		return RawRequest{}, usage("a GET sends no body", "api.body_not_allowed",
			"The API reads nothing from the body of a read, so --data would be discarded.")
	}
	if len(body) > 0 && !json.Valid(body) {
		return RawRequest{}, usage("the body is not JSON", "api.body_invalid",
			"The API accepts JSON only. Check for a trailing comma or a missing quote.")
	}

	return RawRequest{Method: m, Path: p, Body: body}, nil
}

// rawPath normalises the path and folds in the query parameters.
func rawPath(path string, query []string) (string, error) {
	p := strings.TrimSpace(path)
	switch {
	case p == "":
		return "", usage("no path given", "api.path_required",
			"A path is the part after /api, such as /App/GetAppsByTeamId.")
	case strings.HasPrefix(strings.ToLower(p), "http://"),
		strings.HasPrefix(strings.ToLower(p), "https://"):
		return "", usage("give a path, not a full address", "api.path_invalid",
			"The host comes from the configured API address, so this command cannot "+
				"be pointed somewhere else. Change --api-url to move it.")
	}

	// A leading slash is optional, because half the world writes one and the
	// other half does not, and both mean the same endpoint.
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}

	// /api is the base address's own suffix. Somebody copying a path out of the
	// browser's network tab brings it along, and sending it twice produces a
	// 404 that says nothing about the duplication.
	if strings.HasPrefix(p, "/api/") {
		p = strings.TrimPrefix(p, "/api")
	}

	base, existing, _ := strings.Cut(p, "?")
	values, err := url.ParseQuery(existing)
	if err != nil {
		return "", usage(fmt.Sprintf("the query string in %q cannot be read", path),
			"api.path_invalid", "")
	}

	for _, q := range query {
		key, value, ok := strings.Cut(q, "=")
		if !ok || strings.TrimSpace(key) == "" {
			return "", usage(fmt.Sprintf("%q is not a query parameter", q),
				"api.query_invalid", "Write it as key=value.")
		}
		values.Add(key, value)
	}

	if len(values) == 0 {
		return base, nil
	}
	return base + "?" + values.Encode(), nil
}

// Call sends the request and returns what came back.
func Call(ctx context.Context, c *api.Client, r RawRequest) (api.RawResponse, error) {
	return c.Raw(ctx, r.Method, r.Path, r.Body)
}

// ReadBody reads --data, which may be a literal, a file or standard input.
//
// @- is standard input, which is how a body larger than a command line arrives
// and the only form an agent can use without writing a temporary file. A
// literal that starts with @ has to become a file path, because a JSON document
// never starts with one.
func ReadBody(value string, read func(string) ([]byte, error), stdin func() ([]byte, error)) ([]byte, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil, nil
	}

	if !strings.HasPrefix(trimmed, "@") {
		return []byte(value), nil
	}
	if trimmed == "@-" {
		body, err := stdin()
		if err != nil {
			return nil, clierr.New(clierr.KindUsage, "could not read the body: %v", err).
				WithCode("api.body_unreadable")
		}
		return body, nil
	}

	path := strings.TrimPrefix(trimmed, "@")
	body, err := read(path)
	if err != nil {
		return nil, clierr.New(clierr.KindUsage, "could not read %s: %v", path, err).
			WithCode("api.body_unreadable")
	}
	return body, nil
}
