package registry

// HTTP access logs.
//
// What reached an application from outside, recorded by the proxy in front of
// it. This is not the same thing as `logs`, and the difference matters when
// something is wrong: a runtime log is whatever the application chose to print,
// while an access log exists whether the application said anything or not. An
// app answering 502 in silence is invisible in one and obvious in the other.
//
// Facts that shape the command:
//
//   - The method, the status and the host are recorded as stream labels, so
//     filtering on them selects streams without reading a single record.
//   - Nothing labels the application. The proxy records a service name that
//     contains it, so naming an app costs a parse of every candidate record.
//   - Only HTTP is recorded. A TCP port is forwarded rather than proxied, so it
//     produces no requests here at all.

func init() {
	Register(requests())
}

func requests() Command {
	return Command{
		Path:  []string{"requests"},
		Short: "show the HTTP requests an application received",
		Long: "Prints the HTTP traffic that reached an application: when, from where, " +
			"what was asked, what was answered and how long it took.\n\n" +
			"With no argument it covers every application in the team, which is what you " +
			"want before you know where a problem is. Name one to narrow it.\n\n" +
			"This is the proxy's record, so it exists whether or not the application " +
			"printed anything. For what the application itself printed, use `outplane logs`.",

		Risk:         RiskRead,
		RequiresAuth: true,
		Session:      SessionAny,
		Idempotent:   true,

		LongRunning: true,
		Streams:     StreamNDJSON,

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
				Description: "how many of the most recent requests to show",
			},
			{
				Name:        "follow",
				Short:       "f",
				Type:        "bool",
				Default:     "false",
				Description: "keep printing new requests until interrupted",
			},
			{
				Name: "status",
				Type: "string",
				Description: "only these statuses, comma separated. " +
					"A class such as 5xx, or an exact code such as 404",
			},
			{
				Name:        "method",
				Type:        "string",
				Description: "only these methods, comma separated, e.g. POST,DELETE",
			},
			{
				Name: "search",
				Type: "string",
				Description: "only records containing this text, case-insensitive. " +
					"Matches the whole record, so it reaches the path, the host and the " +
					"headers alike",
			},
		},

		OutputFields: []Field{
			{Name: "at", Type: "string", Description: "RFC 3339, UTC"},
			{
				Name:        "app",
				Type:        "string | null",
				Description: "which application served it. null when the proxy's service name has an unrecognised shape",
			},
			{Name: "method", Type: "string"},
			{Name: "status", Type: "int", Description: "what the client received"},
			{Name: "path", Type: "string"},
			{Name: "host", Type: "string", Description: "the address that was asked for, which may be a custom domain"},
			{Name: "latencyMs", Type: "float", Description: "the whole request, as the client experienced it"},
			{
				Name:        "originMs",
				Type:        "float",
				Description: "the part the application itself took. The difference from latencyMs is proxy overhead and retries",
			},
			{
				Name: "originStatus",
				Type: "int",
				Description: "what the application answered. It differs from status when the " +
					"proxy answered on its own, which is how a 502 with originStatus 0 reads " +
					"as \"the app never replied\"",
			},
			{Name: "bytes", Type: "int", Description: "response size"},
			{Name: "protocol", Type: "string | null", Description: "e.g. HTTP/2.0"},
			{Name: "scheme", Type: "string | null"},
			{
				Name:        "clientIp",
				Type:        "string | null",
				Description: "the caller, taken from the forwarded address when there is one",
			},
			{Name: "country", Type: "string | null", Description: "two-letter code, when the edge reported one"},
			{
				Name:        "service",
				Type:        "string | null",
				Description: "the proxy's own name for the route, which is where app comes from",
			},
		},

		ErrorCodes: []string{
			"logs.no_team_slug",
			"app.not_found",
			"usage.bad_status",
			"usage.bad_method",
			"usage.bad_duration",
		},
		ExitCodes: []int{0, 2, 3, 5, 8, 130},

		Examples: []Example{
			{
				Title:   "the last hour of traffic, for every application",
				Command: "outplane requests",
				Argv:    []string{"outplane", "requests"},
				Risk:    RiskRead,
			},
			{
				Title:        "what is failing on one application",
				Command:      "outplane requests checkout --status 5xx --since 24h",
				Argv:         []string{"outplane", "requests", "checkout", "--status", "5xx", "--since", "24h"},
				Placeholders: map[string]string{"checkout": "<APP_NAME>"},
				Risk:         RiskRead,
			},
			{
				Title:        "watch traffic as it arrives",
				Command:      "outplane requests checkout --follow",
				Argv:         []string{"outplane", "requests", "checkout", "--follow"},
				Placeholders: map[string]string{"checkout": "<APP_NAME>"},
				Risk:         RiskRead,
			},
			{
				Title:   "the slowest requests in the last hour",
				Command: "outplane requests --json --fields path,latencyMs,status",
				Argv:    []string{"outplane", "requests", "--json", "--fields", "path,latencyMs,status"},
				Risk:    RiskRead,
				OutputSample: map[string]any{
					"items": []any{
						map[string]any{"path": "/api/checkout", "latencyMs": 1240.7, "status": 200},
						map[string]any{"path": "/healthz", "latencyMs": 1.2, "status": 200},
					},
					"total":     2,
					"truncated": false,
				},
			},
			{
				Title:        "one path, whatever it answered",
				Command:      "outplane requests checkout --search /api/orders",
				Argv:         []string{"outplane", "requests", "checkout", "--search", "/api/orders"},
				Placeholders: map[string]string{"checkout": "<APP_NAME>"},
				Risk:         RiskRead,
			},
		},

		AutomationNotes: []string{
			"Without --follow this returns {items, total, truncated} like any other list. " +
				"With --follow it emits one object per line as requests arrive, in every " +
				"format, because a stream that has no end cannot be one JSON document.",
			"--lines counts from the most recent, so raising it reaches further back rather " +
				"than adding newer requests. There is no way to page: ask for a narrower " +
				"window instead.",
			"status is what the client received and originStatus is what the application " +
				"answered. A 502 with originStatus 0 means the application never replied, " +
				"which is a different failure from one it returned itself.",
			"--search matches the raw record before it is parsed, so it finds a path, a host " +
				"and a header alike. It cannot be anchored to one field; use --status and " +
				"--method for those.",
			"Only HTTP is recorded. An app that serves a TCP port is forwarded rather than " +
				"proxied and produces nothing here, which reads as no traffic rather than as " +
				"an error.",
			"Without --follow the command returns what exists now and exits 0, even when that " +
				"is nothing. An application nobody visited is not an error.",
			"--follow ends only on interruption, and exits 130 when it is.",
		},

		Related: []string{"logs", "app get", "deploy logs"},
		DocsURL: "https://docs.outplane.com/cli/requests",
	}
}
