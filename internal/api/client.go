// Package api is the only package that speaks HTTP to the Out Plane API.
//
// It exists to contain three pieces of knowledge that would otherwise leak
// into every command:
//
//  1. The response envelope. Every endpoint, success or failure, returns
//     {data, statusCode, message, isSuccessful, error:{errors[], isShow}}.
//     Commands should see a decoded result or an error, never that shape.
//
//  2. The X-Team-Id header. It is required by most endpoints and, critically,
//     is checked before any authentication logic runs. A missing header is a
//     400 even with a perfectly valid token, which is a confusing failure the
//     client should make impossible rather than explain.
//
//  3. The status code mapping, which is unusual on this API and gets
//     misdiagnosed constantly:
//     403 means authorisation failed. It is not 401.
//     402 means a Hobby team hit a plan limit.
//     429 means a Pro team hit a plan limit. It is NOT rate limiting.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/outplane/cli/internal/clierr"
)

// VersionHeader carries this release's version so the API can refuse releases
// it no longer serves. RecommendedHeader comes back on every response and names
// the version this release ought to be.
const (
	VersionHeader     = "X-Outplane-Cli-Version"
	RecommendedHeader = "X-Outplane-Cli-Recommended"
)

// userAgent identifies the client to the server.
//
// Beyond politeness, this is what makes it possible to ever retire an old
// release: without a version histogram there is no safe way to know who would
// break. The version is injected at build time.
func userAgent(version, osArch string) string {
	return fmt.Sprintf("outplane/%s (%s)", version, osArch)
}

// Client talks to the Out Plane API.
//
// It is created once per invocation and passed down. Nothing below this
// package constructs HTTP requests.
type Client struct {
	baseURL string
	token   string
	teamID  string
	version string
	osArch  string

	http *http.Client
}

// Config is what a Client needs to exist. A struct rather than positional
// arguments, because four strings in a row is a bug waiting to happen.
type Config struct {
	BaseURL string
	Token   string
	TeamID  string
	Version string
	OSArch  string
	Timeout time.Duration
}

// New creates a client.
func New(cfg Config) *Client {
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}
	return &Client{
		baseURL: strings.TrimRight(cfg.BaseURL, "/"),
		token:   cfg.Token,
		teamID:  cfg.TeamID,
		version: cfg.Version,
		osArch:  cfg.OSArch,
		http:    &http.Client{Timeout: cfg.Timeout},
	}
}

// envelope is the wire shape every endpoint returns.
//
// It is unexported on purpose: no caller outside this package should ever see
// it, so that the day the API adopts a different shape, exactly one file
// changes.
type envelope struct {
	Data         json.RawMessage `json:"data"`
	StatusCode   int             `json:"statusCode"`
	Message      string          `json:"message"`
	IsSuccessful bool            `json:"isSuccessful"`
	Error        *struct {
		Errors []string `json:"errors"`
		// isShow exists on the wire but is deliberately not read. It marks
		// only two exception types as displayable, which would mean discarding
		// the API's most useful messages ("Api token has been revoked.",
		// "Missing or invalid X-Team-Id header."). The HTTP status is the
		// reliable discriminator instead; see toError.
	} `json:"error"`
}

// Get performs a GET and decodes data into out.
func (c *Client) Get(ctx context.Context, path string, out any) error {
	return c.do(ctx, http.MethodGet, path, nil, out)
}

// Post performs a POST with a JSON body and decodes data into out.
// A nil body sends no body at all, which several endpoints expect.
func (c *Client) Post(ctx context.Context, path string, body, out any) error {
	return c.do(ctx, http.MethodPost, path, body, out)
}

// Put performs a PUT with a JSON body.
func (c *Client) Put(ctx context.Context, path string, body, out any) error {
	return c.do(ctx, http.MethodPut, path, body, out)
}

// Delete performs a DELETE.
func (c *Client) Delete(ctx context.Context, path string, out any) error {
	return c.do(ctx, http.MethodDelete, path, nil, out)
}

// GetAbsolute fetches a full URL and returns the body untouched.
//
// It exists for the log gateway, which is a different host and does not speak
// the API's response envelope: it answers with its own JSON, so the usual
// decode would look for a "data" field that is not there and report an empty
// result instead of a failure.
//
// The credential and team headers are the same ones the API gets, because the
// gateway authorises against the same token.
func (c *Client) GetAbsolute(ctx context.Context, rawURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, clierr.New(clierr.KindInternal, "could not build request: %v", err)
	}
	c.setHeaders(req, false)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, transportError(err, hostOf(rawURL))
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, clierr.New(clierr.KindUpstream, "could not read the response: %v", err)
	}

	if resp.StatusCode >= 400 {
		// No envelope to unwrap, so the status is all there is to go on. The
		// body is kept in details rather than printed: it is somebody else's
		// error format and may be an HTML page from a proxy.
		return nil, toHostError(resp.StatusCode, envelope{}, requestID(resp), hostOf(rawURL)).
			WithDetail("responseBody", truncate(string(raw), 500))
	}
	return raw, nil
}

// RawResponse is one arbitrary request's answer, kept in both shapes.
//
// Data is what every other command would have decoded, and Envelope is the
// whole reply including the fields this package normally hides. Both are here
// because the caller is `outplane api`, whose reason to exist is that the CLI
// does not know what the caller is looking for.
type RawResponse struct {
	Status    int
	Data      json.RawMessage
	Envelope  json.RawMessage
	RequestID string
}

// Raw performs a request this package knows nothing about.
//
// It is the escape hatch's transport, and it deliberately keeps everything the
// ordinary path provides: the credential, the team header, the version header,
// and the same status mapping, so that a 403 through here exits the way a 403
// through `app list` does. What it does not do is decode into a type, because
// there is no type to decode into.
func (c *Client) Raw(ctx context.Context, method, path string, body []byte) (RawResponse, error) {
	var reader io.Reader
	if len(body) > 0 {
		reader = bytes.NewReader(body)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return RawResponse{}, clierr.New(clierr.KindInternal, "could not build request: %v", err)
	}
	c.setHeaders(req, len(body) > 0)

	resp, err := c.http.Do(req)
	if err != nil {
		return RawResponse{}, transportError(err, "the Out Plane API")
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return RawResponse{}, clierr.New(clierr.KindUpstream, "could not read the response: %v", err)
	}

	out := RawResponse{Status: resp.StatusCode, RequestID: requestID(resp)}
	if len(raw) > 0 {
		out.Envelope = json.RawMessage(raw)
	}

	var env envelope
	if len(raw) > 0 && json.Unmarshal(raw, &env) == nil {
		out.Data = env.Data
	}

	if resp.StatusCode >= 400 {
		// Mapped like any other failure, so an agent reading exit codes does
		// not need a second rule for this command. The body travels in details
		// for the case where the caller was inspecting the error itself.
		return out, toError(resp.StatusCode, env, out.RequestID).
			WithDetail("responseBody", truncate(string(raw), 2000))
	}
	return out, nil
}

// Dial opens a WebSocket to the API.
//
// A socket gets everything a request gets: the same credential, the same team
// header, the same version header. The one thing it must not inherit is the
// client's timeout, which bounds a whole exchange and would cut a live session
// off after thirty seconds. Bounding the handshake alone is the transport's
// job, so this uses a client of its own.
//
// The credential travels in Authorization, as it does everywhere else in this
// package. The console cannot do that, because a browser may not set headers on
// a handshake, and sends a "bearer.<jwt>" subprotocol instead which the API also
// accepts. There is no reason for a CLI to put a token somewhere a proxy will
// log it as a protocol name.
func (c *Client) Dial(ctx context.Context, path string, query url.Values, subprotocol string) (*websocket.Conn, error) {
	u, err := url.Parse(c.baseURL + path)
	if err != nil {
		return nil, clierr.New(clierr.KindInternal, "could not build the socket address: %v", err)
	}
	// The library accepts http and https and reads them as ws and wss. Doing it
	// here anyway keeps the scheme honest in anything that quotes the address.
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	}
	u.RawQuery = query.Encode()

	header := http.Header{}
	c.headers(header, false)

	conn, resp, err := websocket.Dial(ctx, u.String(), &websocket.DialOptions{
		HTTPClient:   socketClient(),
		HTTPHeader:   header,
		Subprotocols: []string{subprotocol},
	})
	if err != nil {
		// A handshake that reached the server fails with a status, and the
		// status is the whole diagnosis: 403 for another team's application,
		// 404 for an instance that has restarted since it was listed. One that
		// never got a response is a network failure and reads as one.
		if resp == nil {
			return nil, transportError(err, "the Out Plane API")
		}
		return nil, toError(resp.StatusCode, socketEnvelope(resp), requestID(resp))
	}
	return conn, nil
}

// socketClient is an HTTP client with no overall deadline.
//
// Every timeout here bounds the handshake: connecting, TLS, and waiting for the
// response headers. After the upgrade there is no deadline at all, because a
// session ends when the caller's context does or when the far end closes, and
// an idle terminal is not a stalled one.
func socketClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           (&net.Dialer{Timeout: 15 * time.Second}).DialContext,
			TLSHandshakeTimeout:   15 * time.Second,
			ResponseHeaderTimeout: 30 * time.Second,
		},
	}
}

// socketEnvelope reads the error body of a refused handshake.
//
// Only the first kilobyte is readable, which the library documents and which is
// more than an envelope needs. A body that is not one leaves it empty, and the
// status code speaks for itself.
func socketEnvelope(resp *http.Response) envelope {
	var env envelope
	if raw, err := io.ReadAll(resp.Body); err == nil {
		_ = json.Unmarshal(raw, &env)
	}
	return env
}

// setHeaders applies the credential, the team and the version to a request.
//
// Shared by every path out of this package so that a header added for one
// cannot be forgotten on the others; the version header in particular decides
// whether the server gates the request at all.
func (c *Client) setHeaders(req *http.Request, hasBody bool) {
	c.headers(req.Header, hasBody)
}

// headers is setHeaders for anything that carries an http.Header without being
// an *http.Request. A WebSocket handshake is one.
func (c *Client) headers(h http.Header, hasBody bool) {
	h.Set("Accept", "application/json")
	h.Set("User-Agent", userAgent(c.version, c.osArch))
	h.Set(VersionHeader, c.version)
	if hasBody {
		h.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		h.Set("Authorization", "Bearer "+c.token)
	}
	// Sent on every request, not only where it is needed: the header is checked
	// before authentication, so omitting it produces a 400 that looks nothing
	// like the real problem.
	if c.teamID != "" {
		h.Set("X-Team-Id", c.teamID)
	}
}

// truncate keeps a foreign error body short enough to travel in a detail field.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return clierr.New(clierr.KindInternal, "could not encode request body: %v", err)
		}
		reader = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return clierr.New(clierr.KindInternal, "could not build request: %v", err)
	}

	c.setHeaders(req, body != nil)

	resp, err := c.http.Do(req)
	if err != nil {
		return transportError(err, "the Out Plane API")
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return clierr.New(clierr.KindUpstream, "could not read the response: %v", err)
	}

	var env envelope
	if len(raw) > 0 {
		// A body that is not our envelope is itself a signal: a proxy error
		// page, a 502 from an ingress, an HTML login redirect. Report it as
		// upstream rather than pretending to have parsed it.
		if err := json.Unmarshal(raw, &env); err != nil {
			return clierr.New(clierr.KindUpstream,
				"the server returned an unexpected response (HTTP %d)", resp.StatusCode).
				WithHint("This usually means something between the CLI and the API, such as a proxy, answered instead.")
		}
	}

	if resp.StatusCode >= 400 {
		return toError(resp.StatusCode, env, requestID(resp))
	}

	if out != nil && len(env.Data) > 0 && string(env.Data) != "null" {
		if err := json.Unmarshal(env.Data, out); err != nil {
			return clierr.New(clierr.KindInternal,
				"could not decode the response: %v", err).
				WithHint("The API may have changed. Try upgrading with: outplane update")
		}
	}
	return nil
}

// toError converts a failed response into a CLI error.
//
// The rule for whether to show the server's message is the HTTP status, not
// the envelope's isShow flag:
//
//   - 4xx messages come from typed exceptions with hand-written text. They are
//     specific and actionable, and hiding them would make the CLI worse than
//     curl.
//   - 500 comes from a catch-all that passes through any .NET exception
//     message, which can contain a connection string, a SQL fragment or an
//     internal path. Those are replaced with a generic sentence plus a request
//     id, and there is no flag to reveal them.
func toError(status int, env envelope, reqID string) *clierr.Error {
	return toHostError(status, env, reqID, "the Out Plane API")
}

// toHostError is toError for a response from a host that is not the API.
//
// Only the 5xx wording differs, and it matters: "the Out Plane API failed" sent
// a reader to check a service that was working while the log or metrics gateway
// was the one returning 500.
func toHostError(status int, env envelope, reqID string, host string) *clierr.Error {
	apiMessage := ""
	if env.Error != nil && len(env.Error.Errors) > 0 {
		apiMessage = strings.Join(env.Error.Errors, "; ")
	}

	switch {
	case status == http.StatusBadRequest:
		e := clierr.New(clierr.KindUsage, "%s", fallback(apiMessage, "the request was rejected"))
		// The single most common cause, and the least self-explanatory.
		if strings.Contains(apiMessage, "X-Team-Id") {
			return e.WithCode("context.no_team").
				WithHint("No team was resolved for this command.").
				WithStep("choose a team for this directory", "outplane", "link").
				WithStep("or name one for this invocation", "outplane", "app", "list", "--team", "<TEAM_SLUG>").
				WithRequestID(reqID)
		}
		return e.WithRequestID(reqID)

	case status == http.StatusUpgradeRequired:
		// This release is older than the API serves. Nothing about the request
		// or the credential is wrong, so the message is the server's own: it
		// names both the version sent and the version required, and repeating
		// that here would risk the two disagreeing.
		return clierr.New(clierr.KindUpgradeRequired,
			"%s", fallback(apiMessage, "this version of the CLI is no longer supported")).
			WithCode("client.upgrade_required").
			WithHint("The API stopped serving this release. Updating is the only fix; "+
				"retrying will not help.").
			WithStep("install the newest version", "outplane", "update").
			WithRequestID(reqID)

	case status == http.StatusUnauthorized:
		// The JWT middleware rejected the token before any controller saw it:
		// a bad signature, a malformed token, or one that has expired. There is
		// no envelope on this response, because nothing in the application ran.
		//
		// Both this and 403 have to be handled. An earlier version of this
		// function mapped only 403, on the belief that the API never returns
		// 401, and a token with a corrupted signature was consequently reported
		// as "could not reach the API": a wrong diagnosis pointing at the
		// network for a problem in the credential.
		return clierr.New(clierr.KindAuth, "the token was rejected").
			WithCode("auth.token_rejected").
			WithHint("It is expired, malformed, or was not issued by this API.").
			WithStep("check the current credential", "outplane", "whoami").
			WithStep("sign in again", "outplane", "login").
			WithRequestID(reqID)

	case status == http.StatusForbidden:
		// Authenticated, but not allowed. This API also uses 403 where others
		// would use 401, for a revoked token and for one belonging to another
		// team, so this branch cannot assume the caller is merely lacking a
		// permission.
		return clierr.New(clierr.KindAuth, "%s", fallback(apiMessage, "not authorised")).
			WithCode("auth.forbidden").
			WithHint("The token may be revoked, or belong to a different team.").
			WithStep("check the current credential", "outplane", "whoami").
			WithStep("sign in again", "outplane", "login").
			WithRequestID(reqID)

	case status == http.StatusNotFound:
		return clierr.New(clierr.KindNotFound, "%s", fallback(apiMessage, "not found")).
			WithRequestID(reqID)

	case status == http.StatusConflict:
		return clierr.New(clierr.KindConflict, "%s", fallback(apiMessage, "conflict")).
			WithRequestID(reqID)

	case status == http.StatusPaymentRequired:
		// A Hobby team over its plan limit.
		return clierr.New(clierr.KindQuota, "%s", fallback(apiMessage, "plan limit reached")).
			WithCode("quota.upgrade_required").
			WithHint("This team is on the Hobby plan and has reached its limit. Plans are "+
				"changed in the console; the CLI has no billing commands.").
			WithStep("see what this team is using", "outplane", "app", "list").
			WithRequestID(reqID)

	case status == http.StatusTooManyRequests:
		// A Pro team over its plan limit. Not rate limiting: this API has no
		// rate limiter. Retrying will never succeed, so Retryable stays false.
		return clierr.New(clierr.KindQuota, "%s", fallback(apiMessage, "plan limit reached")).
			WithCode("quota.limit_reached").
			WithHint("This is a plan limit, not rate limiting. Retrying will not help. "+
				"Limits are raised in the console; the CLI has no billing commands.").
			WithStep("see what this team is using", "outplane", "app", "list").
			WithRequestID(reqID)

	case status >= 500:
		// Deliberately generic. The server's message here is whatever
		// exception happened to escape, and it is not safe to print.
		e := clierr.New(clierr.KindUpstream,
			"%s failed to handle this request (HTTP %d)", host, status).
			WithHint("This is a problem on our side, not with the command.").
			WithRequestID(reqID)
		if apiMessage != "" {
			// Kept in details rather than shown: available to a bug report,
			// invisible on the terminal.
			e = e.WithDetail("apiMessage", apiMessage)
		}
		return e

	default:
		return clierr.New(clierr.KindUpstream,
			"%s", fallback(apiMessage, fmt.Sprintf("unexpected response (HTTP %d)", status))).
			WithRequestID(reqID)
	}
}

// transportError turns a network failure into something a user can act on.
// "dial tcp: i/o timeout" tells a developer plenty and a user nothing.
func transportError(err error, host string) *clierr.Error {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "context deadline exceeded"), strings.Contains(msg, "Client.Timeout"):
		return clierr.New(clierr.KindTimeout, "the request timed out").
			WithHint("The operation may still be running on the server.")
	case strings.Contains(msg, "no such host"), strings.Contains(msg, "connection refused"):
		return clierr.New(clierr.KindUpstream, "could not reach %s", host).
			WithHint("Check your network connection, or the address configured for it.").
			WithStep("see the resolved configuration", "outplane", "status")
	default:
		return clierr.New(clierr.KindUpstream, "request failed: %v", err)
	}
}

// hostOf names what could not be reached, so that an unreachable log or metrics
// gateway is not reported as the API being down.
//
// Three hosts answer this CLI and only one of them is the API. Blaming the API
// for the other two sends a reader to check a service that is working.
func hostOf(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return "the Out Plane API"
	}
	return u.Host
}

// requestID pulls the server's correlation id, trying the header names an
// ASP.NET Core service is likely to emit.
func requestID(resp *http.Response) string {
	for _, h := range []string{"X-Request-Id", "Request-Id", "TraceParent"} {
		if v := resp.Header.Get(h); v != "" {
			return v
		}
	}
	return ""
}

func fallback(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return s
}
