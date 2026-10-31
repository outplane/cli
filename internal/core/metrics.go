package core

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"

	"github.com/outplane/cli/internal/api"
	"github.com/outplane/cli/internal/clierr"
)

// Resource usage, from the metrics gateway.
//
// A second host, like the log gateway, reached with the same credential and
// scoped to the team by the same header. The queries below are the console's,
// and two details in them are load-bearing:
//
//   - The application name is not a label. It is cut out of the pod name by the
//     server, which works because an application name cannot contain a dash and
//     a pod name is `{app}-{revision}-{hash}`.
//   - Usage is averaged across an application's pods rather than summed. The
//     limit is per pod, so an average divided by that limit is a percentage
//     that means the same thing whether the app runs one replica or five. A sum
//     would read as 400% on a healthy app with four of them.
//
// Every query is an instant query. A CLI cannot draw a trend line, and a table
// of five hundred data points is not a trend line either, so what this reports
// is what is happening now.

// Usage is one application's current resource use.
//
// Every rate is an average over the last two minutes, because that is what a
// rate is: there is no instantaneous CPU usage to report. A number here is
// therefore slightly behind a spike and will not show one that lasted seconds.
type Usage struct {
	App string `json:"app"`

	CPUMillicores int   `json:"cpuMillicores"`
	MemoryBytes   int64 `json:"memoryBytes"`

	// Percent is nil when the limit is unknown rather than 0, because 0% and
	// "not known" lead to opposite decisions about whether to scale.
	CPUPercent    *int `json:"cpuPercent"`
	MemoryPercent *int `json:"memoryPercent"`

	NetworkInBps  float64 `json:"networkInBps"`
	NetworkOutBps float64 `json:"networkOutBps"`
	DiskReadBps   float64 `json:"diskReadBps"`
	DiskWriteBps  float64 `json:"diskWriteBps"`

	// Instances is how many pods are reporting, which is what is actually
	// running. It differs from the configured replica count while an app is
	// starting, stopping or partly crashed, and that difference is the point.
	Instances int `json:"instances"`

	CPULimitMillicores int `json:"cpuLimitMillicores"`
	MemoryLimitMB      int `json:"memoryLimitMb"`
}

// containerBase excludes the pause container, which every pod has and which
// reports nothing anybody wants counted.
const containerBase = `container!="POD",container!=""`

// byApp wraps a metric so the result is grouped by application.
//
// label_replace does the cutting on the server: the pod name is
// `{app}-{revision}-{hash}` and the pattern takes everything before the first
// dash, which is the whole application name because a name cannot contain one.
func byApp(aggregate, metric string) string {
	return fmt.Sprintf(`%s by (app_name) (label_replace(%s, "app_name", "$1", "pod", "([^-]+)-.*"))`,
		aggregate, metric)
}

// usageQueries is every query one `metrics` invocation runs, and the only place
// that says what each one means.
//
// They are not filtered by application. The gateway already scopes to the team,
// the payload is one sample per app either way, and asking for everything means
// naming one application costs exactly what naming none does.
func usageQueries() []usageQuery {
	rate := func(metric, selector string) string {
		return fmt.Sprintf("rate(%s{%s}[2m])", metric, selector)
	}

	return []usageQuery{
		{
			name:  "cpu",
			query: byApp("avg", rate("container_cpu_usage_seconds_total", containerBase)),
			// Cores to millicores: a busy app reports 0.0031 cores, and three
			// decimal places of a core is a number people read wrong.
			apply: func(u *Usage, v float64) { u.CPUMillicores = int(v*1000 + 0.5) },
		},
		{
			name:  "memory",
			query: byApp("avg", "container_memory_working_set_bytes{"+containerBase+"}"),
			apply: func(u *Usage, v float64) { u.MemoryBytes = int64(v) },
		},
		{
			name:  "instances",
			query: byApp("count", "container_memory_working_set_bytes{"+containerBase+"}"),
			apply: func(u *Usage, v float64) { u.Instances = int(v) },
		},
		{
			// Network is recorded on the pod rather than the container, so it
			// carries no container label to filter on.
			name:  "network in",
			query: byApp("avg", rate("container_network_receive_bytes_total", `pod!=""`)),
			apply: func(u *Usage, v float64) { u.NetworkInBps = v },
		},
		{
			name:  "network out",
			query: byApp("avg", rate("container_network_transmit_bytes_total", `pod!=""`)),
			apply: func(u *Usage, v float64) { u.NetworkOutBps = v },
		},
		{
			name:  "disk read",
			query: byApp("avg", rate("container_fs_reads_bytes_total", containerBase)),
			apply: func(u *Usage, v float64) { u.DiskReadBps = v },
		},
		{
			name:  "disk write",
			query: byApp("avg", rate("container_fs_writes_bytes_total", containerBase)),
			apply: func(u *Usage, v float64) { u.DiskWriteBps = v },
		},
	}
}

type usageQuery struct {
	name  string
	query string
	apply func(*Usage, float64)
}

// QueryUsage reports current usage for the given applications.
//
// Rows come from the application list rather than from the metrics, and the
// order is the caller's. An app with no pods therefore appears with zeroes,
// which is the honest answer for something paused or crash-looping, and a pod
// belonging to no application is ignored: a build runs in the same place and is
// not an application's usage.
func QueryUsage(ctx context.Context, c *api.Client, base string, apps []App) ([]Usage, error) {
	usage := make([]Usage, 0, len(apps))
	index := make(map[string]int, len(apps))
	for _, a := range apps {
		index[a.Name] = len(usage)
		usage = append(usage, Usage{
			App:                a.Name,
			CPULimitMillicores: a.CPULimitMillicores,
			MemoryLimitMB:      a.MemoryLimitMB,
		})
	}

	for _, q := range usageQueries() {
		samples, err := promInstant(ctx, c, base, q.query, "app_name")
		if err != nil {
			// Which metric failed goes in a detail rather than in the message.
			// Wrapping with fmt.Errorf hid the typed error underneath, so the
			// whole failure was reported as "internal": a CLI bug, rather than
			// the gateway problem it is.
			if e := clierr.AsError(err); e != nil {
				return nil, e.WithDetail("metric", q.name)
			}
			return nil, err
		}
		for app, v := range samples {
			if i, ok := index[app]; ok {
				q.apply(&usage[i], v)
			}
		}
	}

	for i := range usage {
		usage[i].CPUPercent = percentOf(float64(usage[i].CPUMillicores), float64(usage[i].CPULimitMillicores))
		usage[i].MemoryPercent = percentOf(float64(usage[i].MemoryBytes)/(1024*1024), float64(usage[i].MemoryLimitMB))
	}

	return usage, nil
}

// percentOf is usage against a limit, or nil when there is no limit to compare
// against.
//
// It is not capped at 100. An application over its limit is the single most
// useful thing this command can report, and hiding it behind a ceiling would
// turn the one alarming number into an ordinary one.
func percentOf(used, limit float64) *int {
	if limit <= 0 {
		return nil
	}
	p := int(used/limit*100 + 0.5)
	return &p
}

// Volume usage.
//
// A disk is reported against its claim, and a claim is named pvc-{volumeId},
// so the claim name without its prefix is the volume id. The queries are the
// console's, for the console's reasons:
//
//   - A volume is reported only while a running instance mounts it, so the
//     last sample is carried forward for seven days. A disk that is not
//     mounted keeps the fill it had, which is still true: nothing is writing
//     to it.
//   - A disk that moved node leaves a frozen series on every node it left. Used
//     space is read from the last five minutes first, which is the node it is on
//     now, and from the frozen series only when there is no live one. The
//     largest frozen value can be a peak that has since been freed, and would
//     read as a full disk for a week.
//   - Capacity does not change, so the largest value in the window is exact.
const (
	volumeUsedQuery = `max by (namespace, persistentvolumeclaim) (last_over_time(kubelet_volume_stats_used_bytes[5m]))` +
		` or ` +
		`max by (namespace, persistentvolumeclaim) (last_over_time(kubelet_volume_stats_used_bytes[7d]))`
	volumeCapacityQuery = `max by (namespace, persistentvolumeclaim) (last_over_time(kubelet_volume_stats_capacity_bytes[7d]))`
)

// VolumeUsage is one volume's last known fill.
type VolumeUsage struct {
	UsedBytes     int64
	CapacityBytes int64

	// Percent is nil when the capacity is not known, as for Usage.
	Percent *int
}

// QueryVolumeUsage reports the last known fill of every volume in the team, by
// volume id.
//
// A volume with no sample in the window is absent rather than zero. One that
// has never been mounted has not been measured, which is not the same as empty.
func QueryVolumeUsage(ctx context.Context, c *api.Client, base string) (map[string]VolumeUsage, error) {
	queries := []struct {
		name  string
		query string
		apply func(*VolumeUsage, float64)
	}{
		{"disk used", volumeUsedQuery, func(u *VolumeUsage, v float64) { u.UsedBytes = int64(v) }},
		{"disk capacity", volumeCapacityQuery, func(u *VolumeUsage, v float64) { u.CapacityBytes = int64(v) }},
	}

	out := map[string]VolumeUsage{}
	for _, q := range queries {
		samples, err := promInstant(ctx, c, base, q.query, "persistentvolumeclaim")
		if err != nil {
			if e := clierr.AsError(err); e != nil {
				return nil, e.WithDetail("metric", q.name)
			}
			return nil, err
		}
		for claim, v := range samples {
			id := volumeID(claim)
			u := out[id]
			q.apply(&u, v)
			out[id] = u
		}
	}

	for id, u := range out {
		u.Percent = percentOf(float64(u.UsedBytes), float64(u.CapacityBytes))
		out[id] = u
	}
	return out, nil
}

// volumeID is the volume id a claim was named after.
func volumeID(claim string) string {
	return strings.ToLower(strings.TrimPrefix(claim, "pvc-"))
}

// promResponse is the metrics gateway's own envelope, which is not the API's.
type promResponse struct {
	Status string `json:"status"`
	Error  string `json:"error"`
	Data   struct {
		Result []struct {
			Metric map[string]string `json:"metric"`
			// Value is [epochSeconds, "stringified number"].
			Value []json.RawMessage `json:"value"`
		} `json:"result"`
	} `json:"data"`
}

// promInstant runs one instant query and returns a value for each value of
// label: an application for usage, a claim for a volume.
//
// A sample the CLI cannot use is dropped rather than reported. Three shapes get
// here and none of them is a reading:
//
//   - NaN, which the gateway writes for a series it could not compute. Go parses
//     it happily, which is how it used to arrive as a confident 0.
//   - +Inf, from a division by zero upstream. Converted to an integer it became
//     9223372036854775807 millicores: not a large number, a fictional one.
//   - a negative value, which no counter rate or byte count can be.
//
// Dropping one leaves the application at zero, the same as an application with
// nothing running, and leaves a volume absent, the same as one never measured.
// That is a real loss of nuance and still the better trade: a missing reading
// reads as idle, while a fabricated one reads as an emergency.
func promInstant(ctx context.Context, c *api.Client, base, query, label string) (map[string]float64, error) {
	path := fmt.Sprintf("%s/api/v1/query?%s",
		strings.TrimRight(base, "/"), url.Values{"query": {query}}.Encode())

	raw, err := c.GetAbsolute(ctx, path)
	if err != nil {
		return nil, err
	}

	// Both of these are the gateway's failure, not the CLI's, and the kind is
	// what an agent branches on: "internal" says file a bug, "upstream" says the
	// service is unhappy and the command may be worth retrying.
	var resp promResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, clierr.New(clierr.KindUpstream,
			"the metrics gateway returned something this release cannot read").
			WithCode("metrics.bad_response").
			WithDetail("parseError", err.Error())
	}
	if resp.Status == "error" {
		return nil, clierr.New(clierr.KindUpstream, "the metrics gateway refused the query").
			WithCode("metrics.query_refused").
			WithDetail("gatewayError", resp.Error)
	}

	out := make(map[string]float64, len(resp.Data.Result))
	for _, r := range resp.Data.Result {
		key := r.Metric[label]
		if key == "" || len(r.Value) != 2 {
			continue
		}
		var text string
		if err := json.Unmarshal(r.Value[1], &text); err != nil {
			continue
		}
		v, err := strconv.ParseFloat(text, 64)
		if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
			continue
		}
		out[key] = v
	}
	return out, nil
}
