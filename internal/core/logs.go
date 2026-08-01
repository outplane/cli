package core

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/outplane/cli/internal/api"
	"github.com/outplane/cli/internal/clierr"
)

// LogLine is one line an application wrote.
type LogLine struct {
	// At is when the line was written, as reported by the collector.
	At time.Time

	// Text is the line itself, with no timestamp prefix: the gateway keeps the
	// two apart and re-joining them would stop `outplane logs | grep` matching
	// what the reader sees.
	Text string

	// App is which application produced it, which matters when the query
	// covers a whole team.
	App string

	// atNs is the raw nanosecond timestamp. Kept as the string the gateway
	// sent, because it is also the cursor for the next request and reformatting
	// it through a time.Time would round it into overlapping or skipped lines.
	atNs string
}

// logResponse is the gateway's query result.
type logResponse struct {
	Data struct {
		Result []struct {
			Stream map[string]string `json:"stream"`
			// Values are [timestampNs, line] pairs.
			Values [][2]string `json:"values"`
		} `json:"result"`
	} `json:"data"`
}

// LogWindow is the span and size of one query.
type LogWindow struct {
	Since time.Duration
	Limit int

	// after, when set, replaces Since: the follow loop asks for everything
	// newer than the last line it printed rather than re-asking for a window
	// and filtering. Nanoseconds, exclusive.
	after string
}

// entry is one line as the gateway returned it, before anything decides what
// the line means.
//
// The two commands that read this gateway want different things from the same
// response: `logs` wants the text, `requests` wants the fields inside it. The
// fetch, the window arithmetic and the ordering are identical for both, so they
// happen once, here, and each command decodes what it needs afterwards.
type entry struct {
	atNs   string
	line   string
	labels map[string]string
}

// queryRange asks the gateway for one window.
//
// Entries come back newest-first and are returned oldest-first, because that is
// the order a log is read in. Sorting here rather than in the callers keeps the
// follow loop's cursor arithmetic on the same data it prints.
func queryRange(ctx context.Context, c *api.Client, base, teamSlug, query string, w LogWindow) ([]entry, error) {
	start, end := w.bounds(time.Now())

	params := url.Values{}
	params.Set("query", query)
	params.Set("start", start)
	params.Set("end", end)
	params.Set("limit", strconv.Itoa(w.Limit))
	// Newest first, so a limit keeps the most recent lines rather than the
	// oldest ones in the window, which is what "show me the last 100" means.
	params.Set("direction", "backward")

	path := fmt.Sprintf("%s/%s/loki/api/v1/query_range?%s",
		strings.TrimRight(base, "/"), url.PathEscape(teamSlug), params.Encode())

	raw, err := c.GetAbsolute(ctx, path)
	if err != nil {
		return nil, err
	}

	var resp logResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, clierr.New(clierr.KindUpstream,
			"the log gateway returned something this release cannot read").
			WithCode("logs.bad_response").
			WithDetail("parseError", err.Error())
	}

	var entries []entry
	for _, stream := range resp.Data.Result {
		for _, v := range stream.Values {
			entries = append(entries, entry{atNs: v[0], line: v[1], labels: stream.Stream})
		}
	}

	// Oldest first. Streams arrive separately, so lines from two applications
	// are interleaved only after this.
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].atNs < entries[j].atNs })
	return entries, nil
}

// QueryLogs fetches an application's output.
func QueryLogs(ctx context.Context, c *api.Client, base, teamSlug, query string, w LogWindow) ([]LogLine, error) {
	entries, err := queryRange(ctx, c, base, teamSlug, query, w)
	if err != nil {
		return nil, err
	}

	lines := make([]LogLine, 0, len(entries))
	for _, e := range entries {
		lines = append(lines, LogLine{
			At:   nsToTime(e.atNs),
			atNs: e.atNs,
			Text: strings.TrimRight(e.line, "\n"),
			App:  e.labels["app_name"],
		})
	}
	return lines, nil
}

// cursored is anything a follow loop can resume from.
type cursored interface{ cursor() string }

func (l LogLine) cursor() string { return l.atNs }

// Cursor is the position to resume a follow from, or empty when there is none.
//
// It takes the last item rather than the newest timestamp because the entries
// are already ordered, and re-deriving the maximum here would be a second place
// that has to agree with queryRange about which end is newest.
func Cursor[T cursored](items []T) string {
	if len(items) == 0 {
		return ""
	}
	return items[len(items)-1].cursor()
}

// After returns a window that asks only for lines newer than cursor.
func (w LogWindow) After(cursor string) LogWindow {
	w.after = cursor
	return w
}

// bounds resolves the window into the gateway's nanosecond range.
//
// A follow starts one nanosecond after the last line already printed. Starting
// at the line itself would print it again on every poll, which on a quiet
// application means the same line forever.
func (w LogWindow) bounds(now time.Time) (start, end string) {
	end = strconv.FormatInt(now.UnixNano(), 10)

	if w.after != "" {
		if n, err := strconv.ParseInt(w.after, 10, 64); err == nil {
			return strconv.FormatInt(n+1, 10), end
		}
	}
	return strconv.FormatInt(now.Add(-w.Since).UnixNano(), 10), end
}

func nsToTime(ns string) time.Time {
	n, err := strconv.ParseInt(ns, 10, 64)
	if err != nil {
		return time.Time{}
	}
	return time.Unix(0, n).UTC()
}
