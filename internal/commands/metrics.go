package commands

import (
	"context"

	"github.com/outplane/cli/internal/core"
	"github.com/outplane/cli/internal/output"
)

func init() {
	register("metrics", metrics)
}

// metrics reports what the team's applications are using right now.
//
// It is deliberately a snapshot rather than a history. A terminal cannot draw a
// trend, and a table of five hundred data points is not a trend either; what a
// reader can act on is what is happening now and how close it is to the limit
// they are paying for.
//
// The cost is fixed rather than proportional: one call for the applications and
// their limits, then one query per metric for the whole team. Naming a single
// application filters the result and does not make it cheaper, which is worth
// knowing before calling this in a loop.
func metrics(ctx context.Context, req Request) (output.Table, error) {
	gw, err := openGateway(req)
	if err != nil {
		return output.Table{}, err
	}

	apps, err := core.ListApps(ctx, gw.client, "")
	if err != nil {
		return output.Table{}, err
	}

	// One application narrows the rows rather than the queries. Resolving it
	// first also means a mistyped name fails with the usual "no application
	// called that", instead of an empty table that reads like an idle app.
	if name, err := gatewayApp(ctx, req); err != nil {
		return output.Table{}, err
	} else if name != "" {
		apps = onlyApp(apps, name)
	}

	usage, err := core.QueryUsage(ctx, gw.client, gw.metrics, apps)
	if err != nil {
		return output.Table{}, err
	}

	return usageTable(usage), nil
}

func onlyApp(apps []core.App, name string) []core.App {
	for _, a := range apps {
		if a.Name == name {
			return []core.App{a}
		}
	}
	return nil
}

// usageTable lays the snapshot out the way somebody scans it: what it is, how
// hard it is working, and only then the raw amounts.
//
// The percentages are the columns that answer a question on their own. A
// memory figure of 697 MiB means nothing without the limit beside it, which is
// why the limits are in the row even though the table has no room for them.
func usageTable(usage []core.Usage) output.Table {
	table := output.Table{
		Columns: []string{
			"app", "cpuMillicores", "cpuPercent",
			"memoryBytes", "memoryPercent",
			"networkInBps", "networkOutBps", "instances",
		},
		// The values carry their units, so the headings do not repeat them.
		Headers: map[string]string{
			"cpuMillicores": "CPU",
			"cpuPercent":    "CPU %",
			"memoryBytes":   "MEMORY",
			"memoryPercent": "MEM %",
			"networkInBps":  "NET IN",
			"networkOutBps": "NET OUT",
			"diskReadBps":   "DISK READ",
			"diskWriteBps":  "DISK WRITE",
		},
		Units: map[string]output.Unit{
			"cpuMillicores": output.UnitMillicores,
			"cpuPercent":    output.UnitPercent,
			"memoryBytes":   output.UnitBytes,
			"memoryPercent": output.UnitPercent,
			"networkInBps":  output.UnitBytesPerSecond,
			"networkOutBps": output.UnitBytesPerSecond,
			"diskReadBps":   output.UnitBytesPerSecond,
			"diskWriteBps":  output.UnitBytesPerSecond,
		},
		Total: len(usage),
	}

	for _, u := range usage {
		table.Rows = append(table.Rows, map[string]any{
			"app":           u.App,
			"cpuMillicores": u.CPUMillicores,
			"cpuPercent":    percent(u.CPUPercent),
			"memoryBytes":   u.MemoryBytes,
			"memoryPercent": percent(u.MemoryPercent),
			// Rounded, like a latency: a rate of 3.7743324540459384 bytes per
			// second is nine digits of noise around the one that matters.
			"networkInBps":       round1(u.NetworkInBps),
			"networkOutBps":      round1(u.NetworkOutBps),
			"diskReadBps":        round1(u.DiskReadBps),
			"diskWriteBps":       round1(u.DiskWriteBps),
			"instances":          u.Instances,
			"cpuLimitMillicores": u.CPULimitMillicores,
			"memoryLimitMb":      u.MemoryLimitMB,
		})
	}
	return table
}

// percent unwraps a percentage that may be unknown, so that "no limit on
// record" stays null all the way to the output rather than becoming 0.
func percent(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}
