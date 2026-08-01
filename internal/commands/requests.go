package commands

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/outplane/cli/internal/clierr"
	"github.com/outplane/cli/internal/core"
	"github.com/outplane/cli/internal/output"
)

func init() {
	register("requests", requests)
}

// requests prints the HTTP traffic an application received.
//
// It answers a question runtime logs cannot: what the outside world sent and
// what it got back. An application that returns 502 without printing anything
// says nothing in `logs` and everything here.
//
// The command has two shapes and they are deliberately different. A finished
// window is a table, because a request is a record with fields and a reader
// wants them lined up. A follow is a stream of lines, because there is no last
// row to align against and a table redrawn every two seconds is unreadable.
func requests(ctx context.Context, req Request) (output.Table, error) {
	gw, err := openGateway(req)
	if err != nil {
		return output.Table{}, err
	}

	filter, err := buildRequestFilter(ctx, req)
	if err != nil {
		return output.Table{}, err
	}
	window, err := buildWindow(req)
	if err != nil {
		return output.Table{}, err
	}

	query := core.BuildRequestQuery(filter)
	fetch := func(ctx context.Context, w core.LogWindow) ([]core.HTTPRequest, error) {
		return core.QueryRequests(ctx, gw.client, gw.base, gw.teamSlug, query, w)
	}

	found, err := fetch(ctx, window)
	if err != nil {
		return output.Table{}, err
	}

	// The application is named on every row only when the query spans the team.
	// For one application it would repeat the name the reader just typed.
	showApp := filter.App == ""

	if !req.Flags.Bool("follow") {
		return requestTable(found, showApp, req.Flags.String("since")), nil
	}

	// Stdout now carries a sequence rather than one result, and an error
	// envelope cannot ride along in it. See output.Writer.RawStream.
	req.CLI.Out.RawStream = true
	print := func(items []core.HTTPRequest) { printRequests(req, items, showApp) }
	print(found)

	return streamed(), follow(ctx, req, window, core.Cursor(found), stream[core.HTTPRequest]{
		fetch:  fetch,
		print:  print,
		cursor: core.Cursor[core.HTTPRequest],
	})
}

// requestTable renders a finished window.
//
// An empty result says which window was empty, because "no results" on its own
// leaves a reader unsure whether the application had no traffic or they asked
// about the wrong hour. It is a footer rather than a note so that it lands
// under the answer instead of above it.
func requestTable(found []core.HTTPRequest, showApp bool, since string) output.Table {
	table := output.Table{
		Columns: requestColumns(showApp),
		Total:   len(found),
	}
	if len(found) == 0 {
		table.Footer = fmt.Sprintf("Nothing arrived in the last %s. Try a longer window with --since.",
			orDefault(since, "1h"))
	}
	for _, r := range found {
		table.Rows = append(table.Rows, requestRow(r))
	}
	return table
}

// requestColumns is the text layout, and the order is the order a person reads
// an access log in: when, how it ended, what was asked, how long it took, and
// what was asked for.
//
// The columns are a fraction of the fields. A client address and a country are
// answers to a specific question rather than something to scan, and putting an
// address on screen by default puts it in every screen share and CI log too.
func requestColumns(showApp bool) []string {
	columns := []string{"at", "status", "method", "latencyMs", "path"}
	if showApp {
		return append([]string{"at", "app"}, columns[1:]...)
	}
	return columns
}

func requestRow(r core.HTTPRequest) map[string]any {
	return map[string]any{
		"at":           r.At.Format(time.RFC3339),
		"app":          nilIfEmpty(r.App),
		"method":       r.Method,
		"status":       r.Status,
		"path":         r.Path,
		"host":         r.Host,
		"latencyMs":    round1(r.LatencyMs),
		"originMs":     round1(r.OriginMs),
		"originStatus": r.OriginStatus,
		"bytes":        r.Bytes,
		"protocol":     nilIfEmpty(r.Protocol),
		"scheme":       nilIfEmpty(r.Scheme),
		"clientIp":     nilIfEmpty(r.ClientIP),
		"country":      nilIfEmpty(r.Country),
		"service":      nilIfEmpty(r.Service),
	}
}

// printRequests writes a follow's records as they arrive.
func printRequests(req Request, found []core.HTTPRequest, showApp bool) {
	for _, r := range found {
		req.CLI.Out.Item(requestRow(r), requestLine(r, showApp))
	}
}

// requestLine is one record at fixed widths, in the column order the table
// uses, so that following and reading a finished window look the same.
//
// The widths come from what the values actually are rather than from what they
// could be: a status is three digits, a method is at most seven letters, and a
// name is usually short. A value that overflows its column pushes the line out
// rather than being cut, because a truncated path is worse than a ragged line.
func requestLine(r core.HTTPRequest, showApp bool) string {
	var b strings.Builder

	b.WriteString(r.At.Format(time.RFC3339))
	if showApp {
		fmt.Fprintf(&b, "  %-18s", r.App)
	}
	fmt.Fprintf(&b, "  %3d  %-7s %9s  %s", r.Status, r.Method, latency(r.LatencyMs), r.Path)

	return b.String()
}

func latency(ms float64) string { return fmt.Sprintf("%.1fms", ms) }

// round1 keeps one decimal, which is the precision a latency is discussed at.
// Full precision would report a request as having taken 12.334917 ms, which is
// six digits of noise around the one that matters.
func round1(v float64) float64 {
	return float64(int64(v*10+0.5)) / 10
}

// buildRequestFilter turns the flags into a query filter.
func buildRequestFilter(ctx context.Context, req Request) (core.RequestFilter, error) {
	f := core.RequestFilter{Search: req.Flags.String("search")}

	var err error
	if f.Statuses, err = parseList(req.Flags.String("status"), core.ParseStatus, "usage.bad_status"); err != nil {
		return f, err
	}
	if f.Methods, err = parseList(req.Flags.String("method"), core.ParseMethod, "usage.bad_method"); err != nil {
		return f, err
	}

	f.App, err = gatewayApp(ctx, req)
	return f, err
}

// parseList splits a comma-separated flag and validates each value.
//
// Every value is checked before the query is built, because these reach the
// gateway inside a regular expression: an unchecked one is either a syntax
// error reported by the server or, worse, a filter that quietly matches more
// than was asked for.
func parseList(raw string, parse func(string) (string, error), code string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}

	var out []string
	for _, part := range strings.Split(raw, ",") {
		if strings.TrimSpace(part) == "" {
			continue
		}
		v, err := parse(part)
		if err != nil {
			return nil, clierr.New(clierr.KindUsage, "%v", err).WithCode(code)
		}
		out = append(out, v)
	}
	return out, nil
}
