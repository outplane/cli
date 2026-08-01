package core

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/outplane/cli/internal/api"
)

// HTTP access logs: what reached an application from outside.
//
// These come from the same gateway as runtime logs and are a different thing
// from them. A runtime log is whatever the application chose to print; an
// access log is what the proxy in front of it recorded, whether or not the
// application said anything. An app that answers 502 without logging is
// invisible in one and obvious in the other.
//
// Two properties of the gateway shape everything below:
//
//   - The method, the status and the host are stream labels, so filtering on
//     them is a selector match and never reads a line. The console filters the
//     same fields after parsing, which is the same result by a slower route.
//   - Nothing labels the application. The proxy records a service name that
//     contains it, so the app filter is a match on that name, exactly as the
//     console does it.

// HTTPRequest is one request the proxy handled.
type HTTPRequest struct {
	At time.Time

	// App is which application served it, derived from the proxy's service
	// name. Empty when that name does not have the expected shape; Service
	// always carries the original.
	App string

	Method string
	Status int
	Path   string
	Host   string

	// LatencyMs is the whole request as the client experienced it. OriginMs is
	// the part the application itself took, so the difference is the proxy's
	// own overhead plus any retry.
	LatencyMs float64
	OriginMs  float64

	// OriginStatus is what the application answered, which is not always what
	// the client got: the proxy answers on its own when the app is unreachable.
	OriginStatus int

	Bytes    int64
	Protocol string
	Scheme   string

	// ClientIP is the caller as far as it can be known: the proxy's own view is
	// the last hop, so a forwarded address is preferred when one is present.
	ClientIP string
	Country  string

	Service string

	atNs string
}

func (r HTTPRequest) cursor() string { return r.atNs }

// RequestFilter narrows an access-log query. Every field is optional, and an
// empty filter means every request the team received.
type RequestFilter struct {
	// App is one application's name. It is matched against the proxy's service
	// name rather than a label, because there is no label for it.
	App string

	// Methods are upper-case verbs. Empty means all of them.
	Methods []string

	// Statuses are already-expanded patterns from ParseStatus: "404" matches
	// one code, "5.." matches a class.
	Statuses []string

	// Search is case-insensitive text the raw record must contain. It is
	// applied before parsing, so it reaches every field at once, including
	// ones this type does not model.
	Search string
}

// BuildRequestQuery assembles the selector and its filters.
//
// The order is deliberate and is what keeps a busy team's query cheap: label
// matchers first, because they select streams without reading anything; then
// the text filter, which reads lines but does not parse them; then the parse,
// which only the app filter needs.
func BuildRequestQuery(f RequestFilter) string {
	// request_host is on every access-log stream and on nothing else, so this
	// is "all HTTP traffic". The team is scoped by the gateway path, the same
	// way the console scopes it.
	matchers := []string{`request_host=~".+"`}
	if m := labelMatcher("request_method", f.Methods); m != "" {
		matchers = append(matchers, m)
	}
	if m := labelMatcher("downstream_status", f.Statuses); m != "" {
		matchers = append(matchers, m)
	}

	q := "{" + strings.Join(matchers, ",") + "}"

	if s := strings.TrimSpace(f.Search); s != "" {
		q += fmt.Sprintf(" |~ `(?i)%s`", quoteMeta(s))
	}

	if f.App != "" {
		// The service name is `{team}-{n}-{app}-{host}-{hash}@kubernetescrd`,
		// so the application appears between two dashes. Application names
		// cannot contain a dash, which is what makes this unambiguous.
		q += fmt.Sprintf(" | json | ServiceName=~`.*-%s-.*`", regexp.QuoteMeta(f.App))
	}

	return q
}

// labelMatcher builds one label match, exact for a single value and a regular
// expression for several. Values are already validated by their parsers.
func labelMatcher(label string, values []string) string {
	switch len(values) {
	case 0:
		return ""
	case 1:
		if strings.ContainsAny(values[0], ".|") {
			return fmt.Sprintf(`%s=~"%s"`, label, values[0])
		}
		return fmt.Sprintf(`%s="%s"`, label, values[0])
	default:
		return fmt.Sprintf(`%s=~"%s"`, label, strings.Join(values, "|"))
	}
}

// ParseStatus turns one status argument into a pattern.
//
// Both forms exist because both questions are real: "did anything fail" is a
// class, "where is that 404 coming from" is a code. A class becomes a pattern
// rather than an enumeration, so 418 is matched by 4xx without anybody having
// listed it.
func ParseStatus(s string) (string, error) {
	v := strings.ToLower(strings.TrimSpace(s))

	if len(v) == 3 && v[0] >= '1' && v[0] <= '5' {
		if v[1] == 'x' && v[2] == 'x' {
			return string(v[0]) + "..", nil
		}
		if isDigit(v[1]) && isDigit(v[2]) {
			return v, nil
		}
	}
	return "", fmt.Errorf("%q is not a status, expected a class like 5xx or a code like 404", s)
}

// ParseMethod normalises one method argument.
//
// It is restricted to letters because the value reaches the gateway inside a
// regular expression, where a stray character is either a syntax error or, in
// the case of a dot, a filter that silently matches more than it was asked to.
func ParseMethod(s string) (string, error) {
	v := strings.ToUpper(strings.TrimSpace(s))
	if v == "" || !isLetters(v) {
		return "", fmt.Errorf("%q is not an HTTP method", s)
	}
	return v, nil
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }

func isLetters(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 'A' || s[i] > 'Z' {
			return false
		}
	}
	return true
}

// accessLogDTO is the proxy's record, of which the CLI reports a part.
//
// The names are the proxy's own, capitalised, and the header fields arrive
// prefixed. Field names here are the wire's; the mapping to something readable
// happens once, below.
type accessLogDTO struct {
	RequestMethod   string  `json:"RequestMethod"`
	RequestHost     string  `json:"RequestHost"`
	RequestPath     string  `json:"RequestPath"`
	RequestProtocol string  `json:"RequestProtocol"`
	RequestScheme   string  `json:"RequestScheme"`
	DownstreamCode  int     `json:"DownstreamStatus"`
	OriginStatus    int     `json:"OriginStatus"`
	Duration        float64 `json:"Duration"`
	OriginDuration  float64 `json:"OriginDuration"`
	ContentSize     int64   `json:"DownstreamContentSize"`
	ServiceName     string  `json:"ServiceName"`
	ClientHost      string  `json:"ClientHost"`
	RealIP          string  `json:"request_X-Real-Ip"`
	ForwardedIP     string  `json:"request_Cf-Connecting-Ip"`
	Country         string  `json:"request_Cf-Ipcountry"`
}

// QueryRequests fetches the access log.
//
// A record that does not parse is skipped rather than reported. The selector
// admits only access-log streams, so anything unparseable is a line the proxy
// wrote in a shape this release does not know; dropping one is better than
// failing the whole query, and better than emitting a row of zeroes that reads
// like a real request that took no time and returned no status.
func QueryRequests(ctx context.Context, c *api.Client, base, teamSlug, query string, w LogWindow) ([]HTTPRequest, error) {
	entries, err := queryRange(ctx, c, base, teamSlug, query, w)
	if err != nil {
		return nil, err
	}

	out := make([]HTTPRequest, 0, len(entries))
	for _, e := range entries {
		var d accessLogDTO
		if err := json.Unmarshal([]byte(e.line), &d); err != nil {
			continue
		}
		if d.RequestMethod == "" {
			continue
		}

		out = append(out, HTTPRequest{
			At:           nsToTime(e.atNs),
			atNs:         e.atNs,
			App:          appFromService(teamSlug, d.ServiceName),
			Method:       d.RequestMethod,
			Status:       d.DownstreamCode,
			Path:         d.RequestPath,
			Host:         d.RequestHost,
			LatencyMs:    nsToMs(d.Duration),
			OriginMs:     nsToMs(d.OriginDuration),
			OriginStatus: d.OriginStatus,
			Bytes:        d.ContentSize,
			Protocol:     d.RequestProtocol,
			Scheme:       d.RequestScheme,
			ClientIP:     firstNonEmpty(d.RealIP, d.ForwardedIP, d.ClientHost),
			Country:      d.Country,
			Service:      d.ServiceName,
		})
	}
	return out, nil
}

// appFromService recovers the application name from the proxy's service name.
//
// The shape is `{team}-{n}-{app}-{host}-{hash}@kubernetescrd`, where the host
// has had its dots turned into dashes. The team is stripped by name because it
// may contain dashes itself; what follows is a number, and after that is the
// application.
//
// An unrecognised shape returns nothing rather than a guess. The service name
// is reported alongside, so a reader still sees where the request went, and the
// day the proxy's naming changes this goes quiet instead of confidently
// attributing requests to the wrong application.
func appFromService(teamSlug, service string) string {
	rest, ok := strings.CutPrefix(service, teamSlug+"-")
	if !ok {
		return ""
	}

	parts := strings.Split(rest, "-")
	if len(parts) < 2 || !isDigits(parts[0]) {
		return ""
	}
	return parts[1]
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !isDigit(s[i]) {
			return false
		}
	}
	return true
}

// nsToMs converts the proxy's nanoseconds into the milliseconds people quote
// latency in.
func nsToMs(ns float64) float64 { return ns / 1e6 }

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
