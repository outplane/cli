package registry

// Build logs.
//
// A separate command from `outplane logs` on purpose. Build output belongs to
// one deployment, not to an application: the argument is a deployment id, the
// source is the API rather than the log gateway, and the text is one long
// stream rather than per-line records with fields to filter on. Folding the two
// together would mean one command whose argument, source and output shape all
// change with a flag.

func init() {
	Register(deployLogs())
}

func deployLogs() Command {
	return Command{
		Path:  []string{"deploy", "logs"},
		Short: "show the build output of one deployment",
		Long: "Prints what the build produced for a single deployment.\n\n" +
			"This is the build, not the running application. For the output of the " +
			"application itself, use `outplane logs`.\n\n" +
			"Only applications built from a repository have build output. A " +
			"deployment of an image that was already built has none, and this says so " +
			"rather than printing nothing.",

		Risk:         RiskRead,
		RequiresAuth: true,
		Session:      SessionAny,
		Idempotent:   true,

		LongRunning: true,
		Streams:     StreamLines,

		APICalls: []string{"GET /api/AppDeployment/GetAppDeploymentBuildLogs/{deploymentId}/{offset}"},

		Args: []Arg{
			{
				Name:     "deployment",
				Short:    "deployment id, as reported by `outplane deploy create`",
				Required: true,
			},
		},

		Flags: []Flag{
			{
				Name:        "follow",
				Short:       "f",
				Type:        "bool",
				Default:     "false",
				Description: "keep printing until the build finishes",
			},
			{
				Name:        "timeout",
				Type:        "duration",
				Default:     "20m",
				Description: "how long --follow waits before giving up",
			},
		},

		// The output is the log text itself, written as it arrives. There are
		// no fields, and declaring any would promise a structure this does not
		// have.
		OutputFields: nil,

		ErrorCodes: []string{"deploy.no_build", "deploy.logs_gone", "app.not_found"},
		ExitCodes:  []int{0, 2, 3, 5, 8, 124},

		Examples: []Example{
			{
				Title:        "read a finished build",
				Command:      "outplane deploy logs 4821",
				Argv:         []string{"outplane", "deploy", "logs", "4821"},
				Placeholders: map[string]string{"4821": "<DEPLOYMENT_ID>"},
				Risk:         RiskRead,
			},
			{
				Title:        "watch a build that is still running",
				Command:      "outplane deploy logs 4821 --follow",
				Argv:         []string{"outplane", "deploy", "logs", "4821", "--follow"},
				Placeholders: map[string]string{"4821": "<DEPLOYMENT_ID>"},
				Risk:         RiskRead,
			},
			{
				Title:        "search a finished build's output",
				Command:      "outplane deploy logs 4821 -o text",
				Argv:         []string{"outplane", "deploy", "logs", "4821", "-o", "text"},
				Placeholders: map[string]string{"4821": "<DEPLOYMENT_ID>"},
				Risk:         RiskRead,
			}},

		AutomationNotes: []string{
			"Output is the build text, written to stdout as it arrives. It is not JSON, " +
				"and --json does not change that: there is no record structure to report.",
			"Build output lives with the build machine and is removed after it is cleaned " +
				"up, so an old deployment may have none left. That is reported, not treated " +
				"as an empty build.",
			"A deployment of an already-built image never had a build, so it has no output.",
			"Without --follow this returns whatever exists right now, which for a running " +
				"build is a partial log.",
			"This is the build. `outplane logs` is the running application.",
		},

		Related: []string{"deploy create", "logs", "app list"},
		DocsURL: "https://docs.outplane.com/cli/deploy",
	}
}
