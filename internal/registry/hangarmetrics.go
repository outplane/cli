package registry

// What a Hangar is using.
//
// This is `metrics` for a Hangar: the same gateway, the same instant queries,
// the same fixed cost. What a reader needs to know differs in three places:
//
//   - The disk is the home disk, as last measured, rather than read and write
//     rates. It is the part of a Hangar that keeps what is written to it, so
//     how full it is is the question. Temporary storage is measured nowhere and
//     is not reported.
//   - A Hangar with nothing running reports null, not 0, because a 0% beside a
//     paused one reads as a Hangar that is running and idle.
//   - A Hangar at its memory limit freezes rather than restarting, so 90% is a
//     warning in the text output and the threshold to act on in the machine
//     formats.

func init() {
	Register(hangarMetrics())
}

func hangarMetrics() Command {
	return Command{
		Path:  []string{"hangar", "metrics"},
		Short: "show what Hangars are using right now",
		Long: "Reports current CPU and memory use, and how full the home disk is, against what " +
			"each Hangar's size allows.\n\n" +
			"With no argument it covers every Hangar in the team. Name one to see only that.\n\n" +
			"A Hangar that reaches its memory limit freezes rather than restarting, so use at " +
			"90% or more is called out under the table.\n\n" +
			"This is a snapshot, not a history. CPU is averaged over the last two minutes, and " +
			"the disk is its last measured fill, so a paused Hangar still reports what is on it.",

		Risk:         RiskRead,
		RequiresAuth: true,
		Session:      SessionAny,
		Idempotent:   true,

		APICalls: []string{
			"GET /api/Hangar/GetHangarsByTeamId",
			"GET {metricsGateway}/api/v1/query",
		},

		Args: []Arg{
			{
				Name:     "hangar",
				Short:    "Hangar name or id. Omit for every Hangar in the team",
				Required: false,
				Resolves: "hangar",
			},
		},

		OutputFields: []Field{
			{Name: "hangar", Type: "string"},
			{
				Name:        "status",
				Type:        "string",
				Description: "as `hangar list` reports it. Says why CPU and memory are null",
			},
			{
				Name:        "cpuMillicores",
				Type:        "int | null",
				Description: "1000 millicores is one core. null while nothing runs: paused, starting or stopped",
			},
			{
				Name: "cpuPercent",
				Type: "int | null",
				Description: "against cpuLimitMillicores. null while nothing runs or when no limit is on " +
					"record, and above 100 when the Hangar is over it",
			},
			{
				Name:        "memoryBytes",
				Type:        "int | null",
				Description: "working set. null while nothing runs",
			},
			{
				Name: "memoryPercent",
				Type: "int | null",
				Description: "against memoryLimitMb. null while nothing runs or when no limit is on record. " +
					"At 90 or more, act: a Hangar that reaches its limit freezes rather than restarting",
			},
			{
				Name: "diskUsedBytes",
				Type: "int | null",
				Description: "the home disk's last measured fill, kept while the Hangar is paused. null " +
					"until it has been measured",
			},
			{Name: "diskPercent", Type: "int | null", Description: "against diskCapacityBytes"},
			{
				Name:        "diskCapacityBytes",
				Type:        "int | null",
				Description: "the home disk's size as its file system reports it, a little under diskGb",
			},
			{Name: "cpuLimitMillicores", Type: "int", Description: "what its size allows. 0 when not on record"},
			{Name: "memoryLimitMb", Type: "int", Description: "what its size allows. 0 when not on record"},
		},

		ErrorCodes: []string{
			"logs.no_team_slug",
			"hangar.not_found",
			"hangar.ambiguous",
			"usage.empty_argument",
			"metrics.bad_response",
			"metrics.query_refused",
			"context.no_team",
		},
		ExitCodes: []int{0, 2, 3, 5, 8},

		Examples: []Example{
			{
				Title:   "what every Hangar is using",
				Command: "outplane hangar metrics",
				Argv:    []string{"outplane", "hangar", "metrics"},
				Risk:    RiskRead,
			},
			{
				Title:        "one Hangar",
				Command:      "outplane hangar metrics myhangar",
				Argv:         []string{"outplane", "hangar", "metrics", "myhangar"},
				Placeholders: map[string]string{"myhangar": "<HANGAR_NAME>"},
				Risk:         RiskRead,
			},
			{
				Title:        "check how close it is to its limits",
				Command:      "outplane hangar metrics myhangar --json --fields hangar,status,memoryPercent,diskPercent",
				Argv:         []string{"outplane", "hangar", "metrics", "myhangar", "--json", "--fields", "hangar,status,memoryPercent,diskPercent"},
				Placeholders: map[string]string{"myhangar": "<HANGAR_NAME>"},
				Risk:         RiskRead,
				OutputSample: map[string]any{
					"items": []any{
						map[string]any{"hangar": "myhangar", "status": "ready", "memoryPercent": 92, "diskPercent": 41},
					},
					"total":     1,
					"truncated": false,
				},
			},
		},

		AutomationNotes: []string{
			"Cost is fixed: one API call for the Hangars, then nine instant queries on the metrics " +
				"gateway for the whole team, the seven `metrics` runs and two for the disks. Naming " +
				"one Hangar filters the rows, not the queries, so ask once rather than in a loop.",
			"A paused or starting Hangar has no samples. cpuMillicores, cpuPercent, memoryBytes " +
				"and memoryPercent are null for it, not 0, and status says why.",
			"memoryPercent at 90 or more is the threshold to act on. A Hangar that reaches its " +
				"memory limit freezes rather than restarting, and its status can go on reading ready. " +
				"The text output warns under the table; the machine formats carry the number only.",
			"The disk figures are the home disk's last measured fill, carried for up to seven days, " +
				"so a paused Hangar still reports them. They are null for a Hangar whose disk has " +
				"never been measured, such as one still starting for the first time.",
			"Only the home disk is reported. Temporary storage outside it is not measured.",
			"Percentages are never capped: over 100 is a real state and the most useful thing " +
				"this reports.",
			"The text table writes bytes in units a person reads. The machine formats carry the " +
				"raw numbers, so nothing has to be parsed back.",
		},

		Related: []string{"hangar get", "hangar list", "hangar connect", "metrics"},
		DocsURL: "https://docs.outplane.com/cli/hangar",
	}
}
