package registry

// Runtime logs.
//
// What the application itself wrote, which is a different thing from what its
// build wrote. Build output belongs to one deployment and is read with
// `outplane deploy logs`; this is the running process, and it outlives any
// single deployment.
//
// Facts that shape the command:
//
//   - The lines come from a log gateway, a second host, not from the API. It is
//     reached with the same token and team header.
//   - Filtering happens there, not here. Pulling a busy application's whole
//     output back to discard most of it locally would fail exactly when
//     somebody is trying to read it.
//   - Severity is not recorded on the line; it is inferred from the words in
//     it. That makes --level a narrowing hint rather than a guarantee, and the
//     automation notes say so.

func init() {
	Register(logs())
}

func logs() Command {
	return Command{
		Path:  []string{"logs"},
		Short: "show what an application printed",
		Long: "Prints an application's own output.\n\n" +
			"With no argument it shows every application in the team, which is what " +
			"you want when you do not yet know where a problem is. Name one to narrow " +
			"it.\n\n" +
			"This is the running application. For what its build printed, use " +
			"`outplane deploy logs`.",

		Risk:         RiskRead,
		RequiresAuth: true,
		Session:      SessionAny,
		Idempotent:   true,

		LongRunning: true,
		Streams:     StreamLines,

		APICalls: []string{"GET {logGateway}/{team}/loki/api/v1/query_range"},

		Args: []Arg{
			{
				Name:     "app",
				Short:    "application name or id. Omit for the whole team",
				Required: false,
				Resolves: "app",
			},
		},

		Flags: []Flag{
			{
				Name:        "since",
				Type:        "duration",
				Default:     "1h",
				Description: "how far back to look",
			},
			{
				Name:        "lines",
				Short:       "n",
				Type:        "int",
				Default:     "200",
				Description: "how many of the most recent lines to show",
			},
			{
				Name:        "follow",
				Short:       "f",
				Type:        "bool",
				Default:     "false",
				Description: "keep printing new lines until interrupted",
			},
			{
				Name:        "search",
				Type:        "string",
				Description: "only lines containing this text, case-insensitive",
			},
			{
				Name: "level",
				Type: "string",
				Enum: []string{"error", "warning", "info", "debug", "trace"},
				Description: "only lines that look like this severity. " +
					"Repeatable is not supported; pass the lowest you want",
			},
			{
				Name:        "timestamps",
				Type:        "bool",
				Default:     "false",
				Description: "prefix each line with the time it was written",
			},
		},

		// The output is the lines themselves, written as they arrive.
		OutputFields: nil,

		ErrorCodes: []string{"logs.no_team_slug", "app.not_found", "usage.bad_level"},
		ExitCodes:  []int{0, 2, 3, 5, 8, 130},

		Examples: []Example{
			{
				Title:   "the last hour, for every application in the team",
				Command: "outplane logs",
				Argv:    []string{"outplane", "logs"},
				Risk:    RiskRead,
			},
			{
				Title:        "follow one application",
				Command:      "outplane logs checkout --follow",
				Argv:         []string{"outplane", "logs", "checkout", "--follow"},
				Placeholders: map[string]string{"checkout": "<APP_NAME>"},
				Risk:         RiskRead,
			},
			{
				Title:        "errors from the last day, with times",
				Command:      "outplane logs checkout --since 24h --level error --timestamps",
				Argv:         []string{"outplane", "logs", "checkout", "--since", "24h", "--level", "error", "--timestamps"},
				Placeholders: map[string]string{"checkout": "<APP_NAME>"},
				Risk:         RiskRead,
			},
		},

		AutomationNotes: []string{
			"Output is the log text on stdout, one line per line, so it can be piped into " +
				"grep or a file. It is not JSON, and --json does not change that: a log line " +
				"has no fields to report.",
			"--level narrows by the words in the line, because severity is not recorded " +
				"separately. It can miss a line that phrases its severity differently, and " +
				"asking for info applies no filter at all, since info is the absence of the " +
				"other keywords.",
			"--lines counts from the most recent, so raising it reaches further back rather " +
				"than adding newer lines.",
			"Without --follow the command returns what exists now and exits 0, even when that " +
				"is nothing. An application that has printed nothing is not an error.",
			"--follow ends only on interruption, and exits 130 when it is. It never decides " +
				"the application is finished.",
			"This is the running application. Build output is `outplane deploy logs`.",
		},

		Related: []string{"deploy logs", "app list", "status"},
		DocsURL: "https://docs.outplane.com/cli/logs",
	}
}
