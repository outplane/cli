package registry

// What lives inside a managed database: logins, and places to put tables.
//
// The instance comes first in every one of these, as in every other db command,
// and the thing inside it second. One endpoint on the server creates either
// kind; they are separate commands because a role is a credential and a
// database is storage.

func init() {
	Register(
		dbRoleList(),
		dbRoleCreate(),
		dbRoleDelete(),
		dbSchemaList(),
		dbSchemaCreate(),
		dbSchemaDelete(),
	)
}

// insideArgs are the two positionals: which instance, and which thing in it.
func insideArgs(kind string) []Arg {
	return []Arg{
		dbArg(),
		{Name: kind, Short: "the " + kind + "'s name", Required: true},
	}
}

func dbRoleList() Command {
	return Command{
		Path:  []string{"db", "role", "list"},
		Short: "list the logins a database accepts",
		Long: "Lists the roles in a managed database.\n\n" +
			"A role is a login. Its password is never shown here; it reaches a caller " +
			"through `outplane db url`, which is the only place it appears.",

		Risk:         RiskRead,
		RequiresAuth: true,
		Session:      SessionAny,
		Idempotent:   true,

		APICalls: []string{"GET /api/DataStorage/GetDataStorageRolesAndDatabases/{id}"},
		Args:     []Arg{dbArg()},

		OutputFields: []Field{
			{Name: "name", Type: "string"},
			{Name: "db", Type: "string", Description: "the instance it belongs to"},
		},

		ErrorCodes: []string{"db.not_found", "usage.missing_argument"},
		ExitCodes:  []int{0, 2, 3, 5, 8},

		Examples: []Example{
			{
				Title:        "which logins exist",
				Command:      "outplane db role list orders",
				Argv:         []string{"outplane", "db", "role", "list", "orders"},
				Placeholders: map[string]string{"orders": "<DATABASE_NAME>"},
				Risk:         RiskRead,
			},
			{
				Title:        "read the names in a pipeline",
				Command:      "outplane db role list orders --json --fields name",
				Argv:         []string{"outplane", "db", "role", "list", "orders", "--json", "--fields", "name"},
				Placeholders: map[string]string{"orders": "<DATABASE_NAME>"},
				Risk:         RiskRead,
				OutputSample: map[string]any{
					"items":     []any{map[string]any{"name": "app"}},
					"total":     1,
					"truncated": false,
				},
			},
			{
				Title:        "check whether one exists before creating it",
				Command:      "outplane db role list orders -o text",
				Argv:         []string{"outplane", "db", "role", "list", "orders", "-o", "text"},
				Placeholders: map[string]string{"orders": "<DATABASE_NAME>"},
				Risk:         RiskRead,
			},
		},

		AutomationNotes: []string{
			"A database that is still provisioning reports none, which is a state rather " +
				"than an error.",
			"Passwords are not here and are not anywhere else either. `db url` assembles a " +
				"connection string that contains one.",
		},

		Related: []string{"db role create", "db url", "db get"},
		DocsURL: "https://docs.outplane.com/cli/db",
	}
}

func dbRoleCreate() Command {
	return Command{
		Path:  []string{"db", "role", "create"},
		Short: "add a login to a database",
		Long: "Creates a role in a managed database.\n\n" +
			"The provider generates its password, which is not returned. Read it once " +
			"through `outplane db url <database> --role <role>`.",

		Risk:           RiskWrite,
		RequiresAuth:   true,
		Session:        SessionAny,
		Idempotent:     false,
		SupportsDryRun: true,

		APICalls: []string{"POST /api/DataStorage/CreateDataStorageRoleOrDatabase"},
		Args:     insideArgs("role"),

		OutputFields: []Field{
			{Name: "kind", Type: "string", Enum: []string{"role", "database"}},
			{Name: "name", Type: "string"},
			{Name: "db", Type: "string"},
			{Name: "dbId", Type: "string"},
			{Name: "changed", Type: "bool"},
		},

		ErrorCodes: []string{"db.not_found", "usage.missing_argument"},
		ExitCodes:  []int{0, 2, 3, 5, 8},

		Examples: []Example{
			{
				Title:        "check the request without creating anything",
				Command:      "outplane db role create orders checkout --dry-run --json",
				Argv:         []string{"outplane", "db", "role", "create", "orders", "checkout", "--dry-run", "--json"},
				Placeholders: map[string]string{"orders": "<DATABASE_NAME>", "checkout": "<ROLE_NAME>"},
				Risk:         RiskRead,
				OutputSample: map[string]any{"kind": "role", "name": "checkout", "changed": false},
			},
			{
				Title:        "a login for one application",
				Command:      "outplane db role create orders checkout",
				Argv:         []string{"outplane", "db", "role", "create", "orders", "checkout"},
				Placeholders: map[string]string{"orders": "<DATABASE_NAME>", "checkout": "<ROLE_NAME>"},
				Risk:         RiskWrite,
			},
			{
				Title:        "create one and read its connection string",
				Command:      "outplane db role create orders checkout --json --fields name,changed",
				Argv:         []string{"outplane", "db", "role", "create", "orders", "checkout", "--json", "--fields", "name,changed"},
				Placeholders: map[string]string{"orders": "<DATABASE_NAME>", "checkout": "<ROLE_NAME>"},
				Risk:         RiskWrite,
			},
		},

		AutomationNotes: []string{
			"The password is generated and never returned by this command. It is in the " +
				"connection string and nowhere else, so read it there when it is needed.",
			"Creating the same role twice is refused by the provider rather than being a " +
				"no-op.",
		},

		Related: []string{"db url", "db database create", "db role delete"},
		DocsURL: "https://docs.outplane.com/cli/db",
	}
}

func dbRoleDelete() Command {
	return insideDelete("role",
		"remove a login",
		"Deletes a role.\n\n"+
			"Anything still connecting as it stops working immediately, including "+
			"applications holding a connection string built from it.")
}

func dbSchemaList() Command {
	return Command{
		Path:  []string{"db", "database", "list"},
		Short: "list the databases inside a managed instance",
		Long: "Lists the databases inside a managed instance.\n\n" +
			"The platform calls the instance a database and so does PostgreSQL, for two " +
			"different things. The instance is what `outplane db list` reports; these are " +
			"what is inside it.",

		Risk:         RiskRead,
		RequiresAuth: true,
		Session:      SessionAny,
		Idempotent:   true,

		APICalls: []string{"GET /api/DataStorage/GetDataStorageRolesAndDatabases/{id}"},
		Args:     []Arg{dbArg()},

		OutputFields: []Field{
			{Name: "name", Type: "string"},
			{Name: "owner", Type: "string | null", Description: "the role that owns it"},
			{Name: "db", Type: "string", Description: "the instance it is inside"},
		},

		ErrorCodes: []string{"db.not_found", "usage.missing_argument"},
		ExitCodes:  []int{0, 2, 3, 5, 8},

		Examples: []Example{
			{
				Title:        "what is inside an instance",
				Command:      "outplane db database list orders",
				Argv:         []string{"outplane", "db", "database", "list", "orders"},
				Placeholders: map[string]string{"orders": "<DATABASE_NAME>"},
				Risk:         RiskRead,
			},
			{
				Title:        "read the names and owners in a pipeline",
				Command:      "outplane db database list orders --json --fields name,owner",
				Argv:         []string{"outplane", "db", "database", "list", "orders", "--json", "--fields", "name,owner"},
				Placeholders: map[string]string{"orders": "<DATABASE_NAME>"},
				Risk:         RiskRead,
				OutputSample: map[string]any{
					"items":     []any{map[string]any{"name": "main", "owner": "app"}},
					"total":     1,
					"truncated": false,
				},
			},
			{
				Title:        "check what a role owns before removing it",
				Command:      "outplane db database list orders -o text",
				Argv:         []string{"outplane", "db", "database", "list", "orders", "-o", "text"},
				Placeholders: map[string]string{"orders": "<DATABASE_NAME>"},
				Risk:         RiskRead,
			},
		},

		AutomationNotes: []string{
			"Two levels share the word database: the managed instance, and the databases " +
				"inside it. `db list` is the first, this is the second.",
		},

		Related: []string{"db database create", "db url", "db list"},
		DocsURL: "https://docs.outplane.com/cli/db",
	}
}

func dbSchemaCreate() Command {
	return Command{
		Path:  []string{"db", "database", "create"},
		Short: "add a database inside a managed instance",
		Long: "Creates a database inside a managed instance, owned by a role.\n\n" +
			"The owner is optional while the instance has exactly one role. With several, " +
			"it has to be named: choosing an owner is choosing who can read the data.",

		Risk:           RiskWrite,
		RequiresAuth:   true,
		Session:        SessionAny,
		Idempotent:     false,
		SupportsDryRun: true,

		APICalls: []string{
			"GET /api/DataStorage/GetDataStorageRolesAndDatabases/{id}",
			"POST /api/DataStorage/CreateDataStorageRoleOrDatabase",
		},

		Args: insideArgs("database"),

		Flags: []Flag{
			{
				Name:        "owner",
				Type:        "string",
				Description: "role that owns it. Optional when the instance has one role",
			},
		},

		OutputFields: []Field{
			{Name: "kind", Type: "string", Enum: []string{"role", "database"}},
			{Name: "name", Type: "string"},
			{Name: "owner", Type: "string | null"},
			{Name: "db", Type: "string"},
			{Name: "dbId", Type: "string"},
			{Name: "changed", Type: "bool"},
		},

		ErrorCodes: []string{"db.not_found", "db.owner_required", "usage.missing_argument"},
		ExitCodes:  []int{0, 2, 3, 5, 8},

		Examples: []Example{
			{
				Title:        "one database per service",
				Command:      "outplane db database create orders checkout --owner checkout",
				Argv:         []string{"outplane", "db", "database", "create", "orders", "checkout", "--owner", "checkout"},
				Placeholders: map[string]string{"orders": "<DATABASE_NAME>", "checkout": "<NAME>"},
				Risk:         RiskWrite,
			},
			{
				Title:        "check the request without creating anything",
				Command:      "outplane db database create orders checkout --owner checkout --dry-run --json",
				Argv:         []string{"outplane", "db", "database", "create", "orders", "checkout", "--owner", "checkout", "--dry-run", "--json"},
				Placeholders: map[string]string{"orders": "<DATABASE_NAME>", "checkout": "<NAME>"},
				Risk:         RiskRead,
				OutputSample: map[string]any{"kind": "database", "name": "checkout", "owner": "checkout", "changed": false},
			},
			{
				Title:        "create one and read the result",
				Command:      "outplane db database create orders checkout --owner checkout --json --fields name,owner,changed",
				Argv:         []string{"outplane", "db", "database", "create", "orders", "checkout", "--owner", "checkout", "--json", "--fields", "name,owner,changed"},
				Placeholders: map[string]string{"orders": "<DATABASE_NAME>", "checkout": "<NAME>"},
				Risk:         RiskWrite,
			}},

		AutomationNotes: []string{
			"The owner must already exist. Create the role first when a service should own " +
				"its own database.",
			"With one role the owner is filled in. With several the command refuses and " +
				"lists them rather than picking one.",
		},

		Related: []string{"db role create", "db url", "db database delete"},
		DocsURL: "https://docs.outplane.com/cli/db",
	}
}

func dbSchemaDelete() Command {
	return insideDelete("database",
		"destroy a database inside an instance",
		"Deletes a database and every table in it.\n\n"+
			"Nothing restores it. The managed instance and its other databases are "+
			"untouched.")
}

// insideDelete is the shared declaration for the two destructive commands. They
// differ in what is lost, which is the hint, and in nothing else.
func insideDelete(kind, short, long string) Command {
	return Command{
		Path:  []string{"db", kind, "delete"},
		Short: short,
		Long:  long,

		Risk:           RiskDestructive,
		RequiresAuth:   true,
		Session:        SessionAny,
		Idempotent:     false,
		SupportsDryRun: true,

		APICalls: []string{
			"DELETE /api/DataStorage/DeleteDataStorageRole/{id}/{name}",
			"DELETE /api/DataStorage/DeleteDataStorageDatabase/{id}/{name}",
		},

		Args: insideArgs(kind),

		Flags: []Flag{
			{
				Name: "yes", Short: "y", Type: "bool", Default: "false",
				Description: "acknowledge the deletion. Not sufficient on its own",
			},
			{Name: "confirm-name", Type: "string", Description: "the name, typed again"},
		},

		OutputFields: []Field{
			{Name: "kind", Type: "string", Enum: []string{"role", "database"}},
			{Name: "name", Type: "string"},
			{Name: "db", Type: "string"},
			{Name: "changed", Type: "bool"},
		},

		ErrorCodes: []string{
			"confirmation.required",
			"db.confirm_name_mismatch",
			"db.not_found",
			"usage.missing_argument",
		},
		ExitCodes: []int{0, 2, 3, 4, 5, 8},

		Examples: []Example{
			{
				Title:        "see what would go",
				Command:      "outplane db " + kind + " delete orders old --dry-run",
				Argv:         []string{"outplane", "db", kind, "delete", "orders", "old", "--dry-run"},
				Placeholders: map[string]string{"orders": "<DATABASE_NAME>", "old": "<NAME>"},
				Risk:         RiskRead,
			},
			{
				Title:        "the confirmed form",
				Command:      "outplane db " + kind + " delete orders old --yes --confirm-name old",
				Argv:         []string{"outplane", "db", kind, "delete", "orders", "old", "--yes", "--confirm-name", "old"},
				Placeholders: map[string]string{"orders": "<DATABASE_NAME>", "old": "<NAME>"},
				Risk:         RiskDestructive,
			},
			{
				Title:        "read what the name resolves to, before confirming",
				Command:      "outplane db " + kind + " delete orders old --dry-run --json",
				Argv:         []string{"outplane", "db", kind, "delete", "orders", "old", "--dry-run", "--json"},
				Placeholders: map[string]string{"orders": "<DATABASE_NAME>", "old": "<NAME>"},
				Risk:         RiskRead,
				OutputSample: map[string]any{"kind": kind, "name": "old", "changed": false},
			},
		},

		AutomationNotes: []string{
			"Never prompts. Without confirmation it exits 4 and returns the command to " +
				"replay in the error's confirm_command field.",
			"Under a detected agent harness it exits 4 even with both flags.",
			"Nothing updates the applications holding a connection string. They fail on " +
				"their next connection.",
		},

		Related: []string{"db role list", "db database list", "db url"},
		DocsURL: "https://docs.outplane.com/cli/db",
	}
}
