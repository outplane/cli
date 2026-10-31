package commands

import (
	"context"
	"fmt"
	"strings"

	"github.com/outplane/cli/internal/core"
	"github.com/outplane/cli/internal/output"
)

func init() {
	register("hangar metrics", hangarMetrics)
}

// hangarMemoryWarnPercent is the memory use worth a warning.
//
// A Hangar at its memory limit is not restarted. It freezes, with its session
// and its agents in it, so the warning has to come while there is still room to
// stop something.
const hangarMemoryWarnPercent = 90

// hangarMetrics reports what the team's Hangars are using right now.
//
// It is `metrics` for a Hangar, and runs the same queries. A Hangar's instance
// is named the way an application's is, so the usage queries find its CPU and
// memory, and its home disk is a volume like any other, so two volume queries
// find that. The temporary storage outside the home disk is measured nowhere,
// so it is not reported.
//
// The cost is fixed for the same reason as there: one call for the Hangars,
// then queries for the whole team. Naming one Hangar filters the rows.
func hangarMetrics(ctx context.Context, req Request) (output.Table, error) {
	gw, err := openGateway(req)
	if err != nil {
		return output.Table{}, err
	}

	// A named Hangar is resolved the way every other Hangar command resolves
	// one, so a mistyped name fails with the names that exist rather than with
	// an empty table that reads like an idle Hangar.
	var hangars []core.Hangar
	if len(req.Args) > 0 {
		hangar, err := targetHangar(ctx, req, "metrics")
		if err != nil {
			return output.Table{}, err
		}
		hangars = []core.Hangar{hangar}
	} else if hangars, err = core.ListHangars(ctx, gw.client); err != nil {
		return output.Table{}, err
	}

	apps := make([]core.App, 0, len(hangars))
	for _, h := range hangars {
		apps = append(apps, core.App{
			Name:               h.Name,
			CPULimitMillicores: h.CPULimitMillicores,
			MemoryLimitMB:      h.MemoryLimitMB,
		})
	}

	usage, err := core.QueryUsage(ctx, gw.client, gw.metrics, apps)
	if err != nil {
		return output.Table{}, err
	}

	disks, err := core.QueryVolumeUsage(ctx, gw.client, gw.metrics)
	if err != nil {
		return output.Table{}, err
	}

	return hangarUsageTable(hangars, usage, disks), nil
}

// hangarUsageTable is usageTable for a Hangar: the home disk in place of
// network and disk rates, and nothing at all where nothing was measured.
//
// QueryUsage reports zeroes for something with no instance reporting, which is
// right for an application in a list of them. Here a Hangar with none is
// paused, starting or stopped, and a 0% beside it reads as a Hangar that is
// running and idle. Null says that nothing was measured.
func hangarUsageTable(hangars []core.Hangar, usage []core.Usage, disks map[string]core.VolumeUsage) output.Table {
	table := output.Table{
		Columns: []string{
			"hangar", "status", "cpuMillicores", "cpuPercent",
			"memoryBytes", "memoryPercent", "diskUsedBytes", "diskPercent",
		},
		Headers: map[string]string{
			"cpuMillicores": "CPU",
			"cpuPercent":    "CPU %",
			"memoryBytes":   "MEMORY",
			"memoryPercent": "MEM %",
			"diskUsedBytes": "DISK",
			"diskPercent":   "DISK %",
		},
		Units: map[string]output.Unit{
			"cpuMillicores":     output.UnitMillicores,
			"cpuPercent":        output.UnitPercent,
			"memoryBytes":       output.UnitBytes,
			"memoryPercent":     output.UnitPercent,
			"diskUsedBytes":     output.UnitBytes,
			"diskPercent":       output.UnitPercent,
			"diskCapacityBytes": output.UnitBytes,
		},
		Total: len(hangars),
	}

	var nearLimit []string
	for i, h := range hangars {
		row := map[string]any{
			"hangar":             h.Name,
			"status":             h.Status,
			"cpuMillicores":      nil,
			"cpuPercent":         nil,
			"memoryBytes":        nil,
			"memoryPercent":      nil,
			"diskUsedBytes":      nil,
			"diskPercent":        nil,
			"diskCapacityBytes":  nil,
			"cpuLimitMillicores": h.CPULimitMillicores,
			"memoryLimitMb":      h.MemoryLimitMB,
		}

		if u := usage[i]; u.Instances > 0 {
			row["cpuMillicores"] = u.CPUMillicores
			row["cpuPercent"] = percent(u.CPUPercent)
			row["memoryBytes"] = u.MemoryBytes
			row["memoryPercent"] = percent(u.MemoryPercent)
			if u.MemoryPercent != nil && *u.MemoryPercent >= hangarMemoryWarnPercent {
				nearLimit = append(nearLimit, h.Name)
			}
		}

		// The disk is last known rather than live, so a paused Hangar still
		// reports what is on it. Only one never measured has nothing here.
		if d, ok := disks[h.DiskID]; ok && h.DiskID != "" {
			row["diskUsedBytes"] = d.UsedBytes
			row["diskPercent"] = percent(d.Percent)
			if d.CapacityBytes > 0 {
				row["diskCapacityBytes"] = d.CapacityBytes
			}
		}

		table.Rows = append(table.Rows, row)
	}

	if len(nearLimit) > 0 {
		table.Footer = memoryWarning(nearLimit)
	}
	return table
}

// memoryWarning names the Hangars close to their memory limit and what to do
// about it.
func memoryWarning(names []string) string {
	subject := names[0] + " is"
	if len(names) > 1 {
		subject = strings.Join(names, ", ") + " are"
	}
	return fmt.Sprintf("%s at %d%% or more of the memory limit. A Hangar that reaches its limit "+
		"freezes rather than restarting, so stop a dev server or an agent it does not need "+
		"before it gets there.", subject, hangarMemoryWarnPercent)
}
