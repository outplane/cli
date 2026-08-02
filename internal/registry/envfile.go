package registry

// Environment variables and a file.
//
// The three commands here connect a `.env` to an application, and the shape of
// each one comes from a decision recorded in internal/commands/envfile.go: this
// pair is not a synchronisation. `pull` overwrites the file, `push` adds to the
// application, and neither deletes on the far side, because the two ends are
// not copies of each other. Removal stays where it can be seen, one name at a
// time, in `env unset`.
//
// `run` exists because it is what most people wanted `pull` for: a local
// process that sees the deployed configuration. It never writes the values
// anywhere, which makes it the safer of the two by a wide margin.

func init() {
	Register(
		envPull(),
		envPush(),
		envRun(),
	)
}

// envFileArg is the same optional path in pull and push.
func envFileArg() Arg {
	return Arg{
		Name:     "file",
		Short:    "path to the file. Defaults to .env",
		Required: false,
	}
}

func envPull() Command {
	return Command{
		Path:  []string{"env", "pull"},
		Short: "write an application's environment variables to a file",
		Long: "Writes the variables set on an application to a local file, in .env format.\n\n" +
			"The file holds real values in the clear, which is the point of it and also its " +
			"whole risk. It is written readable by its owner only, and it belongs in " +
			".gitignore.\n\n" +
			"An existing file is not overwritten. Whatever is in it may be a local edit " +
			"rather than a stale copy, and nothing here can tell the difference, so --force " +
			"is required to replace one.\n\n" +
			"Variables the application receives from a shared group are not included. They " +
			"belong to the group, and writing them here would invite pushing a shared value " +
			"back onto one application.",

		Risk:         RiskRead,
		RequiresAuth: true,
		Session:      SessionAny,
		Idempotent:   true,

		SupportsDryRun: true,

		APICalls: []string{
			"GET /api/App/GetAppsByTeamId",
			"GET /api/AppSetting/GetEnvironmentVariables/{appId}",
			"GET /api/EnvVariableGroup/GetAssignedGroupsByAppId/{appId}",
		},

		Args: []Arg{envFileArg()},

		Flags: []Flag{
			appFlag(),
			{
				Name:        "force",
				Type:        "bool",
				Default:     "false",
				Description: "replace the file if it already exists",
			},
		},

		OutputFields: []Field{
			{Name: "action", Type: "string", Enum: []string{"pull"}},
			{Name: "file", Type: "string", Description: "the path that was written"},
			{Name: "app", Type: "string"},
			{Name: "appId", Type: "string"},
			{Name: "variables", Type: "int", Description: "how many were written"},
			{
				Name:        "keys",
				Type:        "array",
				Description: "the names that were written, sorted. Never the values",
			},
			{Name: "written", Type: "bool", Description: "false for a dry run"},
			{Name: "deploymentId", Type: "int | null", Description: "always null here"},
		},

		ErrorCodes: []string{
			"env.file_exists",
			"env.file_unwritable",
			"env.file_unreadable",
			"env.file_invalid",
			"context.no_app",
			"app.not_found",
		},
		ExitCodes: []int{0, 1, 2, 3, 5, 6, 8},

		Examples: []Example{
			{
				Title:   "write the linked application's variables to .env",
				Command: "outplane env pull",
				Argv:    []string{"outplane", "env", "pull"},
				Risk:    RiskRead,
			},
			{
				Title:        "write another application's to a named file",
				Command:      "outplane env pull .env.production --app checkout",
				Argv:         []string{"outplane", "env", "pull", ".env.production", "--app", "checkout"},
				Placeholders: map[string]string{"checkout": "<APP_NAME>"},
				Risk:         RiskRead,
			},
			{
				Title:   "replace a file that is already there",
				Command: "outplane env pull .env --force",
				Argv:    []string{"outplane", "env", "pull", ".env", "--force"},
				Risk:    RiskRead,
			},
			{
				Title:   "check what would be written, and where",
				Command: "outplane env pull --dry-run --json",
				Argv:    []string{"outplane", "env", "pull", "--dry-run", "--json"},
				Risk:    RiskRead,
				OutputSample: map[string]any{
					"action":    "pull",
					"file":      ".env",
					"app":       "checkout",
					"variables": 12,
					"written":   false,
				},
			},
		},

		AutomationNotes: []string{
			"Values are written to the file and never to this command's own output. keys " +
				"lists the names; there is no flag that adds the values to the result.",
			"The file is created with owner-only permissions, replacing it atomically, so a " +
				"failure part way through leaves the previous file intact rather than half " +
				"of a new one.",
			"An existing file is a refusal, exit 6 with env.file_exists, not a silent " +
				"overwrite. --force replaces it.",
			"Variables from an assigned group are not included, so a pulled file can hold " +
				"fewer variables than the running application has. What is missing is " +
				"reported on stderr and read with `outplane env group get`.",
			"Values are written in .env format, which means a value containing a newline, a " +
				"quote or a leading space comes back quoted. Reading it with `env push` " +
				"returns exactly what was pulled.",
		},

		Related: []string{"env push", "env run", "env list", "env group get"},
		DocsURL: "https://docs.outplane.com/cli/env",
	}
}

func envPush() Command {
	return Command{
		Path:  []string{"env", "push"},
		Short: "set an application's environment variables from a file",
		Long: "Reads a .env file and sets what it contains on an application.\n\n" +
			"Only what differs is sent, and the report says how much was new, how much " +
			"changed, and how much was already the same.\n\n" +
			"Nothing is removed. A variable set on the application and absent from the file " +
			"stays exactly as it is, because a file is one person's view of the " +
			"configuration and deleting by omission would remove a colleague's variable " +
			"without either of them mentioning it. Removal is `outplane env unset`.\n\n" +
			"The running application keeps its old values until it is deployed again. " +
			"--deploy does that immediately.",

		Risk:         RiskWrite,
		RequiresAuth: true,
		Session:      SessionAny,
		Idempotent:   true,

		SupportsDryRun: true,

		APICalls: []string{
			"GET /api/AppSetting/GetEnvironmentVariables/{appId}",
			"POST /api/AppSetting/AddEnvironmentVariables/{appId}",
			"POST /api/AppDeployment/CreateAppDeployment/{appId}",
		},

		Args: []Arg{envFileArg()},

		Flags: []Flag{
			appFlag(),
			{
				Name:        "deploy",
				Type:        "bool",
				Default:     "false",
				Description: "deploy afterwards, so the change reaches the running app",
			},
		},

		OutputFields: []Field{
			{Name: "action", Type: "string", Enum: []string{"push"}},
			{Name: "file", Type: "string", Description: "the path that was read"},
			{Name: "app", Type: "string"},
			{Name: "appId", Type: "string"},
			{Name: "added", Type: "int", Description: "keys the application did not have"},
			{Name: "changed", Type: "int", Description: "keys whose value differed"},
			{Name: "unchanged", Type: "int", Description: "keys that were already identical, and were not sent"},
			{Name: "addedKeys", Type: "array"},
			{Name: "changedKeys", Type: "array"},
			{Name: "unchangedKeys", Type: "array"},
			{
				Name:        "sent",
				Type:        "bool",
				Description: "false for a dry run, and false when nothing differed",
			},
			{
				Name:        "deploymentId",
				Type:        "int | null",
				Description: "the deployment --deploy started, or null when it was not given",
			},
		},

		ErrorCodes: []string{
			"env.file_not_found",
			"env.file_invalid",
			"env.file_unreadable",
			"env.reserved_key",
			"env.reserved_prefix",
			"env.key_too_long",
			"env.value_too_long",
			"env.too_many",
			"context.no_app",
			"app.not_found",
		},
		ExitCodes: []int{0, 1, 2, 3, 5, 8},

		Examples: []Example{
			{
				Title:   "see what a file would change, without changing it",
				Command: "outplane env push --dry-run",
				Argv:    []string{"outplane", "env", "push", "--dry-run"},
				Risk:    RiskRead,
				OutputSample: map[string]any{
					"action":      "push",
					"file":        ".env",
					"added":       2,
					"changed":     1,
					"unchanged":   9,
					"addedKeys":   []any{"CACHE_URL", "LOG_LEVEL"},
					"changedKeys": []any{"TIMEOUT"},
					"sent":        false,
				},
			},
			{
				Title:   "apply it",
				Command: "outplane env push",
				Argv:    []string{"outplane", "env", "push"},
				Risk:    RiskWrite,
			},
			{
				Title:        "apply a named file to a named application, and deploy",
				Command:      "outplane env push .env.production --app checkout --deploy",
				Argv:         []string{"outplane", "env", "push", ".env.production", "--app", "checkout", "--deploy"},
				Placeholders: map[string]string{"checkout": "<APP_NAME>"},
				Risk:         RiskWrite,
			},
			{
				Title:   "read the result in a pipeline",
				Command: "outplane env push --json --fields added,changed,sent",
				Argv:    []string{"outplane", "env", "push", "--json", "--fields", "added,changed,sent"},
				Risk:    RiskWrite,
			},
		},

		AutomationNotes: []string{
			"This is not a synchronisation. A key set on the application and missing from " +
				"the file is left alone; nothing here removes anything. `outplane env unset` " +
				"removes, by name.",
			"Only keys that are new or whose value differs are sent. sent is false when " +
				"every key already matched, and that is a success, not a failure. A file " +
				"with no variables in it is the same case: nothing is sent, nothing is " +
				"removed, and the exit code is 0.",
			"Keys are compared the way the server compares them, ignoring case, so a file " +
				"with `path=` changes an existing PATH rather than adding a second variable.",
			"Saving does not restart anything. Without --deploy the running application " +
				"keeps the values it started with.",
			"The whole file is validated before anything is sent: a reserved name or an " +
				"over-long value fails naming the line, and no part of the file is applied.",
			"An unquoted # is part of the value, not the start of a comment. A password " +
				"ending in #1 survives a round trip; a trailing comment does not work.",
			"deploymentId is null unless --deploy was given. A queued deployment is not a " +
				"finished one; follow it with `outplane deploy logs`.",
		},

		Related: []string{"env pull", "env set", "env unset", "deploy create"},
		DocsURL: "https://docs.outplane.com/cli/env",
	}
}

func envRun() Command {
	return Command{
		Path:  []string{"env", "run"},
		Short: "run a local command with an application's environment variables",
		Long: "Runs a program on this machine with the application's variables in its " +
			"environment.\n\n" +
			"Nothing is written to disk. The values exist for as long as the program runs, " +
			"which makes this the safer way to work against the deployed configuration and " +
			"the reason `env pull` should be the second choice.\n\n" +
			"The program is executed directly, never through a shell, so `env run -- echo " +
			"$HOME` passes a literal dollar sign and expands nothing. For a pipeline, a " +
			"redirect or a shell built-in, ask for a shell: `env run -- sh -c \"...\"`.\n\n" +
			"The local environment is inherited and the application's variables are laid " +
			"over it, so PATH and everything else a program needs to start still exist. " +
			"--pure drops the local environment and leaves only the application's.",

		Risk:         RiskWrite,
		RequiresAuth: true,
		Session:      SessionAny,

		// Whether running it twice is safe is a property of the program being
		// run, and this command cannot know or promise anything about that.
		Idempotent: false,

		LongRunning:    true,
		SupportsDryRun: true,

		APICalls: []string{
			"GET /api/App/GetAppsByTeamId",
			"GET /api/AppSetting/GetEnvironmentVariables/{appId}",
		},

		Args: []Arg{
			{
				Name:     "command",
				Short:    "the program to run and its arguments, after --",
				Required: true,
				Variadic: true,
			},
		},

		Flags: []Flag{
			appFlag(),
			{
				Name:    "pure",
				Type:    "bool",
				Default: "false",
				Description: "start from an empty environment instead of this shell's, " +
					"so the program sees only the application's variables",
			},
		},

		OutputFields: []Field{
			{Name: "app", Type: "string"},
			{Name: "appId", Type: "string"},
			{Name: "command", Type: "string", Description: "what was run, as one line"},
			{Name: "argv", Type: "array", Description: "the same thing, unjoined"},
			{Name: "variables", Type: "int", Description: "how many were placed in the environment"},
			{Name: "keys", Type: "array", Description: "their names. Never the values"},
		},

		ErrorCodes: []string{
			"env.command_not_found",
			"env.command_not_executable",
			"env.run_failed",
			"usage.missing_argument",
			"context.no_app",
			"app.not_found",
		},

		// These are the codes this command produces on its own, before the
		// program starts. Once it has started the exit code is the program's
		// and cannot be enumerated, which the automation notes say plainly
		// because the field cannot.
		ExitCodes: []int{0, 1, 2, 3, 5, 8},

		Examples: []Example{
			{
				Title:   "run a local server against the deployed configuration",
				Command: "outplane env run -- npm start",
				Argv:    []string{"outplane", "env", "run", "--", "npm", "start"},
				Risk:    RiskWrite,
			},
			{
				Title:        "run a one-off task against another application",
				Command:      "outplane env run --app checkout -- python manage.py migrate",
				Argv:         []string{"outplane", "env", "run", "--app", "checkout", "--", "python", "manage.py", "migrate"},
				Placeholders: map[string]string{"checkout": "<APP_NAME>"},
				Risk:         RiskWrite,
			},
			{
				Title:   "use shell syntax, by asking for a shell",
				Command: "outplane env run -- sh -c 'echo \"$DATABASE_URL\" | cut -d@ -f2'",
				Argv:    []string{"outplane", "env", "run", "--", "sh", "-c", "echo \"$DATABASE_URL\" | cut -d@ -f2"},
				Risk:    RiskWrite,
			},
			{
				Title:   "check which variables a command would get, without running it",
				Command: "outplane env run --dry-run --json -- npm test",
				Argv:    []string{"outplane", "env", "run", "--dry-run", "--json", "--", "npm", "test"},
				Risk:    RiskRead,
				OutputSample: map[string]any{
					"app":       "checkout",
					"command":   "npm test",
					"variables": 12,
					"keys":      []any{"DATABASE_URL", "LOG_LEVEL"},
				},
			},
		},

		AutomationNotes: []string{
			"The exit code is the program's own, whatever it is, so this command's exit " +
				"code is not from the CLI's table when the program actually ran. A program " +
				"exiting 2 is not a usage error here.",
			"A program killed by a signal reports 128 plus the signal number, as a shell " +
				"does, so an out-of-memory kill is 137 rather than a code that appears " +
				"nowhere else.",
			"The program's output is this command's output, unchanged and unbuffered. " +
				"--json and --fields describe the invocation only, and are not printed at " +
				"all once the program starts, so nothing of the CLI's lands in the middle " +
				"of it.",
			"There is no shell. Arguments are passed to the program exactly as given: no " +
				"expansion, no globbing, no pipelines. `-- sh -c \"...\"` is how to get one.",
			"This command's own flags go before --. Anything after it belongs to the " +
				"program, including --json and --app, which is why `run -- true --json` " +
				"passes --json to true and produces no structured output.",
			"A program that is not on PATH is a usage error, exit 2 with " +
				"env.command_not_found, rather than the 127 a shell would give.",
			"The application's values overwrite the local ones where the names collide. " +
				"--pure removes the local environment entirely, which usually means the " +
				"program cannot find its own interpreter.",
			"Variables from an assigned group are not included, so the environment here is " +
				"the application's own variables and not everything the deployed container " +
				"receives.",
			"An interrupt reaches the program, and the CLI waits for it to finish rather " +
				"than returning first.",
		},

		Related: []string{"env pull", "env list", "env set", "app shell"},
		DocsURL: "https://docs.outplane.com/cli/env",
	}
}
