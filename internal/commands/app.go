package commands

import (
	"context"
	"fmt"

	"github.com/outplane/cli/internal/core"
	"github.com/outplane/cli/internal/output"
)

func init() {
	register("app list", appList)
	register("app get", appGet)
}

// appList is the reference implementation for a read-only command.
//
// The shape below is the pattern every other read command should follow:
// get a client, call one core function, turn the result into a Table. There is
// no formatting here and no HTTP here; both live in packages that exist to do
// exactly one of those things.
func appList(ctx context.Context, req Request) (output.Table, error) {
	client, err := req.CLI.APIClient()
	if err != nil {
		return output.Table{}, err
	}

	apps, err := core.ListApps(ctx, client, req.Flags.String("search"))
	if err != nil {
		return output.Table{}, err
	}

	// Column order is the order a person scans: what it is called, whether it
	// is healthy, how many of it there are, how big each one is, and when
	// anything last happened to it. The id is in the JSON but not in the table,
	// because a GUID eats a third of the terminal width and nobody reads one.
	//
	// The date is the last deployment rather than updatedAt, which is the app
	// record's own modification time: editing a variable moves it and deploying
	// does not, so as a "what is stale here" column it would mislead.
	//
	// deploymentStatus is likewise JSON-only. It repeats status for every app
	// that is not paused, so as a column it would be noise on most rows and
	// useful on almost none.
	table := output.Table{
		Columns: []string{"name", "status", "instances", "size", "lastDeployedAt"},
		Total:   len(apps),
	}
	for _, a := range apps {
		table.Rows = append(table.Rows, map[string]any{
			"id":               a.ID,
			"name":             a.Name,
			"displayName":      a.DisplayName,
			"status":           a.Status,
			"deploymentStatus": a.DeploymentStatus,
			"paused":           a.Paused,
			"instances":        a.Instances,
			"size":             a.Size,
			"source":           a.Source,
			"lastDeployedAt":   a.LastDeployedAt,
			"updatedAt":        a.UpdatedAt,
		})
	}
	return table, nil
}

// appGet reports one application in full.
//
// Two requests, not one: the API addresses applications by id only, so a name
// has to be resolved against the list before the detail can be fetched. That is
// resolveApp's job, and it means `app get checkout` costs the same as
// `app get <guid>` plus one list call.
//
// The reason to run this rather than `app list` is the public address, which
// listing cannot report because endpoints are a separate request per app.
func appGet(ctx context.Context, req Request) (output.Table, error) {
	app, err := targetApp(ctx, req, "app", "get")
	if err != nil {
		return output.Table{}, err
	}

	client, err := req.CLI.APIClient()
	if err != nil {
		return output.Table{}, err
	}

	detail, err := core.GetApp(ctx, client, app.ID)
	if err != nil {
		return output.Table{}, err
	}

	return appDetailTable(detail), nil
}

func appDetailTable(d core.AppDetail) output.Table {
	endpoints := make([]map[string]any, 0, len(d.Endpoints))
	for _, e := range d.Endpoints {
		endpoints = append(endpoints, map[string]any{
			"portId":        e.PortID,
			"port":          e.Port,
			"scheme":        e.Scheme,
			"public":        e.Public,
			"url":           nilIfEmpty(e.URL),
			"customDomains": e.CustomDomains,
		})
	}

	return output.Table{
		Single: true,
		// endpoints is deliberately absent, like the nested app object in
		// deploy create: a list of objects renders as an unreadable map in a
		// table. url carries the answer most readers came for, and the footer
		// points at --json for the rest.
		// commitMessage is last because it is the only field that can be a
		// paragraph, and anything printed after it is pushed off the reader's
		// first screen.
		Columns: []string{
			"name", "displayName", "id", "status", "url",
			"instances", "size", "source", "repository", "branch", "imageRef",
			"buildMethod", "directory", "startCommand",
			"lastDeployedAt", "commitMessage",
		},
		Total:  1,
		Footer: endpointFooter(d),
		Rows: []map[string]any{{
			"id":               d.ID,
			"name":             d.Name,
			"displayName":      nilIfEmpty(d.DisplayName),
			"status":           d.Status,
			"deploymentStatus": d.DeploymentStatus,
			"paused":           d.Paused,
			"instances":        d.Instances,
			"size":             d.Size,
			"source":           d.Source,
			"url":              nilIfEmpty(d.URL),
			"endpoints":        endpoints,
			"repository":       nilIfEmpty(d.Repository),
			"branch":           nilIfEmpty(d.Branch),
			"imageRef":         nilIfEmpty(d.ImageRef),
			"sourceUrl":        nilIfEmpty(d.SourceURL),
			"publicSource":     d.PublicSource,
			"buildMethod":      d.BuildMethod,
			"directory":        nilIfEmpty(d.Directory),
			"startCommand":     nilIfEmpty(d.StartCommand),
			"includePaths":     nilIfEmpty(d.IncludePaths),
			"ignorePaths":      nilIfEmpty(d.IgnorePaths),
			"commitMessage":    nilIfEmpty(d.CommitMessage),
			"lastDeployedAt":   nilIfEmpty(d.LastDeployedAt),
			"createdAt":        nilIfEmpty(d.CreatedAt),
			"updatedAt":        nilIfEmpty(d.UpdatedAt),
		}},
	}
}

// endpointFooter names what the text view left out.
//
// url is one address, and the table gives a reader no way to tell whether it is
// the only one. An app with three domains and an app with one look identical
// there, so the count is the footer's whole job. When there is nothing left to
// say it says nothing.
func endpointFooter(d core.AppDetail) string {
	if len(d.Endpoints) == 0 {
		return "This app serves no ports, so it has no address."
	}

	switch n := addressCount(d); {
	case n == 0:
		// Ports, but nothing addressable over HTTP: a TCP port is reached by
		// host and port rather than by URL.
		return "This app has no HTTP address. Run with --json to see its ports."
	case n == 1:
		return ""
	default:
		return fmt.Sprintf("This app answers on %d addresses, and url is one of them. "+
			"Run with --json to see them all.", n)
	}
}

// addressCount is how many URLs the app can be reached at, across every port.
func addressCount(d core.AppDetail) int {
	n := 0
	for _, e := range d.Endpoints {
		if e.URL != "" {
			n++
		}
		n += len(e.CustomDomains)
	}
	return n
}
