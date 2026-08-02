package registry

// Environment variables.
//
// Platform facts that shape this group:
//
//   - Saving a variable does not change a running application. The platform
//     refreshes a workload when it is scaled or paused, and not when its
//     variables change, so a value takes effect at the next deployment. Every
//     command here says so, and --deploy does it in one step.
//   - The API offers a full replacement and the CLI never uses it. Replacement
//     is a read-modify-write over shared state with no version check, so two
//     callers setting two different keys would each save the set they read.
//     Adding merges on the server and removal names one variable by id.
//   - Keys are compared case-insensitively by the server, so SET path=x
//     replaces PATH. The CLI matches the same way when it looks one up.
//   - The application is named with --app rather than positionally, because
//     `env unset FOO BAR` cannot otherwise be told from an application called
//     FOO.

func init() {
	Register(
		envList(),
		envGet(),
		envSet(),
		envUnset(),
	)
}

// appFlag is the same declaration in all four commands.
func appFlag() Flag {
	return Flag{
		Name: "app",
		Type: "string",
		Description: "application name or id. Defaults to the linked app. " +
			"A flag rather than an argument, so that variable names cannot be mistaken for it",
	}
}

func envList() Command {
	return Command{
		Path:  []string{"env", "list"},
		Short: "list an application's environment variables",
		Long: "Lists the variables set on an application.\n\n" +
			"Values are hidden by default and their length is reported instead. Answering " +
			"\"which keys are set\" should not put thirty credentials on a screen that may " +
			"be shared, recorded or logged. Use --reveal for all of them, or " +
			"`outplane env get` for one.\n\n" +
			"These are the variables set on the application itself. Variables it receives " +
			"from a shared group are managed in the console and are not listed here.",

		Risk:         RiskRead,
		RequiresAuth: true,
		Session:      SessionAny,
		Idempotent:   true,

		APICalls: []string{"GET /api/AppSetting/GetEnvironmentVariables/{appId}"},

		Flags: []Flag{
			appFlag(),
			{
				Name:        "reveal",
				Type:        "bool",
				Default:     "false",
				Description: "print the values instead of hiding them",
			},
		},

		OutputFields: []Field{
			{Name: "key", Type: "string"},
			{
				Name: "value",
				Type: "string",
				Description: "the value, or a mask when --reveal was not given. " +
					"Read revealed to know which one you have",
			},
			{Name: "revealed", Type: "bool", Description: "whether value is the real value"},
			{Name: "length", Type: "int", Description: "the value's length, which is reported even when it is hidden"},
		},

		ErrorCodes: []string{"context.no_app", "app.not_found"},
		ExitCodes:  []int{0, 2, 3, 5, 8},

		Examples: []Example{
			{
				Title:   "which variables are set on the linked app",
				Command: "outplane env list",
				Argv:    []string{"outplane", "env", "list"},
				Risk:    RiskRead,
			},
			{
				Title:        "the same for a named app, with values",
				Command:      "outplane env list --app checkout --reveal",
				Argv:         []string{"outplane", "env", "list", "--app", "checkout", "--reveal"},
				Placeholders: map[string]string{"checkout": "<APP_NAME>"},
				Risk:         RiskRead,
			},
			{
				Title:   "just the names",
				Command: "outplane env list --json --fields key",
				Argv:    []string{"outplane", "env", "list", "--json", "--fields", "key"},
				Risk:    RiskRead,
				OutputSample: map[string]any{
					"items": []any{
						map[string]any{"key": "DATABASE_URL"},
						map[string]any{"key": "PORT"},
					},
					"total":     2,
					"truncated": false,
				},
			},
		},

		AutomationNotes: []string{
			"value is masked unless --reveal is given, in every format including --json. " +
				"Check revealed before treating it as a credential; length is accurate either way.",
			"Variables an application receives from a shared group are not listed. This " +
				"reports what is set on the application itself.",
			"Keys are compared case-insensitively by the platform, so PATH and path are the " +
				"same variable.",
		},

		Related: []string{"env get", "env set", "env unset", "app get"},
		DocsURL: "https://docs.outplane.com/cli/env",
	}
}

func envGet() Command {
	return Command{
		Path:  []string{"env", "get"},
		Short: "print one environment variable",
		Long: "Prints the value of one variable, and nothing else.\n\n" +
			"In text output the value is the only thing on stdout, so it can be captured " +
			"directly: DATABASE_URL=$(outplane env get DATABASE_URL).",

		Risk:         RiskRead,
		RequiresAuth: true,
		Session:      SessionAny,
		Idempotent:   true,

		// Text even in a pipe, which is the one thing this command exists for.
		// The tool-wide default would answer command substitution with a JSON
		// object, so `DATABASE_URL=$(outplane env get DATABASE_URL)` would set
		// the variable to `{"key":"DATABASE_URL","value":"…"}` in every script,
		// which is exactly where substitution is used. --json still asks for the
		// object explicitly.
		Output: &OutputMode{TTY: "text", Piped: "text"},

		APICalls: []string{"GET /api/AppSetting/GetEnvironmentVariables/{appId}"},

		Args: []Arg{
			{Name: "key", Short: "the variable's name", Required: true},
		},

		Flags: []Flag{appFlag()},

		OutputFields: []Field{
			{Name: "key", Type: "string"},
			{Name: "value", Type: "string", Description: "never masked: asking for one value is the request to see it"},
		},

		ErrorCodes: []string{"env.not_found", "context.no_app", "app.not_found", "usage.missing_argument"},
		ExitCodes:  []int{0, 2, 3, 5, 8},

		Examples: []Example{
			{
				Title:        "read one value",
				Command:      "outplane env get DATABASE_URL --app checkout",
				Argv:         []string{"outplane", "env", "get", "DATABASE_URL", "--app", "checkout"},
				Placeholders: map[string]string{"checkout": "<APP_NAME>"},
				Risk:         RiskRead,
			},
			{
				Title:   "capture it in a shell variable",
				Command: "DATABASE_URL=$(outplane env get DATABASE_URL)",
				Argv:    []string{"outplane", "env", "get", "DATABASE_URL"},
				Risk:    RiskRead,
			},
		},

		AutomationNotes: []string{
			"Text is the default even in a pipe, unlike every other command. stdout carries " +
				"the raw value and a newline and nothing else, so KEY=$(outplane env get KEY) " +
				"works in a script. Pass --json to get an object instead.",
			"A missing variable is exit 5 with env.not_found and the available keys in the " +
				"error's details, so a caller never has to list separately to find out what " +
				"it should have asked for.",
		},

		Related: []string{"env list", "env set"},
		DocsURL: "https://docs.outplane.com/cli/env",
	}
}

func envSet() Command {
	return Command{
		Path:  []string{"env", "set"},
		Short: "set environment variables",
		Long: "Adds variables, or replaces the ones that already exist.\n\n" +
			"Only the keys you name are touched; nothing else is read or written, so two " +
			"people setting two different variables at the same time cannot overwrite each " +
			"other.\n\n" +
			"The running application keeps its old values until it is deployed again. " +
			"--deploy does that immediately.",

		Risk:         RiskWrite,
		RequiresAuth: true,
		Session:      SessionAny,
		Idempotent:   true,

		SupportsDryRun: true,

		APICalls: []string{
			"POST /api/AppSetting/AddEnvironmentVariables/{appId}",
			"POST /api/AppDeployment/CreateAppDeployment/{appId}",
		},

		Args: []Arg{
			{
				Name:     "assignment",
				Short:    "KEY=VALUE, repeatable",
				Required: true,
				Variadic: true,
			},
		},

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
			{Name: "action", Type: "string", Enum: []string{"set", "unset"}},
			{Name: "keys", Type: "array", Description: "the names that were written, sorted"},
			{Name: "app", Type: "string"},
			{Name: "changed", Type: "bool", Description: "false for a dry run"},
			{
				Name:        "deploymentId",
				Type:        "int | null",
				Description: "the deployment --deploy started, or null when it was not given",
			},
		},

		ErrorCodes: []string{
			"usage.bad_assignment",
			"usage.duplicate_key",
			"env.reserved_key",
			"env.reserved_prefix",
			"env.key_too_long",
			"env.value_too_long",
			"env.too_many",
			"context.no_app",
			"app.not_found",
		},
		ExitCodes: []int{0, 2, 3, 5, 8},

		Examples: []Example{
			{
				Title:   "set one variable on the linked app",
				Command: "outplane env set LOG_LEVEL=debug",
				Argv:    []string{"outplane", "env", "set", "LOG_LEVEL=debug"},
				Risk:    RiskWrite,
			},
			{
				Title:        "set several and apply them at once",
				Command:      "outplane env set LOG_LEVEL=debug TIMEOUT=30 --app checkout --deploy",
				Argv:         []string{"outplane", "env", "set", "LOG_LEVEL=debug", "TIMEOUT=30", "--app", "checkout", "--deploy"},
				Placeholders: map[string]string{"checkout": "<APP_NAME>"},
				Risk:         RiskWrite,
			},
			{
				Title:   "see what would happen",
				Command: "outplane env set LOG_LEVEL=debug --dry-run",
				Argv:    []string{"outplane", "env", "set", "LOG_LEVEL=debug", "--dry-run"},
				Risk:    RiskRead,
			},
		},

		AutomationNotes: []string{
			"Saving does not restart anything. Without --deploy the running application " +
				"keeps the values it started with, which is the single easiest thing to get " +
				"wrong here.",
			"Only the named keys are changed. This command never sends the whole set, so it " +
				"cannot drop a variable it was not told about.",
			"A value may contain an equals sign: only the first one separates the key, so " +
				"KEY=a=b sets KEY to a=b.",
			"HOSTNAME and any key starting with OP_ or KUBERNETES_ are refused. PORT is not " +
				"reserved and may be set.",
			"Setting an existing key replaces it, so running this twice with the same " +
				"arguments leaves the same state.",
			"deploymentId is null unless --deploy was given. A queued deployment is not a " +
				"finished one; follow it with `outplane deploy logs`.",
		},

		Related: []string{"env list", "env get", "env unset", "deploy create"},
		DocsURL: "https://docs.outplane.com/cli/env",
	}
}

func envUnset() Command {
	return Command{
		Path:  []string{"env", "unset"},
		Short: "remove environment variables",
		Long: "Removes variables by name.\n\n" +
			"Every name is checked before anything is removed, so a typo in the third name " +
			"does not leave the first two gone.\n\n" +
			"The value is not recoverable afterwards, and the running application keeps it " +
			"until the app is deployed again.",

		Risk:         RiskWrite,
		RequiresAuth: true,
		Session:      SessionAny,
		Idempotent:   false,

		SupportsDryRun: true,

		APICalls: []string{
			"GET /api/AppSetting/GetEnvironmentVariables/{appId}",
			"DELETE /api/AppSetting/DeleteEnvironmentVariable/{appId}/{id}",
		},

		Args: []Arg{
			{
				Name:     "key",
				Short:    "the variable's name, repeatable",
				Required: true,
				Variadic: true,
			},
		},

		Flags: []Flag{
			appFlag(),
			{
				Name:        "deploy",
				Type:        "bool",
				Default:     "false",
				Description: "deploy afterwards, so the removal reaches the running app",
			},
		},

		OutputFields: []Field{
			{Name: "action", Type: "string", Enum: []string{"set", "unset"}},
			{Name: "keys", Type: "array", Description: "the names that were removed"},
			{Name: "app", Type: "string"},
			{Name: "changed", Type: "bool", Description: "false for a dry run"},
			{Name: "deploymentId", Type: "int | null"},
		},

		ErrorCodes: []string{"env.not_found", "context.no_app", "app.not_found", "usage.missing_argument"},
		ExitCodes:  []int{0, 2, 3, 5, 8},

		Examples: []Example{
			{
				Title:   "remove one variable",
				Command: "outplane env unset LOG_LEVEL",
				Argv:    []string{"outplane", "env", "unset", "LOG_LEVEL"},
				Risk:    RiskWrite,
			},
			{
				Title:        "remove several and apply the change",
				Command:      "outplane env unset LOG_LEVEL TIMEOUT --app checkout --deploy",
				Argv:         []string{"outplane", "env", "unset", "LOG_LEVEL", "TIMEOUT", "--app", "checkout", "--deploy"},
				Placeholders: map[string]string{"checkout": "<APP_NAME>"},
				Risk:         RiskWrite,
			},
		},

		AutomationNotes: []string{
			"A name that is not set is exit 5 and nothing is removed, including the names " +
				"that were valid. Removal is all or nothing.",
			"The value cannot be recovered afterwards. This command is not gated behind a " +
				"confirmation, because removing a variable is an ordinary edit; the " +
				"irreversible part is the value, not the application.",
			"Without --deploy the running application still has the variable. Removing it " +
				"from the record and from the process are two different things.",
		},

		Related: []string{"env list", "env set", "deploy create"},
		DocsURL: "https://docs.outplane.com/cli/env",
	}
}
