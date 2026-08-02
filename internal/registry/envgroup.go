package registry

// Shared variable groups.
//
// A group is written once and assigned to any number of applications, which is
// what distinguishes it from the variables on an application. Both reach the
// container; the application's own win a clash.
//
// Two API facts shape the commands:
//
//   - Updating replaces the group whole: name, description, scope and every
//     variable in one request. There is no partial update and no merging
//     endpoint, so `env group set` reads before it writes and two editors at
//     once will lose one of the edits. The notes say so.
//   - An assignment is a record with its own id, and removing one needs that id
//     rather than the pair it joins. The CLI looks it up from the application,
//     which is also how "it was not assigned" becomes a sentence instead of a
//     404.

func init() {
	Register(
		envGroupList(),
		envGroupGet(),
		envGroupCreate(),
		envGroupSet(),
		envGroupAssign(),
		envGroupUnassign(),
		envGroupDelete(),
	)
}

func groupArg() Arg {
	return Arg{
		Name:     "group",
		Short:    "group name or id",
		Required: true,
		Resolves: "env-group",
	}
}

// scopeFields describe where a group's variables are injected. Repeated in
// three declarations, so they are written once.
func groupFields() []Field {
	return []Field{
		{Name: "id", Type: "string"},
		{Name: "name", Type: "string"},
		{Name: "description", Type: "string | null"},
		{Name: "variables", Type: "int", Description: "how many variables it holds"},
		{Name: "assignments", Type: "int", Description: "how many applications use it"},
		{
			Name: "scope",
			Type: "string",
			Description: "where the variables are injected, in words: at build and at " +
				"runtime, at build only, at runtime, or nowhere",
			Enum: []string{"at build and at runtime", "at build only", "at runtime", "nowhere"},
		},
		{Name: "useInBuild", Type: "bool"},
		{Name: "useInRuntime", Type: "bool"},
	}
}

func envGroupList() Command {
	return Command{
		Path:  []string{"env", "group", "list"},
		Short: "list the team's shared variable groups",
		Long: "Lists the variable groups in the team.\n\n" +
			"A group holds variables that several applications share. An application also " +
			"has its own variables, which `outplane env list` reports and which win when " +
			"the same key is in both.",

		Risk:         RiskRead,
		RequiresAuth: true,
		Session:      SessionAny,
		Idempotent:   true,

		APICalls:     []string{"GET /api/EnvVariableGroup/GetByTeamId"},
		OutputFields: groupFields(),

		ErrorCodes: []string{"context.no_team"},
		ExitCodes:  []int{0, 2, 3, 8},

		Examples: []Example{
			{
				Title:   "what groups exist",
				Command: "outplane env group list",
				Argv:    []string{"outplane", "env", "group", "list"},
				Risk:    RiskRead,
			},
			{
				Title:   "find the id to assign",
				Command: "outplane env group list --json --fields name,id,assignments",
				Argv:    []string{"outplane", "env", "group", "list", "--json", "--fields", "name,id,assignments"},
				Risk:    RiskRead,
			},
		},

		AutomationNotes: []string{
			"This lists the groups, not what is in them. `env group get` reports the " +
				"variables, with values hidden unless asked for.",
			"A group used neither at build nor at runtime reaches nothing. scope says so in " +
				"words; useInBuild and useInRuntime are the fields to branch on.",
		},

		Related: []string{"env group get", "env group assign", "env list"},
		DocsURL: "https://docs.outplane.com/cli/env",
	}
}

func envGroupGet() Command {
	return Command{
		Path:  []string{"env", "group", "get"},
		Short: "show what a group holds",
		Long: "Lists the variables in a group.\n\n" +
			"Values are hidden by default, for the reason a group exists: it holds the same " +
			"credential for several applications, so printing it prints it once for every " +
			"reader of the screen.",

		Risk:         RiskRead,
		RequiresAuth: true,
		Session:      SessionAny,
		Idempotent:   true,

		APICalls: []string{
			"GET /api/EnvVariableGroup/GetByTeamId",
			"GET /api/EnvVariableGroup/GetById/{groupId}",
		},

		Args: []Arg{groupArg()},

		Flags: []Flag{
			{
				Name: "reveal", Type: "bool", Default: "false",
				Description: "print the values instead of hiding them",
			},
		},

		OutputFields: []Field{
			{Name: "key", Type: "string"},
			{Name: "value", Type: "string", Description: "masked unless --reveal was given"},
			{Name: "revealed", Type: "bool"},
			{Name: "length", Type: "int", Description: "accurate whether or not the value is shown"},
		},

		ErrorCodes: []string{"envgroup.not_found", "envgroup.ambiguous", "usage.missing_argument"},
		ExitCodes:  []int{0, 2, 3, 5, 8},

		Examples: []Example{
			{
				Title:        "what is in a group",
				Command:      "outplane env group get shared",
				Argv:         []string{"outplane", "env", "group", "get", "shared"},
				Placeholders: map[string]string{"shared": "<GROUP_NAME>"},
				Risk:         RiskRead,
			},
		},

		AutomationNotes: []string{
			"The rows are the variables. Which group they belong to, how it is scoped and " +
				"how many applications use it are in the closing line, because a table of " +
				"key and value has nowhere to put them.",
			"Resolving a name costs a list call before the read, since the detail endpoint " +
				"takes an id.",
		},

		Related: []string{"env group list", "env group set", "env get"},
		DocsURL: "https://docs.outplane.com/cli/env",
	}
}

func envGroupCreate() Command {
	return Command{
		Path:  []string{"env", "group", "create"},
		Short: "create a shared variable group",
		Long: "Creates a group and, optionally, fills it.\n\n" +
			"A group is used at runtime unless told otherwise. --build adds it to builds as " +
			"well, and --build-only excludes the runtime.\n\n" +
			"Creating does not assign it to anything. `env group assign` does that.",

		Risk:           RiskWrite,
		RequiresAuth:   true,
		Session:        SessionAny,
		Idempotent:     false,
		SupportsDryRun: true,

		APICalls: []string{"POST /api/EnvVariableGroup/Create"},

		Args: []Arg{
			{Name: "name", Short: "the group's name", Required: true},
		},

		Flags: []Flag{
			{Name: "var", Type: "strings", Description: "KEY=VALUE, repeatable"},
			{Name: "description", Type: "string", Description: "what the group is for"},
			{Name: "build", Type: "bool", Default: "false", Description: "also inject during builds"},
			{
				Name: "build-only", Type: "bool", Default: "false",
				Description: "inject during builds and not at runtime. Needs --build",
			},
		},

		OutputFields: append(groupFields(), Field{Name: "changed", Type: "bool"}),

		ErrorCodes: []string{
			"envgroup.name_required",
			"envgroup.name_invalid",
			"envgroup.scope_empty",
			"usage.bad_assignment",
			"env.reserved_key",
			"env.reserved_prefix",
			"env.value_too_long",
		},
		ExitCodes: []int{0, 2, 3, 8},

		Examples: []Example{
			{
				Title:        "a group two services share",
				Command:      "outplane env group create shared --var LOG_LEVEL=info --var REGION=eu",
				Argv:         []string{"outplane", "env", "group", "create", "shared", "--var", "LOG_LEVEL=info", "--var", "REGION=eu"},
				Placeholders: map[string]string{"shared": "<GROUP_NAME>"},
				Risk:         RiskWrite,
			},
			{
				Title:        "build-time only",
				Command:      "outplane env group create buildargs --var NPM_TOKEN=x --build --build-only",
				Argv:         []string{"outplane", "env", "group", "create", "buildargs", "--var", "NPM_TOKEN=x", "--build", "--build-only"},
				Placeholders: map[string]string{"buildargs": "<GROUP_NAME>"},
				Risk:         RiskWrite,
			},
		},

		AutomationNotes: []string{
			"A new group is assigned to nothing and therefore reaches nothing until " +
				"`env group assign` runs.",
			"--build-only without --build is refused. The server would accept a group used " +
				"neither at build nor at runtime, which is a mistake that looks like working " +
				"configuration.",
			"The same reserved keys apply as anywhere else: HOSTNAME, and anything starting " +
				"with OP_ or KUBERNETES_.",
		},

		Related: []string{"env group assign", "env group set", "env group list"},
		DocsURL: "https://docs.outplane.com/cli/env",
	}
}

func envGroupSet() Command {
	return Command{
		Path:  []string{"env", "group", "set"},
		Short: "change a group's variables or settings",
		Long: "Adds, replaces and removes variables in a group, and changes how it is used.\n\n" +
			"The API replaces a group whole, so this reads it first and sends everything " +
			"back. Two people editing the same group at the same time will lose one of the " +
			"edits; there is no partial update to use instead.",

		Risk:           RiskWrite,
		RequiresAuth:   true,
		Session:        SessionAny,
		Idempotent:     true,
		SupportsDryRun: true,

		APICalls: []string{
			"GET /api/EnvVariableGroup/GetById/{groupId}",
			"PUT /api/EnvVariableGroup/Update/{groupId}",
		},

		Args: []Arg{groupArg()},

		Flags: []Flag{
			{Name: "var", Type: "strings", Description: "KEY=VALUE to add or replace, repeatable"},
			{Name: "unset", Type: "strings", Description: "key to remove, repeatable"},
			{Name: "description", Type: "string"},
			{Name: "build", Type: "bool", Default: "false", Description: "start injecting during builds"},
			{Name: "no-build", Type: "bool", Default: "false", Description: "stop injecting during builds"},
			{Name: "runtime", Type: "bool", Default: "false", Description: "start injecting at runtime"},
			{Name: "build-only", Type: "bool", Default: "false", Description: "stop injecting at runtime"},
		},

		OutputFields: append(groupFields(), Field{Name: "changed", Type: "bool"}),

		ErrorCodes: []string{
			"envgroup.not_found",
			"envgroup.scope_empty",
			"env.not_found",
			"usage.bad_assignment",
			"usage.missing_argument",
		},
		ExitCodes: []int{0, 2, 3, 5, 8},

		Examples: []Example{
			{
				Title:        "add a variable",
				Command:      "outplane env group set shared --var TIMEOUT=30",
				Argv:         []string{"outplane", "env", "group", "set", "shared", "--var", "TIMEOUT=30"},
				Placeholders: map[string]string{"shared": "<GROUP_NAME>"},
				Risk:         RiskWrite,
			},
			{
				Title:        "remove one",
				Command:      "outplane env group set shared --unset TIMEOUT",
				Argv:         []string{"outplane", "env", "group", "set", "shared", "--unset", "TIMEOUT"},
				Placeholders: map[string]string{"shared": "<GROUP_NAME>"},
				Risk:         RiskWrite,
			},
		},

		AutomationNotes: []string{
			"Every variable is sent back on every call, because the endpoint replaces the " +
				"group. Concurrent edits overwrite each other and nothing detects it.",
			"--unset refuses a key the group does not have, rather than reporting success " +
				"for a removal that removed nothing.",
			"A change reaches the applications using the group at their next deployment.",
			"Calling it with nothing to change is an error, not a no-op write.",
		},

		Related: []string{"env group get", "env group create", "env set"},
		DocsURL: "https://docs.outplane.com/cli/env",
	}
}

func envGroupAssign() Command {
	return assignmentCommand("assign", "give an application a group's variables",
		"Assigns a group to an application.\n\n"+
			"The application receives the group's variables at its next deployment. Its own "+
			"variables still win when the same key is in both.")
}

func envGroupUnassign() Command {
	return assignmentCommand("unassign", "stop an application using a group",
		"Removes a group from an application.\n\n"+
			"The application keeps the variables until its next deployment, when it loses "+
			"them. The group itself is untouched.")
}

// assignmentCommand is the shared declaration for the pair.
func assignmentCommand(verb, short, long string) Command {
	return Command{
		Path:  []string{"env", "group", verb},
		Short: short,
		Long:  long,

		Risk:           RiskWrite,
		RequiresAuth:   true,
		Session:        SessionAny,
		Idempotent:     true,
		SupportsDryRun: true,

		APICalls: []string{
			"GET /api/EnvVariableGroup/GetAssignedGroupsByAppId/{appId}",
			"POST /api/EnvVariableGroup/Assign",
			"DELETE /api/EnvVariableGroup/Unassign/{assignmentId}",
		},

		Args: []Arg{groupArg()},

		Flags: []Flag{
			{
				Name: "app", Type: "string",
				Description: "application name or id. Defaults to the linked app",
			},
		},

		OutputFields: []Field{
			{Name: "group", Type: "string"},
			{Name: "groupId", Type: "string"},
			{Name: "app", Type: "string"},
			{Name: "changed", Type: "bool", Description: "false when it was already in that state"},
		},

		ErrorCodes: []string{"envgroup.not_found", "app.not_found", "context.no_app"},
		ExitCodes:  []int{0, 2, 3, 5, 8},

		Examples: []Example{
			{
				Title:        verb + " a group",
				Command:      "outplane env group " + verb + " shared --app checkout",
				Argv:         []string{"outplane", "env", "group", verb, "shared", "--app", "checkout"},
				Placeholders: map[string]string{"shared": "<GROUP_NAME>", "checkout": "<APP_NAME>"},
				Risk:         RiskWrite,
			},
		},

		AutomationNotes: []string{
			"Idempotent. Assigning something already assigned reports changed false and " +
				"exits 0, and so does unassigning something that was not assigned.",
			"The API removes an assignment by the assignment's own id, not by the pair, so " +
				"the assignment is looked up first. That is one extra request and it is what " +
				"turns a missing assignment into a sentence rather than a 404.",
			"The change reaches the application at its next deployment.",
		},

		Related: []string{"env group list", "env group get", "app get"},
		DocsURL: "https://docs.outplane.com/cli/env",
	}
}

func envGroupDelete() Command {
	return Command{
		Path:  []string{"env", "group", "delete"},
		Short: "permanently delete a variable group",
		Long: "Deletes a group and every assignment of it.\n\n" +
			"The variables go with it, and each application using the group loses them at " +
			"its next deployment. Nothing restores them.",

		Risk:           RiskDestructive,
		RequiresAuth:   true,
		Session:        SessionAny,
		Idempotent:     false,
		SupportsDryRun: true,

		APICalls: []string{"DELETE /api/EnvVariableGroup/Delete/{groupId}"},

		Args: []Arg{groupArg()},

		Flags: []Flag{
			{
				Name: "yes", Short: "y", Type: "bool", Default: "false",
				Description: "acknowledge the deletion. Not sufficient on its own",
			},
			{Name: "confirm-name", Type: "string", Description: "the group's name, typed again"},
		},

		OutputFields: append(groupFields(), Field{Name: "changed", Type: "bool"}),

		ErrorCodes: []string{
			"confirmation.required",
			"envgroup.confirm_name_mismatch",
			"envgroup.not_found",
		},
		ExitCodes: []int{0, 2, 3, 4, 5, 8},

		Examples: []Example{
			{
				Title:        "see what would be lost",
				Command:      "outplane env group delete shared --dry-run",
				Argv:         []string{"outplane", "env", "group", "delete", "shared", "--dry-run"},
				Placeholders: map[string]string{"shared": "<GROUP_NAME>"},
				Risk:         RiskRead,
			},
			{
				Title:        "the confirmed form",
				Command:      "outplane env group delete shared --yes --confirm-name shared",
				Argv:         []string{"outplane", "env", "group", "delete", "shared", "--yes", "--confirm-name", "shared"},
				Placeholders: map[string]string{"shared": "<GROUP_NAME>"},
				Risk:         RiskDestructive,
			},
		},

		AutomationNotes: []string{
			"Never prompts. Without confirmation it exits 4 and returns the command to " +
				"replay in the error's confirm_command field.",
			"Under a detected agent harness it exits 4 even with both flags.",
			"Every assignment goes with the group. --dry-run reports how many applications " +
				"that is before anything is asked.",
		},

		Related: []string{"env group unassign", "env group list"},
		DocsURL: "https://docs.outplane.com/cli/env",
	}
}
