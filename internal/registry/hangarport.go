package registry

// The ports a Hangar has open for its dev servers.
//
// This group is the port group for a Hangar: list, set and unset, the same
// PORT[:SCHEME[:public|private]] syntax, and the same read, merge and send,
// because the endpoint replaces the whole set as an application's does. What
// a reader needs to know differs in three places, and every declaration here
// carries them:
//
//   - The Hangar is the first argument, not --app. Nothing defaults to a
//     Hangar, so it is always named.
//   - A save is live within seconds and restarts nothing, so there is no
//     --deploy, and the terminal session and the agents in it keep running.
//   - A Hangar has its own limits: at most three ports, never its own 7070.
//     Public means open to everyone on the internet, with no IP access
//     profile and no custom domain in front of it.

func init() {
	Register(
		hangarPortList(),
		hangarPortSet(),
		hangarPortUnset(),
	)
}

// hangarPortFields are the fields of one port, wherever one is reported.
func hangarPortFields() []Field {
	return []Field{
		{Name: "port", Type: "int", Description: "the port a dev server in the Hangar listens on"},
		{
			Name: "scheme", Type: "string", Enum: []string{"http", "h2c", "tcp"},
			Description: "how it is served. An unknown value arrives as unknown:N rather than being guessed",
		},
		{Name: "public", Type: "bool", Description: "whether it is open to everyone on the internet"},
		{
			Name: "url",
			Type: "string | null",
			Description: "the outside address, or null for a private port. An https address for " +
				"http and h2c; a host and a port number for tcp",
		},
		{
			Name:        "internal",
			Type:        "string",
			Description: "where the team's applications reach it, public or not: name:port",
		},
	}
}

// hangarPortChangeFields are what set and unset report.
func hangarPortChangeFields() []Field {
	return []Field{
		{Name: "action", Type: "string", Enum: []string{"set", "unset"}},
		{Name: "numbers", Type: "array", Description: "the ports named on the command line"},
		{Name: "hangar", Type: "string"},
		{Name: "hangarId", Type: "string"},
		{Name: "serving", Type: "int", Description: "how many ports the Hangar has open afterwards"},
		{
			Name: "ports",
			Type: "array",
			Description: "every port that was sent, {port, scheme, public, url, internal}. This is " +
				"the whole set, not the part that changed, because the request replaces. url is " +
				"null under --dry-run, because the server assigns it",
		},
		{Name: "changed", Type: "bool", Description: "false for a dry run"},
	}
}

// Shared by set and unset, which end the same way.
const (
	hangarPortsLiveNote = "Saving applies the change to the running Hangar within seconds and " +
		"restarts nothing: its terminal session and the agents in it keep running. There is no " +
		"deployment and no --deploy."
	hangarPortsReplaceNote = "The API replaces the whole set rather than merging, so this command " +
		"reads the current ports and sends them back with the change applied. Two callers changing " +
		"different ports at the same time can lose one of the changes. ports in the result is " +
		"everything that was sent."
	hangarPortsListenNote = "A dev server is reached only if it listens on 0.0.0.0. One that listens " +
		"on localhost is not: a public web address answers 502, and applications and tcp clients " +
		"have the connection refused."
)

func hangarPortList() Command {
	return Command{
		Path:  []string{"hangar", "port", "list"},
		Short: "list the ports a Hangar has open",
		Long: "Lists the ports a Hangar has open for its dev servers, with how each one is " +
			"reached.\n\n" +
			"Every open port is reachable from the team's applications at name:port, which " +
			"internal reports. A public port has an outside address besides: an https address " +
			"for http and h2c, a host and a port number for tcp. Public means open to everyone " +
			"on the internet.\n\n" +
			"A dev server has to listen on 0.0.0.0 to be reached at all. One that listens on " +
			"localhost is reached by nothing outside the Hangar.",

		Risk:         RiskRead,
		RequiresAuth: true,
		Session:      SessionAny,
		Idempotent:   true,

		APICalls: []string{"GET /api/Hangar/GetHangarsByTeamId"},

		Args: []Arg{hangarArg()},

		OutputFields: hangarPortFields(),

		ErrorCodes: []string{
			"hangar.not_found",
			"hangar.ambiguous",
			"usage.missing_argument",
			"usage.empty_argument",
			"context.no_team",
		},
		ExitCodes: []int{0, 2, 3, 5, 8},

		Examples: []Example{
			{
				Title:        "list a Hangar's ports",
				Command:      "outplane hangar port list myhangar",
				Argv:         []string{"outplane", "hangar", "port", "list", "myhangar"},
				Placeholders: map[string]string{"myhangar": "<HANGAR_NAME>"},
				Risk:         RiskRead,
			},
			{
				Title:        "find the addresses in a script",
				Command:      "outplane hangar port list myhangar --json --fields port,url,internal",
				Argv:         []string{"outplane", "hangar", "port", "list", "myhangar", "--json", "--fields", "port,url,internal"},
				Placeholders: map[string]string{"myhangar": "<HANGAR_NAME>"},
				Risk:         RiskRead,
				OutputSample: map[string]any{
					"items": []any{
						map[string]any{
							"port":     3000,
							"url":      "https://myhangar-3000-acme.outplane.app",
							"internal": "myhangar:3000",
						},
						map[string]any{"port": 5432, "url": nil, "internal": "myhangar:5432"},
					},
					"total":     2,
					"truncated": false,
				},
			},
			{
				Title:   "list one in another team",
				Command: "outplane hangar port list myhangar --team acme",
				Argv:    []string{"outplane", "hangar", "port", "list", "myhangar", "--team", "acme"},
				Placeholders: map[string]string{
					"myhangar": "<HANGAR_NAME>",
					"acme":     "<TEAM_SLUG>",
				},
				Risk: RiskRead,
			},
		},

		AutomationNotes: []string{
			"url is null for a private port, which is not an error. internal is set on every " +
				"port: the team's applications reach the Hangar at name:port whether the port is " +
				"public or not.",
			"A public tcp port's url is a host and a port number, not a URL with a scheme. The " +
				"outside port number comes from a shared pool and is not the one the dev server " +
				"listens on.",
			"A public port is open to everyone on the internet. A Hangar takes no IP access " +
				"profile and no custom domain.",
			hangarPortsListenNote,
			"The same ports are in `hangar get` and `hangar list` under ports. An empty list " +
				"means nothing outside the Hangar reaches its dev servers.",
		},

		Related: []string{"hangar port set", "hangar port unset", "hangar get", "port list"},
		DocsURL: "https://docs.outplane.com/cli/hangar",
	}
}

func hangarPortSet() Command {
	return Command{
		Path:  []string{"hangar", "port", "set"},
		Short: "open a port on a Hangar, or change one that is already open",
		Long: "Opens ports on a Hangar, or changes the ones already open, so that a dev server " +
			"running in it can be reached.\n\n" +
			"A part left out of the argument keeps what the port already had, so " +
			"`hangar port set myhangar 3000` on a public port leaves it public. On a port that " +
			"is not open yet there is nothing to keep and the defaults apply: private, HTTP. " +
			"Ports not named are left exactly as they are.\n\n" +
			"A Hangar can have up to 3 ports open, and never 7070, which is its own.\n\n" +
			"The change is live within seconds and the Hangar is not restarted: its terminal " +
			"session and its agents keep running.",

		Risk:         RiskWrite,
		RequiresAuth: true,
		Session:      SessionAny,
		Idempotent:   true,

		SupportsDryRun: true,

		APICalls: []string{
			"GET /api/Hangar/GetHangarsByTeamId",
			"PUT /api/Hangar/UpdateHangarPorts/{hangarId}",
		},

		Args: []Arg{hangarArg(), portSpecArg()},

		OutputFields: hangarPortChangeFields(),

		ErrorCodes: []string{
			"hangar.port_invalid",
			"hangar.scheme_invalid",
			"hangar.port_duplicate",
			"hangar.port_reserved",
			"hangar.port_limit",
			"hangar.never_started",
			"hangar.ports_not_applied",
			"hangar.not_found",
			"hangar.ambiguous",
			"usage.bad_port",
			"usage.missing_argument",
			"usage.empty_argument",
			"context.no_team",
		},
		ExitCodes: []int{0, 2, 3, 5, 8},

		Examples: []Example{
			{
				Title:        "open a public HTTP port for a dev server",
				Command:      "outplane hangar port set myhangar 3000:http:public",
				Argv:         []string{"outplane", "hangar", "port", "set", "myhangar", "3000:http:public"},
				Placeholders: map[string]string{"myhangar": "<HANGAR_NAME>"},
				Risk:         RiskWrite,
			},
			{
				Title:        "open several at once",
				Command:      "outplane hangar port set myhangar 3000:http:public 5432:tcp",
				Argv:         []string{"outplane", "hangar", "port", "set", "myhangar", "3000:http:public", "5432:tcp"},
				Placeholders: map[string]string{"myhangar": "<HANGAR_NAME>"},
				Risk:         RiskWrite,
			},
			{
				Title:        "make an open port private without touching its scheme",
				Command:      "outplane hangar port set myhangar 3000::private",
				Argv:         []string{"outplane", "hangar", "port", "set", "myhangar", "3000::private"},
				Placeholders: map[string]string{"myhangar": "<HANGAR_NAME>"},
				Risk:         RiskWrite,
			},
			{
				Title:        "see the whole set that would be sent",
				Command:      "outplane hangar port set myhangar 8080 --dry-run --json",
				Argv:         []string{"outplane", "hangar", "port", "set", "myhangar", "8080", "--dry-run", "--json"},
				Placeholders: map[string]string{"myhangar": "<HANGAR_NAME>"},
				Risk:         RiskRead,
				OutputSample: map[string]any{
					"action":  "set",
					"numbers": []any{8080},
					"hangar":  "myhangar",
					"serving": 2,
					"ports": []any{
						map[string]any{"port": 3000, "scheme": "http", "public": true, "url": nil, "internal": "myhangar:3000"},
						map[string]any{"port": 8080, "scheme": "http", "public": false, "url": nil, "internal": "myhangar:8080"},
					},
					"changed": false,
				},
			},
		},

		AutomationNotes: []string{
			"A part left out keeps what the port already had. `hangar port set myhangar 3000` " +
				"does not make a public port private; `3000::private` does. On a port that is not " +
				"open yet, an omitted part takes the default of private HTTP.",
			hangarPortsLiveNote,
			"A Hangar can have at most 3 ports open, and 7070 is its own and never one of them. " +
				"Both are checked on the whole set before anything is sent, as are the port range, " +
				"1 to 65535, and the scheme.",
			hangarPortsReplaceNote,
			"url in the result is the address the server assigned, and null under --dry-run. " +
				"Making a tcp port public allocates an outside port number from a shared pool.",
			hangarPortsListenNote,
			"Exit 8 with hangar.ports_not_applied means the ports were saved and did not reach " +
				"the Hangar. It is retryable, and the same command again applies them.",
			"A Hangar that never started answers exit 5 with hangar.never_started. The ports " +
				"are saved all the same, but there is nothing to route them to, and deleting it " +
				"and creating it again is the way back.",
		},

		Related: []string{"hangar port list", "hangar port unset", "hangar get", "hangar connect", "port set"},
		DocsURL: "https://docs.outplane.com/cli/hangar",
	}
}

func hangarPortUnset() Command {
	return Command{
		Path:  []string{"hangar", "port", "unset"},
		Short: "close a port on a Hangar",
		Long: "Closes ports on a Hangar.\n\n" +
			"Every number is checked before anything is sent, so a typo in the third one does " +
			"not leave the first two closed. Ports not named are left exactly as they are.\n\n" +
			"The change is live within seconds and the Hangar is not restarted: its terminal " +
			"session and its agents keep running.",

		Risk:         RiskWrite,
		RequiresAuth: true,
		Session:      SessionAny,
		Idempotent:   true,

		SupportsDryRun: true,

		APICalls: []string{
			"GET /api/Hangar/GetHangarsByTeamId",
			"PUT /api/Hangar/UpdateHangarPorts/{hangarId}",
		},

		Args: []Arg{
			hangarArg(),
			{
				Name:     "port",
				Short:    "the port number, repeatable",
				Required: true,
				Variadic: true,
			},
		},

		OutputFields: hangarPortChangeFields(),

		ErrorCodes: []string{
			"hangar.port_not_found",
			"hangar.scheme_invalid",
			"hangar.never_started",
			"hangar.ports_not_applied",
			"hangar.not_found",
			"hangar.ambiguous",
			"usage.bad_port",
			"usage.missing_argument",
			"usage.empty_argument",
			"context.no_team",
		},
		ExitCodes: []int{0, 2, 3, 5, 8},

		Examples: []Example{
			{
				Title:        "see what would be left",
				Command:      "outplane hangar port unset myhangar 8080 --dry-run",
				Argv:         []string{"outplane", "hangar", "port", "unset", "myhangar", "8080", "--dry-run"},
				Placeholders: map[string]string{"myhangar": "<HANGAR_NAME>"},
				Risk:         RiskRead,
			},
			{
				Title:        "close a port",
				Command:      "outplane hangar port unset myhangar 8080",
				Argv:         []string{"outplane", "hangar", "port", "unset", "myhangar", "8080"},
				Placeholders: map[string]string{"myhangar": "<HANGAR_NAME>"},
				Risk:         RiskWrite,
			},
			{
				Title:        "close several and read the result in a pipeline",
				Command:      "outplane hangar port unset myhangar 8080 9000 --json --fields numbers,serving,changed",
				Argv:         []string{"outplane", "hangar", "port", "unset", "myhangar", "8080", "9000", "--json", "--fields", "numbers,serving,changed"},
				Placeholders: map[string]string{"myhangar": "<HANGAR_NAME>"},
				Risk:         RiskWrite,
			},
		},

		AutomationNotes: []string{
			"A number the Hangar has no port open on is an error, exit 5 with " +
				"hangar.port_not_found, and nothing is sent. The error names the ports that are " +
				"open.",
			"Closing every port is allowed. The Hangar keeps running, and nothing outside it " +
				"reaches its dev servers.",
			hangarPortsLiveNote,
			hangarPortsReplaceNote,
			"Exit 8 with hangar.ports_not_applied means the change was saved and did not reach " +
				"the Hangar. It is not retryable as it stands: the ports named are already closed " +
				"in the saved set, so the same command exits 5. `hangar port set` with a port " +
				"still open sends the whole set again and applies it.",
		},

		Related: []string{"hangar port list", "hangar port set", "hangar get", "port unset"},
		DocsURL: "https://docs.outplane.com/cli/hangar",
	}
}
