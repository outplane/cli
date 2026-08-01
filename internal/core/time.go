package core

import (
	"strings"
	"time"
)

// serverInstant normalises a timestamp from the API into RFC 3339 UTC.
//
// The API stores timestamps in `timestamp without time zone` columns and
// serialises them with no offset, so a response carries "2026-06-15T17:17:26"
// and means UTC. Reading that as local time silently shifts every date by the
// reader's own offset, which is how a deploy from an hour ago comes to be
// displayed three hours in the future.
//
// The rule below matches the console's parseServerInstant exactly, and needs to
// keep matching it. Two clients disagreeing about what a stored timestamp means
// is a worse outcome than either rule would be on its own.
//
// A value that does not parse is returned unchanged rather than dropped or
// blanked. These fields are informational, and showing the server's raw string
// beats showing nothing while a reader tries to work out which of the two
// systems lost their data.
func serverInstant(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}

	// A field the server declares as a plain date cannot be null, so "never
	// happened" arrives as the zero value instead. Printing "0001-01-01" for an
	// app that has never been deployed reads as a corrupt record; the honest
	// answer is that there is no timestamp.
	if strings.HasPrefix(s, "0001-01-01") {
		return ""
	}

	// Only ISO-shaped values are assumed to be UTC. The API also emits
	// "07/28/2026 20:10:51" in a couple of places, and guessing a zone for a
	// format this one does not recognise would be inventing information.
	if !hasZone(s) && strings.Contains(s, "T") {
		s += "Z"
	}

	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return s
	}
	return t.UTC().Format(time.RFC3339)
}

// hasZone reports whether an ISO timestamp already carries a zone: a trailing
// Z, or a numeric offset written as +03:00 or +0300.
func hasZone(s string) bool {
	if strings.HasSuffix(s, "Z") || strings.HasSuffix(s, "z") {
		return true
	}
	return tailMatches(s, "+dd:dd") || tailMatches(s, "+dddd")
}

// tailMatches tests the end of s against a shape, in which 'd' stands for any
// digit, '+' for a plus or a minus sign, and every other byte for itself.
//
// Matching by shape rather than by regular expression keeps this readable and
// keeps the package free of a dependency it would otherwise need for one test.
func tailMatches(s, shape string) bool {
	if len(s) < len(shape) {
		return false
	}
	tail := s[len(s)-len(shape):]
	for i := 0; i < len(shape); i++ {
		switch shape[i] {
		case 'd':
			if tail[i] < '0' || tail[i] > '9' {
				return false
			}
		case '+':
			if tail[i] != '+' && tail[i] != '-' {
				return false
			}
		default:
			if tail[i] != shape[i] {
				return false
			}
		}
	}
	return true
}
