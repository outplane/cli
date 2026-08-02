package registry

// Credentials for private container registries.
//
// Three commands because the platform has three endpoints. There is no update:
// rotating a password is a delete and a create, and that is said plainly rather
// than wrapped in a command that would do both and leave nothing behind if the
// second half failed.
//
// The password shapes the rest. It is write-only on the server, no endpoint
// returns it, and nothing here prints it. --password-stdin is the form that
// keeps it out of the process list and the shell's history, which is why the
// inline flag carries a warning.

func init() {
	Register(
		registryList(),
		registryCreate(),
		registryDelete(),
	)
}

func registryFields() []Field {
	return []Field{
		{Name: "id", Type: "string"},
		{Name: "name", Type: "string", Description: "what this credential is called here"},
		{
			Name: "server", Type: "string",
			Description: "the registry's host, such as ghcr.io. Not a URL and not an image reference",
		},
		{Name: "username", Type: "string"},
		{Name: "createdAt", Type: "string | null", Description: "RFC 3339, UTC"},
	}
}

func registryList() Command {
	return Command{
		Path:  []string{"registry", "list"},
		Short: "list the team's private registry credentials",
		Long: "Lists the credentials this team can pull private images with.\n\n" +
			"Passwords are not here. The platform stores them write-only and no endpoint " +
			"returns one, so a credential whose password is lost is replaced rather than " +
			"read.",

		Risk:         RiskRead,
		RequiresAuth: true,
		Session:      SessionAny,
		Idempotent:   true,

		APICalls: []string{"GET /api/RegistryCredential/GetByTeamId"},

		OutputFields: registryFields(),

		ErrorCodes: []string{"context.no_team"},
		ExitCodes:  []int{0, 2, 3, 8},

		Examples: []Example{
			{
				Title:   "what this team can pull with",
				Command: "outplane registry list",
				Argv:    []string{"outplane", "registry", "list"},
				Risk:    RiskRead,
			},
			{
				Title:   "check whether a registry is covered, in a script",
				Command: "outplane registry list --json --fields name,server",
				Argv:    []string{"outplane", "registry", "list", "--json", "--fields", "name,server"},
				Risk:    RiskRead,
				OutputSample: map[string]any{
					"items":     []any{map[string]any{"name": "ghcr", "server": "ghcr.io"}},
					"total":     1,
					"truncated": false,
				},
			},
			{
				Title:   "see who each one logs in as",
				Command: "outplane registry list -o text",
				Argv:    []string{"outplane", "registry", "list", "-o", "text"},
				Risk:    RiskRead,
			},
		},

		AutomationNotes: []string{
			"No password is ever returned, by this or any other command. A credential is " +
				"replaced rather than inspected.",
			"An empty list is not an error. It means every image this team runs is public.",
			"The credential is chosen by the platform when an image is pulled, by matching " +
				"the server. Nothing binds one to an application here.",
		},

		Related: []string{"registry create", "registry delete", "app create"},
		DocsURL: "https://docs.outplane.com/cli/registry",
	}
}

func registryCreate() Command {
	return Command{
		Path:  []string{"registry", "create"},
		Short: "store a login for a private registry",
		Long: "Stores a username and password for a container registry.\n\n" +
			"The platform uses it when an application's image comes from that host. " +
			"Nothing binds a credential to an application: the server is matched when the " +
			"image is pulled.\n\n" +
			"Give the password on standard input with --password-stdin. An argument is " +
			"visible to every other user on the machine through the process list, and it " +
			"is recorded by the shell and by whatever runs it.\n\n" +
			"There is no update. Changing a password means removing the credential and " +
			"storing it again.",

		Risk:         RiskWrite,
		RequiresAuth: true,
		Session:      SessionAny,

		// Storing the same name twice is the server's decision, and it refuses
		// rather than replacing, so a second run is not a no-op.
		Idempotent: false,

		SupportsDryRun: true,

		APICalls: []string{"POST /api/RegistryCredential/Create"},

		Args: []Arg{
			{Name: "name", Short: "what to call this credential here", Required: true},
		},

		Flags: []Flag{
			{
				Name: "server", Type: "string",
				Description: "the registry's host, such as ghcr.io. Required",
			},
			{Name: "username", Type: "string", Description: "the login. Required"},
			{
				Name: "password-stdin", Type: "bool", Default: "false",
				Description: "read the password from standard input, which keeps it out of the process list",
			},
			{
				Name: "password", Type: "string",
				Description: "the password as an argument",
				Discouraged: "argv is visible in process lists and CI logs. Prefer --password-stdin",
			},
		},

		OutputFields: append(registryFields(),
			Field{Name: "changed", Type: "bool", Description: "false for a dry run"}),

		ErrorCodes: []string{
			"registry.name_required",
			"registry.server_required",
			"registry.server_invalid",
			"registry.username_required",
			"registry.password_required",
			"registry.password_unreadable",
			"usage.conflicting_flags",
			"usage.missing_argument",
			"context.no_team",
		},
		ExitCodes: []int{0, 2, 3, 6, 8},

		Examples: []Example{
			{
				Title:   "store one, reading the password from standard input",
				Command: "echo \"$GITHUB_TOKEN\" | outplane registry create ghcr --server ghcr.io --username acme --password-stdin",
				Argv: []string{"outplane", "registry", "create", "ghcr", "--server", "ghcr.io",
					"--username", "acme", "--password-stdin"},
				Placeholders: map[string]string{"ghcr": "<NAME>", "acme": "<USERNAME>"},
				Risk:         RiskWrite,
			},
			{
				Title:   "check the request without sending a password anywhere",
				Command: "outplane registry create ghcr --server ghcr.io --username acme --password x --dry-run --json",
				Argv: []string{"outplane", "registry", "create", "ghcr", "--server", "ghcr.io",
					"--username", "acme", "--password", "x", "--dry-run", "--json"},
				Placeholders: map[string]string{"ghcr": "<NAME>", "acme": "<USERNAME>"},
				Risk:         RiskRead,
				OutputSample: map[string]any{
					"name": "ghcr", "server": "ghcr.io", "username": "acme", "changed": false,
				},
			},
			{
				Title:   "store one from a file, and read back what was stored",
				Command: "cat token.txt | outplane registry create ghcr --server ghcr.io --username acme --password-stdin --json --fields name,server,changed",
				Argv: []string{"outplane", "registry", "create", "ghcr", "--server", "ghcr.io",
					"--username", "acme", "--password-stdin", "--json", "--fields", "name,server,changed"},
				Placeholders: map[string]string{"ghcr": "<NAME>", "acme": "<USERNAME>"},
				Risk:         RiskWrite,
			},
		},

		AutomationNotes: []string{
			"--password-stdin reads everything on standard input and strips one trailing " +
				"newline, which is what `echo` adds. Nothing else is trimmed, because a " +
				"password may legitimately begin or end with a space.",
			"Giving both --password and --password-stdin is refused rather than one of them " +
				"winning, since there is no rule saying which should.",
			"The password is never printed back, by this command or any other. Nothing in " +
				"the result carries it.",
			"An application already deployed keeps pulling with whatever it had. A new " +
				"credential is used by the next deployment.",
			"There is no update endpoint. Rotating a password is `registry delete` followed " +
				"by this command, and the gap between them is a window where a deployment " +
				"would fail.",
		},

		Related: []string{"registry list", "registry delete", "app create", "build set"},
		DocsURL: "https://docs.outplane.com/cli/registry",
	}
}

func registryDelete() Command {
	return Command{
		Path:  []string{"registry", "delete"},
		Short: "remove a private registry credential",
		Long: "Removes a stored login.\n\n" +
			"The password cannot be read back, so this is not reversible without the " +
			"original. An application that pulls from that registry fails at its next " +
			"deployment unless another credential covers the same host.\n\n" +
			"Both --yes and --confirm-name are required, and the CLI refuses to do this " +
			"under an agent harness whatever flags are given.",

		Risk:         RiskDestructive,
		RequiresAuth: true,
		Session:      SessionAny,
		Idempotent:   true,

		SupportsDryRun: true,

		APICalls: []string{
			"GET /api/RegistryCredential/GetByTeamId",
			"DELETE /api/RegistryCredential/Delete/{id}",
		},

		Args: []Arg{
			{Name: "credential", Short: "name or id", Required: true},
		},

		Flags: []Flag{
			{
				Name: "yes", Short: "y", Type: "bool", Default: "false",
				Description: "acknowledge the removal. Not sufficient on its own",
			},
			{
				Name: "confirm-name", Type: "string",
				Description: "the credential's name, repeated. Guards against deleting the wrong one",
			},
		},

		OutputFields: append(registryFields(),
			Field{Name: "changed", Type: "bool", Description: "false for a dry run"}),

		ErrorCodes: []string{
			"registry.not_found",
			"registry.confirm_name_mismatch",
			"confirmation.required",
			"usage.missing_argument",
			"context.no_team",
		},
		ExitCodes: []int{0, 2, 3, 4, 5, 8},

		Examples: []Example{
			{
				Title:        "check what the name resolves to, before confirming",
				Command:      "outplane registry delete ghcr --dry-run --json",
				Argv:         []string{"outplane", "registry", "delete", "ghcr", "--dry-run", "--json"},
				Placeholders: map[string]string{"ghcr": "<NAME>"},
				Risk:         RiskRead,
				OutputSample: map[string]any{"name": "ghcr", "server": "ghcr.io", "changed": false},
			},
			{
				Title:        "request removal. Returns exit 4 with a command to replay",
				Command:      "outplane registry delete ghcr",
				Argv:         []string{"outplane", "registry", "delete", "ghcr"},
				Placeholders: map[string]string{"ghcr": "<NAME>"},
				Risk:         RiskWrite,
			},
			{
				Title:        "the confirmed form",
				Command:      "outplane registry delete ghcr --yes --confirm-name ghcr",
				Argv:         []string{"outplane", "registry", "delete", "ghcr", "--yes", "--confirm-name", "ghcr"},
				Placeholders: map[string]string{"ghcr": "<NAME>"},
				Risk:         RiskDestructive,
			},
		},

		AutomationNotes: []string{
			"Refused under an agent harness, exit 4 with confirmation.required, whatever " +
				"flags are given. The error carries the exact command a person would run.",
			"Outside a harness, both --yes and --confirm-name are required, and " +
				"--confirm-name has to match the credential's name exactly.",
			"The password is not recoverable. Restoring this means having the original " +
				"somewhere else.",
			"Nothing breaks immediately. An application already running keeps its image; " +
				"the failure appears at the next deployment that has to pull.",
		},

		Related: []string{"registry list", "registry create", "app get"},
		DocsURL: "https://docs.outplane.com/cli/registry",
	}
}
