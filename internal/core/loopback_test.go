package core

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// The listener is the one place in this CLI that accepts something from
// outside without a credential, so what it refuses matters as much as what it
// takes. Every case below is a request that could arrive from a page the user
// happened to have open.

// testOrigin stands in for the console. Every request below carries it, since
// a request without it is one the listener is right to ignore.
const testOrigin = "https://console.example.com"

func listening(t *testing.T) *Loopback {
	t.Helper()
	l, err := Listen(testOrigin)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return l
}

// deliver submits the form the console submits, from the origin it comes from.
func deliver(t *testing.T, target, token, state string) int {
	t.Helper()
	return submit(t, target, token, state, testOrigin)
}

func submit(t *testing.T, target, token, state, origin string) int {
	t.Helper()

	form := url.Values{}
	if token != "" {
		form.Set("token", token)
	}
	if state != "" {
		form.Set("state", state)
	}

	req, err := http.NewRequest(http.MethodPost, target, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if origin != "" {
		req.Header.Set("Origin", origin)
	}

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer res.Body.Close()
	return res.StatusCode
}

// A form arriving from anywhere but the console is refused. Browsers send the
// origin on a cross-site navigation, so when it is there it is worth reading.
func TestAnotherOriginIsRefused(t *testing.T) {
	l := listening(t)

	code := submit(t, l.CallbackURL(), "a-token", l.State(), "https://not-the-console.example")
	if code != http.StatusForbidden {
		t.Errorf("got %d, want 403", code)
	}
}

// Not every browser sends one on a navigation, and the state is what decides.
func TestNoOriginStillWorks(t *testing.T) {
	l := listening(t)

	if code := submit(t, l.CallbackURL(), "a-token", l.State(), ""); code != http.StatusOK {
		t.Errorf("got %d, want 200", code)
	}
}

func TestDelivery(t *testing.T) {
	l := listening(t)

	if code := deliver(t, l.CallbackURL(), "a-token", l.State()); code != http.StatusOK {
		t.Fatalf("got %d, want 200", code)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	token, err := l.Wait(ctx)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if token != "a-token" {
		t.Errorf("got %q", token)
	}
}

func TestRefusals(t *testing.T) {
	l := listening(t)

	cases := []struct {
		name  string
		token string
		state string
		want  int
	}{
		{"a state from somewhere else", "a-token", "not-the-one", http.StatusForbidden},
		{"no state at all", "a-token", "", http.StatusForbidden},
		{"a delivery with no token", "", l.State(), http.StatusBadRequest},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if code := deliver(t, l.CallbackURL(), c.token, c.state); code != c.want {
				t.Errorf("got %d, want %d", code, c.want)
			}
		})
	}

	// Nothing above should have reached the waiting side.
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if _, err := l.Wait(ctx); err == nil {
		t.Error("a refused delivery was handed to the caller anyway")
	}
}

// Only the callback path, and only POST. A token in a URL would be in the
// browser's history; a GET here would invite exactly that.
func TestOnlyPostToTheCallbackPath(t *testing.T) {
	l := listening(t)

	res, err := http.Get(l.CallbackURL())
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("GET gave %d, want 404", res.StatusCode)
	}

	elsewhere := strings.Replace(l.CallbackURL(), "/callback", "/", 1)
	if code := deliver(t, elsewhere, "x", l.State()); code != http.StatusNotFound {
		t.Errorf("POST to / gave %d, want 404", code)
	}
}

// The first delivery is the one this process asked for. A second cannot
// replace it, and cannot block the handler either.
func TestOnlyTheFirstDeliveryCounts(t *testing.T) {
	l := listening(t)

	for _, token := range []string{"first", "second"} {
		if code := deliver(t, l.CallbackURL(), token, l.State()); code != http.StatusOK {
			t.Fatalf("%s gave %d", token, code)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	token, err := l.Wait(ctx)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if token != "first" {
		t.Errorf("got %q, want the first delivery", token)
	}
}

func TestStateIsUnpredictable(t *testing.T) {
	first, second := listening(t), listening(t)

	if first.State() == second.State() {
		t.Fatal("two listeners produced the same state")
	}
	if len(first.State()) < 32 {
		t.Errorf("the state is %d characters, which is not enough to be worth guessing at",
			len(first.State()))
	}
	if first.CallbackURL() == second.CallbackURL() {
		t.Error("two listeners took the same port")
	}
	if !strings.HasPrefix(first.CallbackURL(), "http://127.0.0.1:") {
		t.Errorf("the callback is %q, which is not loopback", first.CallbackURL())
	}
}

// An interrupt while waiting ends the wait rather than holding the terminal.
func TestWaitStopsWhenTheCallerDoes(t *testing.T) {
	l := listening(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := l.Wait(ctx); err == nil {
		t.Fatal("a cancelled wait returned a token")
	}
}

// A downgrade from the https console to this http listener makes the browser
// send `Origin: null` under the default referrer policy, which is what Fetch
// specifies and what Chrome does. Refusing it refused the whole flow: the
// console navigated to the listener, the listener answered 403, and the page
// the person was left looking at was empty.
func TestNullOriginIsAccepted(t *testing.T) {
	l := listening(t)

	if got := submit(t, l.CallbackURL(), "tok_test", l.State(), "null"); got != http.StatusOK {
		t.Fatalf("a null origin was refused with %d; it is what a downgrade sends", got)
	}
	if token, err := l.Wait(context.Background()); err != nil || token != "tok_test" {
		t.Fatalf("Wait = (%q, %v), want the delivered token", token, err)
	}
}

// An absent origin has always been allowed, and still is: the state decides.
func TestAbsentOriginStillDefersToTheState(t *testing.T) {
	l := listening(t)

	if got := submit(t, l.CallbackURL(), "tok_test", "not-the-state", ""); got != http.StatusForbidden {
		t.Fatalf("a wrong state with no origin got %d, want 403", got)
	}
	if got := submit(t, l.CallbackURL(), "tok_test", l.State(), ""); got != http.StatusOK {
		t.Fatalf("the right state with no origin got %d, want 200", got)
	}
}
