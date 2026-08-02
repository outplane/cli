package registry

// The interactive shell.
//
// It lives in its own file rather than with the other application commands
// because it is the one command that is not a request: it opens a terminal on a
// running instance and keeps it open. Everything unusual about the declaration
// below follows from that.
//
//   - It cannot be piped, redirected or scripted. A session needs a keyboard at
//     one end and a screen at the other, and without both it refuses rather
//     than opening something nothing can close.
//   - It has no exit status to report. The platform attaches a terminal to
//     every session, and a terminal reports none; the server turns the exec's
//     own status into a line of text on the way past. A command that failed and
//     one that succeeded end the same way.
//   - Its output fields only ever appear under --dry-run. A live session writes
//     the far end's bytes and nothing else.

func init() {
	Register(appShell())
}

func appShell() Command {
	return Command{
		Path:  []string{"app", "shell"},
		Short: "open an interactive shell on a running instance",
		Long: "Opens a terminal on one running instance of an application.\n\n" +
			"This is a real session, not a command runner: the local terminal is handed " +
			"over to a process on the instance, so an editor, a prompt and Ctrl-C all " +
			"behave as they would if you were sitting in front of it. It needs a " +
			"terminal at both ends and cannot be piped, redirected or run from a script.\n\n" +
			"An application running several instances has several shells, and they share " +
			"nothing but the image. A file written in one is in one, and it is gone when " +
			"that instance restarts. Nothing typed here changes the application's " +
			"configuration, so the next deployment replaces all of it.\n\n" +
			"The command given to --command is run directly, not through a shell. Shell " +
			"syntax needs an explicit `sh -c \"...\"`.",

		Risk:         RiskWrite,
		RequiresAuth: true,
		Session:      SessionAny,

		// A session is not repeatable in any useful sense: run it twice and
		// there are two terminals, each having done whatever was typed into it.
		Idempotent: false,

		LongRunning:    true,
		SupportsDryRun: true,

		// No output override. A live session writes its own bytes and never
		// reaches the renderer at all, so the tool-wide default governs the one
		// case that does reach it: --dry-run, which is JSON in a pipe like
		// everything else, because a pipe is where something is reading it.

		APICalls: []string{
			"GET /api/App/GetAppsByTeamId",
			"GET /api/App/GetInstances/{appId}",
			"GET /api/AppShell/Connect/{appId} (WebSocket)",
		},

		Args: []Arg{
			{
				Name:     "app",
				Short:    "app name or id. Defaults to the linked app",
				Required: false,
				Resolves: "app",
			},
		},

		Flags: []Flag{
			{
				Name: "instance",
				Type: "string",
				Description: "which instance to open. Defaults to the first ready one, " +
					"or a running one when none is ready",
			},
			{
				Name:    "command",
				Type:    "string",
				Default: "sh",
				Description: "what to run instead of the default shell. Executed directly, " +
					"so shell syntax needs sh -c \"...\"",
			},
		},

		OutputFields: []Field{
			{Name: "app", Type: "string"},
			{Name: "appId", Type: "string"},
			{Name: "instance", Type: "string", Description: "the instance the session would open on"},
			{Name: "command", Type: "string", Description: "what would run, including the default"},
			{
				Name:        "connected",
				Type:        "bool",
				Description: "always false: only --dry-run produces these fields",
			},
		},

		ErrorCodes: []string{
			"shell.not_interactive",
			"shell.no_instance",
			"shell.instance_not_found",
			"shell.disconnected",
			"app.not_found",
			"context.no_app",
		},
		ExitCodes: []int{0, 1, 2, 3, 5, 8, 130},

		Examples: []Example{
			{
				Title:        "open a shell on an application",
				Command:      "outplane app shell checkout",
				Argv:         []string{"outplane", "app", "shell", "checkout"},
				Placeholders: map[string]string{"checkout": "<APP_NAME>"},
				Risk:         RiskWrite,
			},
			{
				Title:   "open one on a particular instance",
				Command: "outplane app shell checkout --instance checkout-7d9f-abcde",
				Argv: []string{"outplane", "app", "shell", "checkout",
					"--instance", "checkout-7d9f-abcde"},
				Placeholders: map[string]string{
					"checkout":            "<APP_NAME>",
					"checkout-7d9f-abcde": "<INSTANCE_NAME>",
				},
				Risk: RiskWrite,
			},
			{
				Title:        "use bash, on an image that has it",
				Command:      "outplane app shell checkout --command bash",
				Argv:         []string{"outplane", "app", "shell", "checkout", "--command", "bash"},
				Placeholders: map[string]string{"checkout": "<APP_NAME>"},
				Risk:         RiskWrite,
			},
			{
				Title:        "check what a session would open, without opening one",
				Command:      "outplane app shell checkout --dry-run --json",
				Argv:         []string{"outplane", "app", "shell", "checkout", "--dry-run", "--json"},
				Placeholders: map[string]string{"checkout": "<APP_NAME>"},
				Risk:         RiskRead,
				OutputSample: map[string]any{
					"app":       "checkout",
					"instance":  "checkout-7d9f-abcde",
					"command":   "sh",
					"connected": false,
				},
			},
		},

		AutomationNotes: []string{
			"This command requires a terminal on both standard input and standard output. " +
				"Behind a pipe, a redirect or an agent harness it exits 2 with " +
				"shell.not_interactive and opens nothing. --dry-run is the only form " +
				"that runs anywhere.",
			"There is no exit status. The platform attaches a terminal to the session and " +
				"a terminal reports none, so a command that failed inside the session " +
				"still ends it with exit 0. Nothing here can be used to decide whether " +
				"something worked.",
			"Output is the far end's own bytes, including escape sequences and colour. It " +
				"is not records, and --json, --fields and --jq do not apply to a live " +
				"session.",
			"An instance name changes every time it restarts, so one read a minute ago may " +
				"already be gone. Without --instance the first ready instance is chosen, " +
				"and which one that is can differ between two runs a second apart.",
			"Changes made inside a session live on that one instance until it restarts. " +
				"They are not configuration, they are not replicated to the other " +
				"instances, and the next deployment replaces the lot.",
			"An image with no shell fails the exec, and the reason arrives as text inside " +
				"the session rather than as an error: by then the socket is open and " +
				"there is nowhere else to put it.",
		},

		Related: []string{"app instances", "logs", "app get", "app scale"},
		DocsURL: "https://docs.outplane.com/cli/app",
	}
}
