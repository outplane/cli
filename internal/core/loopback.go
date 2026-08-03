package core

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/outplane/cli/internal/clierr"
)

// Signing in without anybody copying a token.
//
// The CLI opens a listener on a loopback port and sends the browser to the
// console with the address and a one-time value. The console creates the token,
// as it always did, and posts it back here. The token is never on screen, never
// in a clipboard and never in a shell's history, which is the whole reason this
// exists: every one of those is a place a secret gets left behind.
//
// The console delivers by submitting a form, not by calling fetch, and that is
// not a style choice. Safari refuses outright to fetch http://127.0.0.1 from an
// https page: "Not allowed to request resource", before a byte leaves the
// browser, with no header on this side able to change it. A form submission is
// a navigation rather than a subresource request, so none of that applies, and
// the token still travels in the body rather than in a URL somebody's history
// would keep.
//
// What makes it safe is small enough to state completely:
//
//   - The listener binds 127.0.0.1 and nothing else, so nothing off this
//     machine can reach it.
//   - It accepts one delivery and then stops. A second has nowhere to go.
//   - The state has to come back. It is the only thing tying a delivery to the
//     browser this process sent, and without it any page a user happened to
//     open could post a token of its choosing to a port it guessed.
//   - When the browser says where the form came from, it has to be the console.
//     Not every browser sends it on a navigation, so this narrows rather than
//     decides; the state is what decides.
//
// The state is compared in constant time. The comparison is not a secret-keeping
// operation in the usual sense, but the cost of getting it right is one function
// call and the cost of reasoning about whether it matters is larger.

// loopbackTimeout is how long the browser has to answer.
//
// Long enough to sign in to the console first, create the token and read what
// is being approved. Short enough that a forgotten terminal does not hold a
// port open all afternoon.
const loopbackTimeout = 3 * time.Minute

// Loopback is a listener waiting for one token.
type Loopback struct {
	listener net.Listener
	state    string
	origin   string
	tokens   chan string
	server   *http.Server
}

// Listen opens a loopback port and starts waiting.
//
// The port is chosen by the operating system. A fixed one would collide with a
// second CLI signing in at the same time, and worse, would be a port anything
// on the machine could squat on before this process asked for it.
// Listen opens the port, allowing deliveries from one origin.
//
// origin is the console this CLI is configured against. It is the only page
// allowed to answer, which matters more here than it looks: a browser will let
// any page it has open reach a loopback port, so the origin check is what keeps
// the listener talking to the console alone.
func Listen(origin string) (*Loopback, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, clierr.New(clierr.KindInternal,
			"could not open a port for the browser to answer on: %v", err).
			WithCode("auth.loopback_unavailable").
			WithHint("Signing in this way needs a local port. Use --no-browser to paste " +
				"a token instead.")
	}

	state, err := randomState()
	if err != nil {
		listener.Close()
		return nil, err
	}

	l := &Loopback{
		listener: listener,
		state:    state,
		origin:   origin,
		// Buffered, so the handler can hand the token over and answer the
		// browser even if the waiting side has already given up.
		tokens: make(chan string, 1),
	}

	l.server = &http.Server{
		Handler:           http.HandlerFunc(l.handle),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go l.server.Serve(listener)

	return l, nil
}

// CallbackURL is where the console posts the token.
func (l *Loopback) CallbackURL() string {
	return fmt.Sprintf("http://127.0.0.1:%d/callback", l.listener.Addr().(*net.TCPAddr).Port)
}

// State is the value the console has to send back.
func (l *Loopback) State() string { return l.state }

// Wait blocks until the browser delivers a token, the caller interrupts, or the
// wait runs out.
//
// A timeout is not a failure of the flow; it usually means the browser opened
// somewhere the user did not see. The caller falls back to asking for a paste,
// so this reports plainly rather than dramatically.
func (l *Loopback) Wait(ctx context.Context) (string, error) {
	select {
	case token := <-l.tokens:
		return token, nil
	case <-ctx.Done():
		return "", clierr.New(clierr.KindInterrupted, "interrupted")
	case <-time.After(loopbackTimeout):
		return "", clierr.New(clierr.KindTimeout, "the browser did not answer").
			WithCode("auth.loopback_timeout")
	}
}

// Close stops listening. Safe to call more than once.
func (l *Loopback) Close() error {
	shutdown, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	return l.server.Shutdown(shutdown)
}

// handle takes the one delivery this listener exists for.
//
// Everything it refuses, it refuses quietly: a browser is not the audience for
// a diagnostic, and a message describing why a state did not match is a message
// telling somebody what to send next time.
func (l *Loopback) handle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.Path != "/callback" {
		http.NotFound(w, r)
		return
	}

	// A form submission, so the body is form-encoded. It is capped because
	// nothing reaching this port is trusted and a token is small.
	r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "", http.StatusBadRequest)
		return
	}

	token, state := r.PostFormValue("token"), r.PostFormValue("state")
	if token == "" {
		http.Error(w, "", http.StatusBadRequest)
		return
	}

	// Not every browser sends a usable origin on a navigation, so this narrows
	// rather than decides: a wrong one is refused, an absent one leaves the
	// state to do the deciding.
	//
	// "null" counts as absent, and getting that wrong broke the flow in Chrome
	// entirely. The console is https and this listener is http, so the request
	// is a downgrade, and Fetch says a downgrade under the default referrer
	// policy sends `Origin: null` rather than the real origin. That is the
	// specified behaviour for exactly the flow this listener exists for, so
	// treating it as a hostile origin refused the only browser doing the right
	// thing.
	if !l.originAllowed(r.Header.Get("Origin")) {
		http.Error(w, "", http.StatusForbidden)
		return
	}
	if subtle.ConstantTimeCompare([]byte(state), []byte(l.state)) != 1 {
		http.Error(w, "", http.StatusForbidden)
		return
	}

	select {
	case l.tokens <- token:
	default:
		// Already delivered. The first one is the one this process asked for.
	}

	// The browser is looking at this page rather than at the console, which
	// navigated here, so this is the confirmation somebody actually reads.
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, signedInPage)
}

// originAllowed reports whether a delivery may proceed on its origin alone.
//
// Absent, and the literal "null", both mean the browser declined to name one.
// Neither is evidence of anything, so both defer to the state, which is the
// check that actually ties a delivery to this process. "null" is what a browser
// sends when the request downgrades from the https console to this http
// listener, which is every successful sign-in, so refusing it refused the flow.
//
// Compared as text because the header is already a serialised origin: scheme,
// host, and a port when there is one. A browser does not send a trailing slash
// or a second spelling, and there is exactly one console to compare against.
func (l *Loopback) originAllowed(origin string) bool {
	return origin == "" || origin == "null" || origin == l.origin
}

// signedInPage is what the browser shows afterwards.
//
// The form submission navigates here, so this is the confirmation the person
// reads, not a fallback. It stays one sentence: the terminal they started from
// is already saying the same thing, and this tab has nothing left to do.
const signedInPage = `<!doctype html>
<meta charset="utf-8">
<title>Signed in</title>
<style>
  body { font: 16px/1.5 system-ui, sans-serif; margin: 0; display: grid;
         place-items: center; min-height: 100vh; color: #111 }
  p { margin: 0 }
  small { color: #666 }
  @media (prefers-color-scheme: dark) {
    body { background: #111; color: #eee } small { color: #999 }
  }
</style>
<div>
  <p>Signed in.</p>
  <p><small>You can close this tab and go back to your terminal.</small></p>
</div>`

// randomState produces the value the console has to echo back.
func randomState() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", clierr.New(clierr.KindInternal,
			"could not generate a one-time value: %v", err)
	}
	return hex.EncodeToString(raw), nil
}
