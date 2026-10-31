package registry

// Hangars.
//
// A Hangar is a team's cloud machine for coding agents: a record shaped like an
// application's, a disk that keeps what is written to it, and one terminal
// session it keeps running. Platform facts that shape this group:
//
//   - Names come from the same set as applications'. The rule is the
//     application name rule, and a name either one holds is taken for both,
//     although `app list` shows no Hangars and `hangar list` shows no
//     applications.
//   - The size and the disk are the platform's choice, not the caller's, so
//     create takes no flag for either and reports what it got.
//   - A team has a fixed maximum number of Hangars. Reaching it is exit 7, and
//     retrying never helps.
//   - The list carries every field the detail does, so a name costs one request
//     to resolve and get needs no second one.
//
// connect is a session, so its declaration follows app shell's. What sets it
// apart is whose session it is. app shell starts a terminal of its own and ends
// it on the way out; a Hangar keeps one session running for as long as it runs,
// and connect joins it. Everything about that declaration follows from this:
//
//   - Leaving stops nothing. Whatever runs in the Hangar keeps running, and the
//     command says so on the way in and says how to come back on the way out.
//   - It asks for nothing but the Hangar. There is no instance to pick and no
//     command to run, because the session is the Hangar's and not the caller's.
//   - Like app shell, it cannot be piped, redirected or scripted, it has no
//     exit status to report, and its output fields only appear under --dry-run.

func init() {
	Register(
		hangarList(),
		hangarGet(),
		hangarCreate(),
		hangarConnect(),
		hangarRestart(),
		hangarDelete(),
	)
}

// hangarArg is the same declaration in every command that takes one.
func hangarArg() Arg {
	return Arg{
		Name:     "hangar",
		Short:    "Hangar name or id",
		Required: true,
		Resolves: "hangar",
	}
}

// hangarFields are the fields list, get and create report, so that a caller
// reading one has read them all.
func hangarFields() []Field {
	return []Field{
		{Name: "id", Type: "string"},
		{
			Name: "name",
			Type: "string",
			Description: "immutable and lower case. Unique in the team among Hangars and " +
				"applications together",
		},
		{Name: "displayName", Type: "string | null", Description: "the name as it was typed at creation"},
		{
			Name: "status",
			Type: "string",
			Description: "effective state, read as app list reads an application's: paused " +
				"outranks the rest. deploying while it starts, ready once it runs, failed when it " +
				"could not start, crashed when it stopped unexpectedly and is restarting, or unknown:N. " +
				"The field to branch on",
		},
		{Name: "paused", Type: "bool"},
		{
			Name:        "agents",
			Type:        "array",
			Description: "the coding agents installed, by id. Empty for a terminal-only Hangar",
		},
		{Name: "size", Type: "string", Description: "instance type code, e.g. op-46. Set by the platform"},
		{Name: "diskGb", Type: "int", Description: "the disk's size in GB. Set by the platform"},
		{
			Name:        "idleTimeoutMinutes",
			Type:        "int | null",
			Description: "the idle timeout chosen at creation, in minutes. null for never",
		},
		{
			Name: "ports",
			Type: "array",
			Description: "the ports open for its dev servers, lowest first: {port, scheme, public, " +
				"url, internal}. url is null on a private port; internal is name:port, where the " +
				"team's applications reach it",
		},
		{Name: "createdAt", Type: "string | null", Description: "RFC 3339, UTC"},
	}
}

// hangarAgents is the agent catalog as --agent accepts it. It is the server's
// list, kept in step with core.HangarAgents.
var hangarAgents = []string{"codex", "claude-code", "pi", "omp", "devin", "opencode", "cursor"}

func hangarList() Command {
	return Command{
		Path:  []string{"hangar", "list"},
		Short: "list the Hangars in a team",
		Long: "Lists every Hangar in the current team.\n\n" +
			"A Hangar is a cloud machine for coding agents that the team shares: the agents " +
			"chosen when it was created, a disk that keeps what is written to it, and one " +
			"terminal session that keeps running between connections.",

		Risk:         RiskRead,
		RequiresAuth: true,
		Session:      SessionAny,
		Idempotent:   true,

		APICalls: []string{"GET /api/Hangar/GetHangarsByTeamId"},

		OutputFields: hangarFields(),

		ErrorCodes: []string{"context.no_team", "auth.token_invalid"},
		ExitCodes:  []int{0, 2, 3, 8},

		Examples: []Example{
			{
				Title:   "list the Hangars in the current team",
				Command: "outplane hangar list",
				Argv:    []string{"outplane", "hangar", "list"},
				Risk:    RiskRead,
			},
			{
				Title:   "list as JSON and pick out the names",
				Command: "outplane hangar list --json --fields name,status",
				Argv:    []string{"outplane", "hangar", "list", "--json", "--fields", "name,status"},
				Risk:    RiskRead,
				OutputSample: map[string]any{
					"items": []any{
						map[string]any{"name": "myhangar", "status": "ready"},
						map[string]any{"name": "reviewbox", "status": "deploying"},
					},
					"total":     2,
					"truncated": false,
				},
			},
			{
				Title:        "list the Hangars in a specific team",
				Command:      "outplane hangar list --team acme",
				Argv:         []string{"outplane", "hangar", "list", "--team", "acme"},
				Placeholders: map[string]string{"acme": "<TEAM_SLUG>"},
				Risk:         RiskRead,
			},
		},

		AutomationNotes: []string{
			"The API returns every Hangar in one response. There is no pagination, so `total` " +
				"is the complete count and `truncated` is always false.",
			"Hangars are not applications, and `app list` does not include them. The two share " +
				"one set of names all the same: a name either one holds is taken for both.",
			"status is read as an application's: paused outranks the rest. A new Hangar " +
				"reports deploying while it starts and ready once it runs.",
			"An unrecognised state decodes to \"unknown:N\", carrying the number the server sent. " +
				"Treat it as unknown rather than as a failure.",
			"Environment variables are never included, in any format.",
		},

		Related: []string{"hangar get", "hangar create", "hangar connect", "app list"},
		DocsURL: "https://docs.outplane.com/cli/hangar",
	}
}

func hangarGet() Command {
	return Command{
		Path:  []string{"hangar", "get"},
		Short: "show one Hangar in detail",
		Long: "Reports one Hangar: its state, the agents installed in it, its size and disk, " +
			"and its idle timeout.\n\n" +
			"The Hangar can be given by name, by id, or by a display name only one Hangar has.",

		Risk:         RiskRead,
		RequiresAuth: true,
		Session:      SessionAny,
		Idempotent:   true,

		APICalls: []string{"GET /api/Hangar/GetHangarsByTeamId"},

		Args: []Arg{hangarArg()},

		OutputFields: hangarFields(),

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
				Title:        "show one Hangar",
				Command:      "outplane hangar get myhangar",
				Argv:         []string{"outplane", "hangar", "get", "myhangar"},
				Placeholders: map[string]string{"myhangar": "<HANGAR_NAME>"},
				Risk:         RiskRead,
			},
			{
				Title:        "read whether it is running, and what is installed",
				Command:      "outplane hangar get myhangar --json --fields status,agents",
				Argv:         []string{"outplane", "hangar", "get", "myhangar", "--json", "--fields", "status,agents"},
				Placeholders: map[string]string{"myhangar": "<HANGAR_NAME>"},
				Risk:         RiskRead,
				OutputSample: map[string]any{
					"status": "ready",
					"agents": []any{"codex", "claude-code"},
				},
			},
			{
				Title:   "show one in another team",
				Command: "outplane hangar get myhangar --team acme",
				Argv:    []string{"outplane", "hangar", "get", "myhangar", "--team", "acme"},
				Placeholders: map[string]string{
					"myhangar": "<HANGAR_NAME>",
					"acme":     "<TEAM_SLUG>",
				},
				Risk: RiskRead,
			},
		},

		AutomationNotes: []string{
			"Returns one object, not a list. `hangar list` returns {items, total, truncated}; " +
				"this returns the fields directly.",
			"A name is resolved by listing the team's Hangars, and the list carries every field, " +
				"so this costs one request.",
			"status ready means the Hangar is running and `hangar connect` can join its session. " +
				"While it is not, connect is refused with exit 6 and hangar.not_running.",
			"Environment variables are never included, in any format, and no flag adds them.",
		},

		Related: []string{"hangar list", "hangar connect", "hangar delete", "hangar port list", "status"},
		DocsURL: "https://docs.outplane.com/cli/hangar",
	}
}

// hangarCreate follows app create, with fewer ways to be wrong: the size and the
// disk are not the caller's to choose, and there is no source to build from.
func hangarCreate() Command {
	return Command{
		Path:  []string{"hangar", "create"},
		Short: "create a Hangar and start it",
		Long: "Creates a Hangar: a cloud machine for coding agents that the team shares, with " +
			"a disk that keeps what is written to it and a terminal session that keeps running " +
			"between connections.\n\n" +
			"Creating also starts it. The command returns as soon as the Hangar exists, and " +
			"`outplane hangar connect` joins it once it is ready.\n\n" +
			"The name follows the rule for application names and comes from the same set: " +
			"letters and numbers, 5 to 45 characters, and not one an application in the team " +
			"already has. The size and the disk are set by the platform and reported in the " +
			"result.",

		Risk:         RiskWrite,
		RequiresAuth: true,
		Session:      SessionAny,

		// Not idempotent: running it twice creates one Hangar and then fails,
		// because the name is taken.
		Idempotent:     false,
		SupportsDryRun: true,

		APICalls: []string{
			"GET /api/EnvVariableGroup/GetByTeamId (once per --env-group)",
			"POST /api/Hangar/CreateHangar",
		},

		Args: []Arg{
			{
				Name:     "name",
				Short:    "letters and numbers, 5 to 45 characters. Permanent",
				Required: true,
				Pattern:  "^[a-zA-Z0-9]{5,45}$",
			},
		},

		Flags: []Flag{
			{
				Name: "agent", Type: "strings", Repeatable: true,
				Enum: hangarAgents,
				Description: "coding agent to install, repeatable. Without it, claude-code and " +
					"codex are installed",
			},
			{
				Name: "terminal-only", Type: "bool", Default: "false",
				Description: "install no agents. Not valid with --agent",
			},
			{
				Name: "env", Type: "strings", Repeatable: true,
				Description: "KEY=VALUE, repeatable. Values are never printed back",
			},
			{
				Name: "env-group", Type: "strings", Repeatable: true,
				Description: "shared variable group to assign, by name or id, repeatable. " +
					"Where two set the same key, the one given later wins",
			},
			{
				Name: "idle-timeout", Type: "string", Default: "30m",
				Enum: []string{"30m", "never"},
				Description: "how long the Hangar may sit idle, as a duration or never. Recorded with " +
					"the Hangar",
			},
		},

		OutputFields: append(hangarFields(),
			Field{
				Name:        "envCount",
				Type:        "int",
				Description: "how many variables were set. Values are never returned",
			},
			Field{
				Name:        "envGroups",
				Type:        "array",
				Description: "the names of the variable groups assigned, in the order given",
			},
			Field{Name: "changed", Type: "bool", Description: "false for a dry run"},
		),

		ErrorCodes: []string{
			"hangar.name_invalid",
			"hangar.name_reserved",
			"hangar.name_taken",
			"hangar.agent_invalid",
			"hangar.agent_duplicate",
			"hangar.agent_conflict",
			"hangar.idle_timeout_invalid",
			"hangar.env_group_duplicate",
			"hangar.limit_reached",
			"envgroup.not_found",
			"envgroup.ambiguous",
			"env.reserved_key",
			"env.reserved_prefix",
			"usage.missing_argument",
			"usage.empty_argument",
			"usage.bad_assignment",
			"usage.duplicate_key",
			"context.no_team",
		},
		ExitCodes: []int{0, 2, 3, 5, 6, 7, 8},

		Examples: []Example{
			{
				Title:        "with the agents the console preselects",
				Command:      "outplane hangar create myhangar",
				Argv:         []string{"outplane", "hangar", "create", "myhangar"},
				Placeholders: map[string]string{"myhangar": "<HANGAR_NAME>"},
				Risk:         RiskWrite,
			},
			{
				Title:        "with one agent and no idle timeout",
				Command:      "outplane hangar create myhangar --agent codex --idle-timeout never",
				Argv:         []string{"outplane", "hangar", "create", "myhangar", "--agent", "codex", "--idle-timeout", "never"},
				Placeholders: map[string]string{"myhangar": "<HANGAR_NAME>"},
				Risk:         RiskWrite,
			},
			{
				Title:   "with a variable and a shared group",
				Command: "outplane hangar create myhangar --env LOG_LEVEL=debug --env-group shared",
				Argv: []string{"outplane", "hangar", "create", "myhangar",
					"--env", "LOG_LEVEL=debug", "--env-group", "shared"},
				Placeholders: map[string]string{"myhangar": "<HANGAR_NAME>", "shared": "<GROUP_NAME>"},
				Risk:         RiskWrite,
			},
			{
				Title:   "create one and read its id and state in a pipeline",
				Command: "outplane hangar create myhangar --json --fields id,status",
				Argv:    []string{"outplane", "hangar", "create", "myhangar", "--json", "--fields", "id,status"},
				Placeholders: map[string]string{
					"myhangar": "<HANGAR_NAME>",
				},
				Risk: RiskWrite,
				OutputSample: map[string]any{
					"id":     "3f2b1c4e-0000-0000-0000-000000000000",
					"status": "deploying",
				},
			},
			{
				Title:        "check the request without sending it",
				Command:      "outplane hangar create myhangar --terminal-only --dry-run --json",
				Argv:         []string{"outplane", "hangar", "create", "myhangar", "--terminal-only", "--dry-run", "--json"},
				Placeholders: map[string]string{"myhangar": "<HANGAR_NAME>"},
				Risk:         RiskRead,
				OutputSample: map[string]any{
					"name":               "myhangar",
					"id":                 nil,
					"agents":             []any{},
					"idleTimeoutMinutes": 30,
					"changed":            false,
				},
			},
		},

		AutomationNotes: []string{
			"Creating starts the Hangar. The command returns as soon as it exists, with status " +
				"deploying; `outplane hangar get <name>` reports when it is ready, and " +
				"`outplane hangar connect` is refused with hangar.not_running until then.",
			"A Hangar that is created but cannot start still exits 0, with status failed. Its " +
				"name stays taken until it is deleted.",
			"The name is stored in lower case, and the name as typed becomes the display name. " +
				"name in the result is the one other commands take.",
			"Applications and Hangars share one set of names. A name an application already has " +
				"is refused with exit 6 and hangar.name_taken, although `hangar list` does not show it.",
			"A team has a fixed maximum number of Hangars. Reaching it is exit 7 with " +
				"hangar.limit_reached; retrying never helps, and deleting one makes room.",
			"Without --agent, claude-code and codex are installed, as in the console. " +
				"--terminal-only installs none.",
			"The size and the disk are set by the platform, not by a flag. Under --dry-run they " +
				"are null, as are id and status.",
			"Variable values are never echoed back; envCount reports how many were set. A " +
				"variable set with --env wins over a group's, and between groups the one given " +
				"later wins. A group used only at build reaches nothing here, because a Hangar " +
				"never builds.",
			"--env-group takes a name or an id. Each one is resolved with a request of its own " +
				"before anything is sent, under --dry-run as well.",
			"Everything the server would refuse is checked first, so an error names the field. " +
				"The exceptions are the name being taken and the team's limit, which only the " +
				"server knows.",
		},

		Related: []string{"hangar list", "hangar get", "hangar connect", "hangar delete", "env group list"},
		DocsURL: "https://docs.outplane.com/cli/hangar",
	}
}

// hangarRestart is a write, not a destructive change: the disk is the same one
// afterwards, so it takes no confirmation, and --dry-run is how to look first.
// What it does stop is everything running, which every declaration here says.
func hangarRestart() Command {
	return Command{
		Path:  []string{"hangar", "restart"},
		Short: "restart a Hangar on a new machine, keeping its disk",
		Long: "Starts a Hangar again on a new machine with the same disk, pulling the Hangar " +
			"image again.\n\n" +
			"Everything running in its terminal stops, agents included, and anyone connected is " +
			"disconnected. Files under /home/hangar stay. Its ports and variables are not " +
			"touched.\n\n" +
			"It is back in under a minute: its status reads deploying, then ready, and " +
			"`outplane hangar connect` joins it again from then.",

		Risk:         RiskWrite,
		RequiresAuth: true,
		Session:      SessionAny,

		// Not idempotent: every restart stops whatever is running at the time.
		Idempotent:     false,
		SupportsDryRun: true,

		APICalls: []string{
			"GET /api/Hangar/GetHangarsByTeamId",
			"POST /api/Hangar/RestartHangar/{hangarId}",
		},

		Args: []Arg{hangarArg()},

		OutputFields: []Field{
			{Name: "restarted", Type: "bool", Description: "false for a dry run"},
			{Name: "hangar", Type: "string", Description: "the Hangar's immutable name"},
			{Name: "hangarId", Type: "string"},
		},

		ErrorCodes: []string{
			"hangar.not_running",
			"hangar.not_found",
			"hangar.ambiguous",
			"usage.missing_argument",
			"usage.empty_argument",
			"context.no_team",
		},
		ExitCodes: []int{0, 2, 3, 5, 6, 8},

		Examples: []Example{
			{
				Title:        "see what a restart would stop, without sending it",
				Command:      "outplane hangar restart myhangar --dry-run --json",
				Argv:         []string{"outplane", "hangar", "restart", "myhangar", "--dry-run", "--json"},
				Placeholders: map[string]string{"myhangar": "<HANGAR_NAME>"},
				Risk:         RiskRead,
				OutputSample: map[string]any{"restarted": false, "hangar": "myhangar"},
			},
			{
				Title:        "restart a Hangar",
				Command:      "outplane hangar restart myhangar",
				Argv:         []string{"outplane", "hangar", "restart", "myhangar"},
				Placeholders: map[string]string{"myhangar": "<HANGAR_NAME>"},
				Risk:         RiskWrite,
			},
			{
				Title:        "restart one and read the result in a pipeline",
				Command:      "outplane hangar restart myhangar --json --fields restarted,hangar",
				Argv:         []string{"outplane", "hangar", "restart", "myhangar", "--json", "--fields", "restarted,hangar"},
				Placeholders: map[string]string{"myhangar": "<HANGAR_NAME>"},
				Risk:         RiskWrite,
				OutputSample: map[string]any{"restarted": true, "hangar": "myhangar"},
			},
			{
				Title:   "restart one in another team",
				Command: "outplane hangar restart myhangar --team acme",
				Argv:    []string{"outplane", "hangar", "restart", "myhangar", "--team", "acme"},
				Placeholders: map[string]string{
					"myhangar": "<HANGAR_NAME>",
					"acme":     "<TEAM_SLUG>",
				},
				Risk: RiskWrite,
			},
		},

		AutomationNotes: []string{
			"Everything running in the Hangar's terminal stops, agents included, and anyone " +
				"connected is disconnected. Files under /home/hangar stay, on the same disk, and " +
				"its ports and variables are not touched.",
			"The command returns once the restart is accepted, not once the Hangar is back. It is " +
				"back in under a minute: poll `outplane hangar get <name> --json --fields status` " +
				"until it has read deploying and then ready; a ready in the first few seconds can " +
				"still be the state from before the restart. `hangar connect` is refused with " +
				"hangar.not_running until then.",
			"Exit 6 with hangar.not_running means nothing is running to restart, for example a " +
				"Hangar that never started or one that is paused. Nothing changed, and retrying " +
				"does not help.",
			"Not idempotent: each restart stops whatever is running at the time. Under --dry-run " +
				"nothing is sent and restarted is false.",
			"It takes no confirmation, because the disk is the same one afterwards. `hangar " +
				"delete` is the command that removes it.",
		},

		Related: []string{"hangar get", "hangar connect", "hangar list", "hangar delete"},
		DocsURL: "https://docs.outplane.com/cli/hangar",
	}
}

// hangarDelete is app delete's declaration, for a Hangar. Nothing on the server
// stops this deletion, so there is no blocked case to describe.
func hangarDelete() Command {
	return Command{
		Path:  []string{"hangar", "delete"},
		Short: "permanently delete a Hangar and its disk",
		Long: "Deletes a Hangar, its disk and everything on the disk: files, clones and whatever " +
			"its agents wrote and did not push elsewhere.\n\n" +
			"There is no undelete and no retention window. Anyone connected to its session is " +
			"disconnected.",

		Risk:         RiskDestructive,
		RequiresAuth: true,
		Session:      SessionAny,
		Idempotent:   false,

		SupportsDryRun: true,

		APICalls: []string{
			"GET /api/Hangar/GetHangarsByTeamId",
			"DELETE /api/Hangar/DeleteHangar/{hangarId}",
		},

		Args: []Arg{hangarArg()},

		Flags: []Flag{
			{
				Name:        "yes",
				Short:       "y",
				Type:        "bool",
				Default:     "false",
				Description: "acknowledge the deletion. Not sufficient on its own",
			},
			{
				Name: "confirm-name",
				Type: "string",
				Description: "the Hangar's name, typed again. Required, because a single " +
					"--force is too easy for an agent to emit by accident",
			},
		},

		OutputFields: []Field{
			{Name: "deleted", Type: "bool", Description: "false for a dry run"},
			{Name: "hangar", Type: "string", Description: "the Hangar's immutable name"},
			{Name: "hangarId", Type: "string"},
		},

		ErrorCodes: []string{
			"confirmation.required",
			"hangar.confirm_name_mismatch",
			"hangar.not_found",
			"hangar.ambiguous",
			"usage.missing_argument",
			"usage.empty_argument",
		},
		ExitCodes: []int{0, 2, 3, 4, 5, 8},

		Examples: []Example{
			{
				Title:        "confirm which Hangar the name resolves to, before confirming anything",
				Command:      "outplane hangar delete myhangar --dry-run --json",
				Argv:         []string{"outplane", "hangar", "delete", "myhangar", "--dry-run", "--json"},
				Placeholders: map[string]string{"myhangar": "<HANGAR_NAME>"},
				Risk:         RiskRead,
				OutputSample: map[string]any{"deleted": false, "hangar": "myhangar"},
			},
			{
				Title:        "request deletion. Returns exit 4 with a command to replay",
				Command:      "outplane hangar delete myhangar",
				Argv:         []string{"outplane", "hangar", "delete", "myhangar"},
				Placeholders: map[string]string{"myhangar": "<HANGAR_NAME>"},
				Risk:         RiskDestructive,
			},
			{
				Title:        "the confirmed form, which a human approves",
				Command:      "outplane hangar delete myhangar --yes --confirm-name myhangar",
				Argv:         []string{"outplane", "hangar", "delete", "myhangar", "--yes", "--confirm-name", "myhangar"},
				Placeholders: map[string]string{"myhangar": "<HANGAR_NAME>"},
				Risk:         RiskDestructive,
			},
		},

		AutomationNotes: []string{
			"This command never prompts. Without confirmation it exits 4 and returns the exact " +
				"command to replay in the error's confirm_command field.",
			"Exit 4 is not a failure. It means the confirmation belongs to somebody else.",
			"Under a detected agent harness it exits 4 even when --yes and --confirm-name are " +
				"supplied. A flag is not a safety boundary, because an agent that read this " +
				"note can emit any flag in it; the harness's own approval step is the gate.",
			"The disk is deleted with the Hangar. Work that exists only there, uncommitted or " +
				"unpushed, is gone with it.",
			"Nothing stops a deletion: a running Hangar, one with people connected to its " +
				"session and one that failed to start are all deleted the same way.",
			"--confirm-name is matched against the immutable name, not the display name.",
			"Deleting frees the name, for a new Hangar or a new application.",
		},

		Related: []string{"hangar list", "hangar get", "hangar create"},
		DocsURL: "https://docs.outplane.com/cli/hangar",
	}
}

func hangarConnect() Command {
	return Command{
		Path:  []string{"hangar", "connect"},
		Short: "join a Hangar's terminal session",
		Long: "Joins the terminal session a Hangar keeps running, from your own terminal.\n\n" +
			"This is the Hangar's own session, not a new shell. Each agent runs in a window " +
			"of its own, the same windows the console shows as tabs, and the list along the " +
			"bottom switches between them: Ctrl-b w, or click + new to open another with a " +
			"shell. Everyone joined with connect sees the same screen and types into it. Leaving detaches this terminal and nothing else, so whatever runs in " +
			"the Hangar keeps running, and connecting again picks up where it is.\n\n" +
			"Detach with Ctrl-b d, or Ctrl-b Ctrl-b d if your own terminal already uses Ctrl-b. It needs a " +
			"terminal at both ends and cannot be piped, redirected or run from a script.",

		Risk:         RiskWrite,
		RequiresAuth: true,
		Session:      SessionAny,

		// Joining twice is two terminals on one session, each able to type
		// into it, which is not the same as joining once.
		Idempotent: false,

		LongRunning:    true,
		SupportsDryRun: true,

		APICalls: []string{
			"GET /api/Hangar/GetHangarsByTeamId",
			"GET /api/AppShell/Connect/{hangarId} (WebSocket)",
		},

		Args: []Arg{hangarArg()},

		OutputFields: []Field{
			{Name: "hangar", Type: "string"},
			{Name: "hangarId", Type: "string"},
			{
				Name: "status",
				Type: "string",
				Description: "the Hangar's state as last recorded, as app list reports an " +
					"application's: paused outranks the rest",
			},
			{
				Name:        "connected",
				Type:        "bool",
				Description: "always false: only --dry-run produces these fields",
			},
		},

		ErrorCodes: []string{
			"shell.not_interactive",
			"shell.disconnected",
			"hangar.not_found",
			"hangar.ambiguous",
			"hangar.not_running",
			"usage.missing_argument",
			"usage.empty_argument",
		},
		ExitCodes: []int{0, 1, 2, 3, 5, 6, 8, 130},

		Examples: []Example{
			{
				Title:        "join a Hangar's session",
				Command:      "outplane hangar connect myhangar",
				Argv:         []string{"outplane", "hangar", "connect", "myhangar"},
				Placeholders: map[string]string{"myhangar": "<HANGAR_NAME>"},
				Risk:         RiskWrite,
			},
			{
				Title:   "join one in another team",
				Command: "outplane hangar connect myhangar --team acme",
				Argv:    []string{"outplane", "hangar", "connect", "myhangar", "--team", "acme"},
				Placeholders: map[string]string{
					"myhangar": "<HANGAR_NAME>",
					"acme":     "<TEAM_SLUG>",
				},
				Risk: RiskWrite,
			},
			{
				Title:        "check which Hangar a name reaches and its state, without joining",
				Command:      "outplane hangar connect myhangar --dry-run --json",
				Argv:         []string{"outplane", "hangar", "connect", "myhangar", "--dry-run", "--json"},
				Placeholders: map[string]string{"myhangar": "<HANGAR_NAME>"},
				Risk:         RiskRead,
				OutputSample: map[string]any{
					"hangar":    "myhangar",
					"status":    "ready",
					"connected": false,
				},
			},
		},

		AutomationNotes: []string{
			"This command requires a terminal on both standard input and standard output. " +
				"Behind a pipe, a redirect or an agent harness it exits 2 with " +
				"shell.not_interactive and joins nothing. --dry-run is the only form " +
				"that runs anywhere.",
			"The session belongs to the Hangar. Detaching, closing the terminal and losing " +
				"the connection all leave everything in it running, and connecting again " +
				"rejoins it as it is.",
			"A Hangar that is not running is refused with exit 6 and hangar.not_running " +
				"before anything opens; the error is retryable only while it is starting. " +
				"status under --dry-run is the state as last recorded and promises neither.",
			"Everyone joined with connect shares one screen and one current window. A " +
				"console tab shows a window of its own and does not follow a switch made here. " +
				"A window takes the size of whichever terminal used it last.",
			"There is no exit status. Detaching and a session the Hangar ended both exit 0; " +
				"a connection that dropped exits 8 with shell.disconnected.",
			"Output is the far end's own bytes, including escape sequences and colour. It " +
				"is not records, and --json and --fields do not apply to a live session.",
		},

		Related: []string{"hangar list", "hangar get", "app shell", "team use", "status"},
		DocsURL: "https://docs.outplane.com/cli/hangar",
	}
}
