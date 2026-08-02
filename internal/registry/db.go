package registry

// Managed PostgreSQL.
//
// The platform provisions these; nothing here runs as one of the team's own
// applications. Facts that shape the commands:
//
//   - Provisioning is not instant. Creating returns a database in a
//     provisioning state, and connecting to it before it is active fails.
//   - The region is chosen once and cannot be changed, so there is no default.
//   - A connection string is assembled from a role and a database inside the
//     instance rather than stored, and it carries the role's password.
//   - Only PostgreSQL is offered. Anything else runs as an ordinary
//     application with a volume.

func init() {
	Register(
		dbList(),
		dbGet(),
		dbURL(),
		dbCreate(),
		dbDelete(),
	)
}

func dbArg() Arg {
	return Arg{
		Name:     "database",
		Short:    "database name or id",
		Required: true,
		Resolves: "database",
	}
}

func dbFields() []Field {
	return []Field{
		{Name: "id", Type: "string"},
		{Name: "name", Type: "string"},
		{
			Name: "status",
			Type: "string",
			Enum: []string{"provisioning", "active", "failed", "deleting"},
			Description: "only active accepts connections. provisioning means the provider " +
				"has not finished",
		},
		{Name: "version", Type: "string", Description: "PostgreSQL major version"},
		{Name: "region", Type: "string", Description: "fixed at creation"},
		{
			Name:        "size",
			Type:        "string | null",
			Description: "the provider's compute unit, such as 0.25-1. Not a platform instance code",
		},
		{Name: "createdAt", Type: "string | null", Description: "RFC 3339, UTC"},
	}
}

func dbList() Command {
	return Command{
		Path:  []string{"db", "list"},
		Short: "list the team's managed databases",
		Long: "Lists the managed PostgreSQL databases in the team.\n\n" +
			"These are provisioned by the platform. A database engine you run yourself is " +
			"an ordinary application and appears in `outplane app list`.",

		Risk:         RiskRead,
		RequiresAuth: true,
		Session:      SessionAny,
		Idempotent:   true,

		APICalls:     []string{"GET /api/DataStorage/GetAllDataSourcesByTeamId"},
		OutputFields: dbFields(),

		ErrorCodes: []string{"context.no_team"},
		ExitCodes:  []int{0, 2, 3, 8},

		Examples: []Example{
			{
				Title:   "what databases exist",
				Command: "outplane db list",
				Argv:    []string{"outplane", "db", "list"},
				Risk:    RiskRead,
			},
			{
				Title:   "see where each one runs",
				Command: "outplane db list -o text",
				Argv:    []string{"outplane", "db", "list", "-o", "text"},
				Risk:    RiskRead,
			},
			{
				Title:   "wait for one to finish provisioning",
				Command: "outplane db list --json --fields name,status",
				Argv:    []string{"outplane", "db", "list", "--json", "--fields", "name,status"},
				Risk:    RiskRead,
				OutputSample: map[string]any{
					"items": []any{
						map[string]any{"name": "orders", "status": "active"},
					},
					"total":     1,
					"truncated": false,
				},
			},
		},

		AutomationNotes: []string{
			"Only PostgreSQL is managed. Anything else is run as an application with a " +
				"volume and is not listed here.",
			"status is the platform's view of provisioning, not a health check. active means " +
				"the provider reported it ready.",
		},

		Related: []string{"db get", "db url", "db create"},
		DocsURL: "https://docs.outplane.com/cli/db",
	}
}

func dbGet() Command {
	return Command{
		Path:  []string{"db", "get"},
		Short: "show one database and what is inside it",
		Long: "Reports one database, with the roles and the databases inside it.\n\n" +
			"Those two are what a connection string is made of, which is why they are read " +
			"here rather than left to a second command.",

		Risk:         RiskRead,
		RequiresAuth: true,
		Session:      SessionAny,
		Idempotent:   true,

		APICalls: []string{
			"GET /api/DataStorage/GetAllDataSourcesByTeamId",
			"GET /api/DataStorage/GetDataStorageById/{id}/false",
			"GET /api/DataStorage/GetDataStorageRolesAndDatabases/{id}",
		},

		Args: []Arg{dbArg()},

		OutputFields: append(dbFields(),
			Field{Name: "roles", Type: "array", Description: "role names that can log in"},
			Field{
				Name:        "databases",
				Type:        "array",
				Description: "{name, owner}: the databases inside the instance",
			}),

		ErrorCodes: []string{"db.not_found", "db.ambiguous", "usage.missing_argument"},
		ExitCodes:  []int{0, 2, 3, 5, 8},

		Examples: []Example{
			{
				Title:   "list the roles a script can connect as",
				Command: "outplane db get orders --json --fields roles,databases",
				Argv: []string{"outplane", "db", "get", "orders", "--json",
					"--fields", "roles,databases"},
				Placeholders: map[string]string{"orders": "<DATABASE_NAME>"},
				Risk:         RiskRead,
				OutputSample: map[string]any{
					"roles":     []any{"app"},
					"databases": []any{map[string]any{"name": "main", "owner": "app"}},
				},
			},
			{
				Title:        "wait until it accepts connections",
				Command:      "outplane db get orders --json --fields status",
				Argv:         []string{"outplane", "db", "get", "orders", "--json", "--fields", "status"},
				Placeholders: map[string]string{"orders": "<DATABASE_NAME>"},
				Risk:         RiskRead,
				OutputSample: map[string]any{"status": "active"},
			},
			{
				Title:        "show a database",
				Command:      "outplane db get orders",
				Argv:         []string{"outplane", "db", "get", "orders"},
				Placeholders: map[string]string{"orders": "<DATABASE_NAME>"},
				Risk:         RiskRead,
			},
		},

		AutomationNotes: []string{
			"roles and databases come from the provider and are named, not identified: a " +
				"connection string is built from one of each.",
			"A database that is still provisioning has neither yet. That is reported as an " +
				"empty list with the status explaining it, not as a failure.",
			"The provider's own project object is returned by the API and deliberately not " +
				"reported: it is somebody else's shape and changes without notice.",
		},

		Related: []string{"db url", "db list"},
		DocsURL: "https://docs.outplane.com/cli/db",
	}
}

func dbURL() Command {
	return Command{
		Path:  []string{"db", "url"},
		Short: "print a connection string",
		Long: "Prints the connection string for a role and a database, and nothing else.\n\n" +
			"In text output the string is the only thing on stdout, so it can be captured " +
			"directly: DATABASE_URL=$(outplane db url orders).\n\n" +
			"The string contains the role's password. It is a credential.",

		Risk:         RiskRead,
		RequiresAuth: true,
		Session:      SessionAny,
		Idempotent:   true,

		// Text even in a pipe, like `env get`, for the same reason: this exists
		// to be captured by a shell.
		Output: &OutputMode{TTY: "text", Piped: "text"},

		APICalls: []string{
			"GET /api/DataStorage/GetDataStorageRolesAndDatabases/{id}",
			"GET /api/DataStorage/GetConnectionUrl/{id}/{role}/{database}",
		},

		Args: []Arg{dbArg()},

		Flags: []Flag{
			{
				Name:        "role",
				Type:        "string",
				Description: "which login to build it for. Optional when there is only one",
			},
			{
				Name:        "database",
				Type:        "string",
				Description: "which database inside the instance. Optional when there is only one",
			},
		},

		OutputFields: []Field{
			{Name: "url", Type: "string", Description: "carries the role's password"},
			{Name: "role", Type: "string"},
			{Name: "database", Type: "string"},
			{Name: "db", Type: "string", Description: "the instance it belongs to"},
		},

		ErrorCodes: []string{
			"db.not_found",
			"db.role_required",
			"db.database_required",
			"usage.missing_argument",
		},
		ExitCodes: []int{0, 2, 3, 5, 8},

		Examples: []Example{
			{
				Title:        "the only role and database",
				Command:      "outplane db url orders",
				Argv:         []string{"outplane", "db", "url", "orders"},
				Placeholders: map[string]string{"orders": "<DATABASE_NAME>"},
				Risk:         RiskRead,
			},
			{
				Title:        "read the parts rather than the string",
				Command:      "outplane db url orders --json --fields role,database,db",
				Argv:         []string{"outplane", "db", "url", "orders", "--json", "--fields", "role,database,db"},
				Placeholders: map[string]string{"orders": "<DATABASE_NAME>"},
				Risk:         RiskRead,
				OutputSample: map[string]any{"role": "app", "database": "main", "db": "orders"},
			},
			{
				Title:        "name them when there is a choice",
				Command:      "outplane db url orders --role app --database main",
				Argv:         []string{"outplane", "db", "url", "orders", "--role", "app", "--database", "main"},
				Placeholders: map[string]string{"orders": "<DATABASE_NAME>"},
				Risk:         RiskRead,
			},
		},

		AutomationNotes: []string{
			"Text is the default even in a pipe, unlike most commands, so command " +
				"substitution captures the string rather than an object. Pass --json for the " +
				"object.",
			"--role and --database are optional only while there is exactly one of each. " +
				"With a choice the command refuses and lists them rather than picking.",
			"The string is a credential and is never masked here: asking for it is the " +
				"request to see it. It does not appear in any other command's output.",
			"Because the piped default is text, a failure is a sentence on stderr rather than a JSON envelope on stdout, and stdout stays empty. That is what keeps command substitution from capturing an error message. Pass --json when the envelope is wanted.",
			"To hand it to an application, capture it and pass it to `outplane env set`, " +
				"which takes a KEY=VALUE pair and can deploy afterwards.",
		},

		Related: []string{"db get", "env set"},
		DocsURL: "https://docs.outplane.com/cli/db",
	}
}

func dbCreate() Command {
	return Command{
		Path:  []string{"db", "create"},
		Short: "provision a managed PostgreSQL database",
		Long: "Creates a managed PostgreSQL database.\n\n" +
			"The region is fixed at creation and cannot be changed, so it has to be given. " +
			"The version defaults to the current major.\n\n" +
			"Provisioning takes a moment: the command returns while the database is still " +
			"being made, and it cannot be connected to until it is active.",

		Risk:           RiskWrite,
		RequiresAuth:   true,
		Session:        SessionAny,
		Idempotent:     false,
		SupportsDryRun: true,

		APICalls: []string{"POST /api/DataStorage/CreatePostgresDataStorage"},

		Args: []Arg{
			{Name: "name", Short: "the database's name", Required: true},
		},

		Flags: []Flag{
			{
				Name: "region", Type: "string",
				Enum: []string{
					"aws-us-east-1", "aws-us-east-2", "aws-us-west-2",
					"aws-eu-central-1", "aws-eu-west-2",
					"aws-ap-southeast-1", "aws-ap-southeast-2", "aws-sa-east-1",
				},
				Description: "where it runs. Required, and permanent",
			},
			{
				Name: "version", Type: "string", Default: "17",
				Enum:        []string{"14", "15", "16", "17", "18"},
				Description: "PostgreSQL major version",
			},
		},

		OutputFields: append(dbFields(), Field{Name: "changed", Type: "bool"}),

		ErrorCodes: []string{
			"db.name_required",
			"db.name_invalid",
			"db.region_required",
			"db.region_invalid",
			"db.version_invalid",
			"quota.limit_reached",
		},
		ExitCodes: []int{0, 2, 3, 7, 8},

		Examples: []Example{
			{
				Title:        "a database in Frankfurt",
				Command:      "outplane db create orders --region aws-eu-central-1",
				Argv:         []string{"outplane", "db", "create", "orders", "--region", "aws-eu-central-1"},
				Placeholders: map[string]string{"orders": "<DATABASE_NAME>"},
				Risk:         RiskWrite,
			},
			{
				Title:   "check the request without provisioning anything",
				Command: "outplane db create orders --region aws-eu-central-1 --dry-run --json",
				Argv: []string{"outplane", "db", "create", "orders", "--region",
					"aws-eu-central-1", "--dry-run", "--json"},
				Placeholders: map[string]string{"orders": "<DATABASE_NAME>"},
				Risk:         RiskRead,
			},
			{
				Title:        "an older major version",
				Command:      "outplane db create legacy --region aws-eu-central-1 --version 15",
				Argv:         []string{"outplane", "db", "create", "legacy", "--region", "aws-eu-central-1", "--version", "15"},
				Placeholders: map[string]string{"legacy": "<DATABASE_NAME>"},
				Risk:         RiskWrite,
			},
		},

		AutomationNotes: []string{
			"The command returns before the database is ready. Poll `outplane db get <name>` " +
				"until status is active; connecting sooner fails.",
			"There is no default region, because it cannot be changed afterwards and a wrong " +
				"one means moving the data by hand.",
			"How many databases a team may have is a plan limit, so exceeding it is exit 7 " +
				"rather than a validation error.",
		},

		Related: []string{"db get", "db url", "db list"},
		DocsURL: "https://docs.outplane.com/cli/db",
	}
}

func dbDelete() Command {
	return Command{
		Path:  []string{"db", "delete"},
		Short: "permanently destroy a database",
		Long: "Deletes a managed database.\n\n" +
			"Every database and role inside it goes, and nothing restores them. There is no " +
			"snapshot and no retention window: this is the data itself, not a machine that " +
			"can be rebuilt.",

		Risk:           RiskDestructive,
		RequiresAuth:   true,
		Session:        SessionAny,
		Idempotent:     false,
		SupportsDryRun: true,

		APICalls: []string{"DELETE /api/DataStorage/DeleteDataStorage/{id}"},

		Args: []Arg{dbArg()},

		Flags: []Flag{
			{
				Name: "yes", Short: "y", Type: "bool", Default: "false",
				Description: "acknowledge the deletion. Not sufficient on its own",
			},
			{Name: "confirm-name", Type: "string", Description: "the database's name, typed again"},
		},

		OutputFields: append(dbFields(), Field{Name: "changed", Type: "bool"}),

		ErrorCodes: []string{
			"confirmation.required",
			"db.confirm_name_mismatch",
			"db.not_found",
		},
		ExitCodes: []int{0, 2, 3, 4, 5, 8},

		Examples: []Example{
			{
				Title:        "see what would go",
				Command:      "outplane db delete orders --dry-run",
				Argv:         []string{"outplane", "db", "delete", "orders", "--dry-run"},
				Placeholders: map[string]string{"orders": "<DATABASE_NAME>"},
				Risk:         RiskRead,
			},
			{
				Title:   "check what the name resolves to first",
				Command: "outplane db delete orders --dry-run --json --fields db,dbId",
				Argv: []string{"outplane", "db", "delete", "orders", "--dry-run", "--json",
					"--fields", "db,dbId"},
				Placeholders: map[string]string{"orders": "<DATABASE_NAME>"},
				Risk:         RiskRead,
			},
			{
				Title:        "the confirmed form",
				Command:      "outplane db delete orders --yes --confirm-name orders",
				Argv:         []string{"outplane", "db", "delete", "orders", "--yes", "--confirm-name", "orders"},
				Placeholders: map[string]string{"orders": "<DATABASE_NAME>"},
				Risk:         RiskDestructive,
			},
		},

		AutomationNotes: []string{
			"Never prompts. Without confirmation it exits 4 and returns the command to " +
				"replay in the error's confirm_command field.",
			"Under a detected agent harness it exits 4 even with both flags.",
			"Applications keep whatever connection string they were given. Nothing here " +
				"updates them, and they will fail on their next connection.",
			"Irreversible. Take a dump first if the data matters.",
		},

		Related: []string{"db list", "db get"},
		DocsURL: "https://docs.outplane.com/cli/db",
	}
}
