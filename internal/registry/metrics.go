package registry

// Resource usage.
//
// A third host after the API and the log gateway, reached the same way and
// scoped to the team by the same header.
//
// Facts that shape the command:
//
//   - The application is not a label on a metric. The server cuts it out of the
//     pod name, which works because an application name cannot contain a dash.
//   - Usage is averaged across an application's instances rather than summed,
//     because the limit is per instance. An average against that limit is a
//     percentage that means the same thing at one replica or five.
//   - Everything is an instant query. There is no chart to draw in a terminal,
//     and a table of data points is not one.

func init() {
	Register(metrics())
}

func metrics() Command {
	return Command{
		Path:  []string{"metrics"},
		Short: "show what applications are using right now",
		Long: "Reports current CPU, memory, network and disk use, and how close each " +
			"application is to the limit its instance type allows.\n\n" +
			"With no argument it covers every application in the team. Name one to see " +
			"only that.\n\n" +
			"This is a snapshot, not a history. Rates are averaged over the last two " +
			"minutes, which is what a rate is: there is no instantaneous figure to report, " +
			"and a spike lasting seconds will not appear here.",

		Risk:         RiskRead,
		RequiresAuth: true,
		Session:      SessionAny,
		Idempotent:   true,

		APICalls: []string{
			"GET /api/App/GetAppsByTeamId",
			"GET {metricsGateway}/api/v1/query",
		},

		Args: []Arg{
			{
				Name:     "app",
				Short:    "application name or id. Omit for the whole team",
				Required: false,
				Resolves: "app",
			},
		},

		OutputFields: []Field{
			{Name: "app", Type: "string"},
			{Name: "cpuMillicores", Type: "int", Description: "1000 millicores is one core"},
			{
				Name: "cpuPercent",
				Type: "int | null",
				Description: "against the per-instance limit. null when no limit is on " +
					"record, and above 100 when the app is over it",
			},
			{Name: "memoryBytes", Type: "int", Description: "working set, per instance"},
			{Name: "memoryPercent", Type: "int | null", Description: "against the per-instance limit"},
			{Name: "networkInBps", Type: "float", Description: "bytes per second received"},
			{Name: "networkOutBps", Type: "float", Description: "bytes per second sent"},
			{Name: "diskReadBps", Type: "float"},
			{Name: "diskWriteBps", Type: "float"},
			{
				Name: "instances",
				Type: "int",
				Description: "how many instances are reporting, which is what is running. " +
					"It differs from the configured count while an app is starting, " +
					"stopping or partly crashed",
			},
			{Name: "cpuLimitMillicores", Type: "int", Description: "what one instance may use"},
			{Name: "memoryLimitMb", Type: "int", Description: "what one instance may use"},
		},

		ErrorCodes: []string{
			"logs.no_team_slug",
			"app.not_found",
			"usage.empty_argument",
			"metrics.bad_response",
			"metrics.query_refused",
			"context.no_team",
		},
		ExitCodes: []int{0, 2, 3, 5, 8},

		Examples: []Example{
			{
				Title:   "what everything is using",
				Command: "outplane metrics",
				Argv:    []string{"outplane", "metrics"},
				Risk:    RiskRead,
			},
			{
				Title:        "one application",
				Command:      "outplane metrics checkout",
				Argv:         []string{"outplane", "metrics", "checkout"},
				Placeholders: map[string]string{"checkout": "<APP_NAME>"},
				Risk:         RiskRead,
			},
			{
				Title:   "find what is near its limit",
				Command: "outplane metrics --json --fields app,cpuPercent,memoryPercent",
				Argv:    []string{"outplane", "metrics", "--json", "--fields", "app,cpuPercent,memoryPercent"},
				Risk:    RiskRead,
				OutputSample: map[string]any{
					"items": []any{
						map[string]any{"app": "checkout", "cpuPercent": 12, "memoryPercent": 83},
						map[string]any{"app": "worker", "cpuPercent": 0, "memoryPercent": 4},
					},
					"total":     2,
					"truncated": false,
				},
			},
		},

		AutomationNotes: []string{
			"Every figure is per instance, not per application: usage is averaged across " +
				"the instances, because the limit is per instance too. An app running five " +
				"replicas at 50% is using five halves of a limit, not 250% of one.",
			"instances is what is reporting, which is not the configured replica count. " +
				"`app list` has the configured one, and a difference between them means the " +
				"app is starting, stopping or partly crashed.",
			"cpuPercent and memoryPercent are null when no limit is on record, and never " +
				"capped: over 100 is a real state and the most useful thing this reports.",
			"An application with no running instances reports zeroes rather than being " +
				"omitted, so a paused app is visible as a paused app.",
			"Naming one application filters the rows, not the queries. Cost is the same " +
				"whether you ask about one or all of them, so ask once rather than in a loop.",
			"Rates are two-minute averages. Two calls a second apart return nearly the same " +
				"numbers, and neither shows a spike that lasted seconds.",
			"The text table writes bytes and rates in units a person reads. The machine " +
				"formats carry the raw numbers, so nothing has to be parsed back.",
		},

		Related: []string{"app list", "app get", "logs", "requests"},
		DocsURL: "https://docs.outplane.com/cli/metrics",
	}
}
