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
	// is healthy, how many of it there are, how big each one is. The id and the
	// timestamps are in the JSON but not in the table, because a GUID eats a
	// third of the terminal width and nobody reads one.
	//
	// deploymentStatus is likewise JSON-only. It repeats status for every app
	// that is not paused, so as a column it would be noise on most rows and
	// useful on almost none.
	table := output.Table{
		Columns: []string{"name", "status", "instances", "size"},
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
			"updatedAt":        a.UpdatedAt,
		})
	}
	return table, nil
}
