package registry

// The escape hatch.
//
// Declared apart from every group because it belongs to none: it is the one
// command whose subject is the API rather than a resource. Its declaration is
// unusual in three ways, and each one follows from that.
//
// It declares no output fields. The shape of the answer belongs to whichever
// endpoint was called, so --fields has nothing to select from and says so.
//
// It is marked destructive although most uses are reads. Risk is published for
// a harness to gate on, and the honest answer for a command that can call
// `DELETE /App/Delete/{id}` is the strongest one. The command narrows it at
// runtime instead: a read runs freely, a write is acknowledged with --yes, and
// a write under an agent harness is refused with the same exit 4 the resource
// commands use.
//
// Its examples are read-only apart from one. An example that deletes something
// is an example somebody will paste.

func init() {
	Register(rawAPI())
}

func rawAPI() Command {
	return Command{
		Path:  []string{"api"},
		Short: "call an API endpoint the CLI has no command for",
		Long: "Sends a request to the Out Plane API and prints what comes back.\n\n" +
			"It exists so that a missing command is an inconvenience rather than a wall. " +
			"Everything the ordinary commands provide still applies: the credential comes " +
			"from the same place, the team header is sent, failures map to the same exit " +
			"codes. What it cannot provide is judgement, because it has never seen the " +
			"endpoint being called.\n\n" +
			"The path is the part after /api. A leading slash is optional and an /api " +
			"prefix copied out of a browser is removed, so both forms work.\n\n" +
			"A read runs like any other read. Anything else needs --yes, and is refused " +
			"under an agent harness: the commands that delete things refuse there too, and " +
			"reaching the same endpoint through here would be that act with none of the " +
			"checks.",

		Risk:         RiskDestructive,
		RequiresAuth: true,
		Session:      SessionAny,

		// Whether a call can be repeated safely is a property of the endpoint,
		// which this command cannot know.
		Idempotent: false,

		SupportsDryRun: true,

		APICalls: []string{"whatever is asked for"},

		Args: []Arg{
			{
				Name:     "method",
				Short:    "GET, POST, PUT, PATCH or DELETE",
				Required: true,
			},
			{
				Name:     "path",
				Short:    "the part after /api, such as /App/GetAppsByTeamId",
				Required: true,
			},
		},

		Flags: []Flag{
			{
				Name: "data", Short: "d", Type: "string",
				Description: "JSON body. @file.json reads a file, @- reads standard input",
			},
			{
				Name: "query", Type: "strings", Repeatable: true,
				Description: "key=value, repeatable. Safer than putting & in a shell argument",
			},
			{
				Name: "raw", Type: "bool", Default: "false",
				Description: "print the whole response envelope instead of its data",
			},
			{
				// Redeclared rather than inherited: a global flag reaches the
				// tree but not the handler, which reads only what its own
				// declaration lists. The destructive commands redeclare it for
				// the same reason, and narrowing the wording is the point.
				Name: "yes", Short: "y", Type: "bool", Default: "false",
				Description: "acknowledge a call that is not a read. Refused under an agent harness whatever it says",
			},
		},

		// No output fields on purpose: the answer's shape is the endpoint's,
		// and --fields is refused with a message that says why.

		ErrorCodes: []string{
			"api.method_invalid",
			"api.path_required",
			"api.path_invalid",
			"api.query_invalid",
			"api.body_invalid",
			"api.body_not_allowed",
			"api.body_unreadable",
			"confirmation.required",
			"usage.missing_argument",
		},
		ExitCodes: []int{0, 1, 2, 3, 4, 5, 6, 7, 8},

		Examples: []Example{
			{
				Title:   "read an endpoint the CLI has no command for",
				Command: "outplane api GET /App/GetAppsByTeamId",
				Argv:    []string{"outplane", "api", "GET", "/App/GetAppsByTeamId"},
				Risk:    RiskRead,
			},
			{
				Title:   "pass query parameters without fighting the shell",
				Command: "outplane api GET /AppDeployment/GetAppDeployments --query appId=<APP_ID> --query page=1",
				Argv: []string{"outplane", "api", "GET", "/AppDeployment/GetAppDeployments",
					"--query", "appId=<APP_ID>", "--query", "page=1"},
				Placeholders: map[string]string{"<APP_ID>": "<APP_ID>"},
				Risk:         RiskRead,
			},
			{
				Title:   "see the whole envelope rather than its data",
				Command: "outplane api GET /App/GetAppsByTeamId --raw",
				Argv:    []string{"outplane", "api", "GET", "/App/GetAppsByTeamId", "--raw"},
				Risk:    RiskRead,
				OutputSample: map[string]any{
					"data":         []any{},
					"statusCode":   200,
					"isSuccessful": true,
				},
			},
			{
				Title:   "check what a write would send, without sending it",
				Command: "outplane api POST /AppSetting/AddEnvironmentVariables/<APP_ID> --data '{\"environmentVariables\":{\"A\":\"1\"}}' --dry-run",
				Argv: []string{"outplane", "api", "POST", "/AppSetting/AddEnvironmentVariables/<APP_ID>",
					"--data", "{\"environmentVariables\":{\"A\":\"1\"}}", "--dry-run"},
				Risk: RiskRead,
			},
			{
				Title:   "send a body too large for a command line",
				Command: "cat body.json | outplane api POST /Some/Endpoint --data @- --yes",
				Argv: []string{"outplane", "api", "POST", "/Some/Endpoint",
					"--data", "@-", "--yes"},
				Risk: RiskDestructive,
			},
		},

		AutomationNotes: []string{
			"This command has no output fields, because the shape of the answer belongs " +
				"to the endpoint. --fields is refused with usage.no_fields. The output is " +
				"one JSON document: the envelope's data, or the whole envelope with --raw.",
			"An endpoint that returns nothing prints null, so the output is always one " +
				"JSON document and never an empty stream.",
			"A write is refused under an agent harness with exit 4 and " +
				"confirmation.required, whatever flags are given. The commands that name " +
				"what they change are the way to make a change from an agent, and they " +
				"exist for everything the CLI covers; `outplane schema` lists them.",
			"Outside a harness, anything other than GET needs --yes. There is no " +
				"--confirm-name here, because this command cannot tell which resource is " +
				"being changed or what it is called.",
			"The path is joined to the configured API address and cannot be a full URL, so " +
				"this command cannot be pointed at another host.",
			"Failures map to the same exit codes as every other command: 3 for a rejected " +
				"credential, 5 for not found, 7 for a plan limit, 8 for a server failure. " +
				"The response body of a failure travels in the error's details.",
			"A body must be JSON. It is checked before the request is sent, so a typo " +
				"fails with api.body_invalid rather than as a server-side 400.",
		},

		Related: []string{"schema", "app list", "status", "whoami"},
		DocsURL: "https://docs.outplane.com/cli/api",
	}
}
