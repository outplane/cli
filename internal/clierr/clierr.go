// Package clierr defines the CLI's error type.
//
// Every failure the user or an agent sees is one of these. That matters more
// than it might sound: an agent cannot branch on prose, so an error has to
// carry a stable machine-readable class, a stable code, and a suggested next
// action, in addition to a sentence a human can read.
//
// The contract, which the documentation states loudly:
//
//	branch on Kind or Code, never on Message.
//
// Kind and Code are frozen once shipped and only ever added to. Message is free
// to change with every release.
//
// This package deliberately depends on nothing. Both the API client and the
// commands construct these, and neither should have to import the other.
package clierr

import (
	"errors"
	"fmt"
)

// Kind is the coarse error class. The set is closed and small enough that a
// caller who knows nothing about Out Plane can handle it exhaustively.
//
// Each Kind maps to exactly one exit code, so a shell script can branch on the
// exit status alone without parsing anything.
type Kind string

const (
	KindUsage        Kind = "usage"                 // exit 2
	KindAuth         Kind = "auth"                  // exit 3
	KindConfirmation Kind = "confirmation_required" // exit 4
	KindNotFound     Kind = "not_found"             // exit 5
	KindConflict     Kind = "conflict"              // exit 6
	KindQuota        Kind = "quota"                 // exit 7
	KindUpstream     Kind = "upstream"              // exit 8
	KindTimeout      Kind = "timeout"               // exit 124
	KindInterrupted  Kind = "interrupted"           // exit 130
	KindInternal     Kind = "internal"              // exit 1

	// KindUpgradeRequired is the API refusing to serve this release at all.
	//
	// Its own code, rather than folding into usage or upstream, because it is
	// the one failure with a single known remedy that a pipeline can act on
	// without a human: run `outplane update`. Nothing about the request or the
	// credential is wrong, so pointing at either would send the reader looking
	// in the wrong place.
	KindUpgradeRequired Kind = "upgrade_required" // exit 9
)

// ExitCode returns the process exit status for a kind.
//
// These values are a public contract. Append only. Never reuse a number and
// never change what one means: callers pin them in scripts we cannot see.
func (k Kind) ExitCode() int {
	switch k {
	case KindUsage:
		return 2
	case KindAuth:
		return 3
	case KindConfirmation:
		return 4
	case KindNotFound:
		return 5
	case KindConflict:
		return 6
	case KindQuota:
		return 7
	case KindUpstream:
		return 8
	case KindUpgradeRequired:
		return 9
	case KindTimeout:
		return 124
	case KindInterrupted:
		return 130
	default:
		return 1
	}
}

// Retryable reports whether trying the same thing again could plausibly work.
//
// Note that quota is NOT retryable, even though it arrives as HTTP 429. The
// Out Plane API has no rate limiter anywhere; 429 means "this team is over its
// plan limit". An agent that treats it as backpressure will retry forever
// against a wall.
func (k Kind) Retryable() bool {
	return k == KindUpstream || k == KindTimeout
}

// Step is a suggested next action.
//
// Argv rather than a string, so an agent can execute it directly with no shell
// tokenisation, and so quoting bugs cannot turn a suggestion into a different
// command than the one we meant.
type Step struct {
	Why  string   `json:"why"`
	Argv []string `json:"argv"`
}

// Error is a CLI error.
//
// Field order below is the order it is rendered in, both as JSON and as text,
// so that reading the struct tells you what the user will see.
type Error struct {
	Kind Kind `json:"kind"`

	// Code is the fine-grained, stable identifier: "app.name_taken".
	// It is optional; a Kind alone is a valid error. When present it is frozen
	// forever, because agents build handlers keyed on it.
	Code string `json:"code,omitempty"`

	// Message is for a person. It may come verbatim from the API for 4xx
	// responses, where the text is hand-written and actionable. It is never
	// taken from a 500, where it could be any .NET exception string and might
	// carry a connection string or a SQL fragment.
	Message string `json:"message"`

	// Hint is our advice, added on top of whatever the server said.
	Hint string `json:"hint,omitempty"`

	// NextSteps are runnable. Populating this is the difference between an
	// error that stops someone and an error that moves them forward.
	NextSteps []Step `json:"next_steps,omitempty"`

	Retryable bool `json:"retryable"`

	// RetryAfter is seconds, when the server told us.
	RetryAfter int `json:"retry_after,omitempty"`

	// ConfirmCommand is set only for KindConfirmation. It is the exact
	// invocation that would proceed, so the approval decision can be handed to
	// a human, a CI gate or an agent harness without anyone having to
	// reconstruct the command.
	ConfirmCommand []string `json:"confirm_command,omitempty"`

	DocsURL string `json:"docs_url,omitempty"`

	// RequestID ties a failure to a server-side log entry. It is the only
	// thing a user can give support when the message itself is deliberately
	// generic, which is exactly the 500 case.
	RequestID string `json:"request_id,omitempty"`

	// Details carries structured context, such as the raw API message on a
	// 500 or the fields that failed validation. Never rendered in text mode.
	Details map[string]any `json:"details,omitempty"`
}

func (e *Error) Error() string { return e.Message }

// ExitCode is what the process should exit with.
func (e *Error) ExitCode() int { return e.Kind.ExitCode() }

// New builds an error of the given kind.
func New(kind Kind, format string, args ...any) *Error {
	return &Error{
		Kind:      kind,
		Message:   fmt.Sprintf(format, args...),
		Retryable: kind.Retryable(),
	}
}

// WithCode attaches a stable fine-grained code.
func (e *Error) WithCode(code string) *Error { e.Code = code; return e }

// WithHint attaches advice.
func (e *Error) WithHint(format string, args ...any) *Error {
	e.Hint = fmt.Sprintf(format, args...)
	return e
}

// WithStep appends a runnable next action.
func (e *Error) WithStep(why string, argv ...string) *Error {
	e.NextSteps = append(e.NextSteps, Step{Why: why, Argv: argv})
	return e
}

// WithRequestID attaches the server's request identifier.
func (e *Error) WithRequestID(id string) *Error { e.RequestID = id; return e }

// WithDetail attaches one structured detail.
func (e *Error) WithDetail(key string, value any) *Error {
	if e.Details == nil {
		e.Details = map[string]any{}
	}
	e.Details[key] = value
	return e
}

// ExitCodeOf returns the exit status for any error, so callers do not have to
// type-switch. An error that is not one of ours is an internal failure, which
// is honest: it means we did not anticipate it.
func ExitCodeOf(err error) int {
	if err == nil {
		return 0
	}
	var e *Error
	if errors.As(err, &e) {
		return e.ExitCode()
	}
	return KindInternal.ExitCode()
}

// AsError converts any error into a *Error, wrapping unrecognised ones as
// internal failures so that rendering never has to handle two shapes.
func AsError(err error) *Error {
	if err == nil {
		return nil
	}
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return &Error{
		Kind:    KindInternal,
		Message: err.Error(),
	}
}
