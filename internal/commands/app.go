package commands

import (
	"context"

	"github.com/outplane/cli/internal/core"
	"github.com/outplane/cli/internal/output"
)

func init() {
	register("app list", appList)
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
	// is healthy, how big it is, where to reach it. The id is available in
	// JSON but is not shown in the table, because a GUID takes a third of the
	// terminal width and nobody reads one.
	table := output.Table{
		Columns: []string{"name", "status", "instances", "size", "url"},
		Total:   len(apps),
	}
	for _, a := range apps {
		table.Rows = append(table.Rows, map[string]any{
			"id":          a.ID,
			"name":        a.Name,
			"displayName": a.DisplayName,
			"status":      a.Status,
			"instances":   a.Instances,
			"size":        a.Size,
			"url":         a.URL,
			"updatedAt":   a.UpdatedAt,
		})
	}
	return table, nil
}
