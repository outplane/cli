package registry

// Team commands.
//
// One fact shapes this entire group, and it is a limitation rather than a
// design choice:
//
// The CLI cannot ask the API which teams a user belongs to. GET /User/GetUserTeams
// exists and looks like exactly the endpoint for it, but under an API token the
// server resolves the caller's identity to the literal string "api-token:{guid}",
// matches no user, and returns an EMPTY LIST rather than an error. A CLI that
// called it would confidently report "you have no teams" to somebody with six.
//
// So `team list` lists the teams this machine holds a credential for, which is
// a strictly smaller set, and every piece of its documentation has to say so.
// The console is the only place the real list exists.
//
// Both commands here are local. Neither makes a request, and both work with the
// network down.

func init() {
	Register(
		teamList(),
		teamUse(),
	)
}

func teamList() Command {
	return Command{
		Path:    []string{"team", "list"},
		Aliases: []string{"teams"},
		Short:   "list the teams this machine is signed in to",
		Long: "Lists the teams a credential is stored for on this machine.\n\n" +
			"This is not the list of teams you belong to. A token can only describe " +
			"its own team, so the CLI knows about a team once you have signed in to " +
			"it and not before. Your full list of teams is in the console.\n\n" +
			"Makes no network request.",

		Risk: RiskRead,
		// Deliberately false. "Which teams am I signed in to" is a question
		// worth answering when the answer is "none", and that is exactly the
		// moment an auth error would be least helpful.
		RequiresAuth: false,
		Session:      SessionAny,
		Idempotent:   true,

		OutputFields: []Field{
			{Name: "slug", Type: "string", Description: "the value to pass to --team"},
			{Name: "teamId", Type: "string"},
			{
				Name: "active",
				Type: "bool",
				Description: "true for the team this invocation would use, which is not " +
					"always the one `team use` selected",
			},
			{Name: "tokenName", Type: "string | null", Description: "the label shown in the console"},
			{Name: "expiresAt", Type: "string | null", Description: "RFC 3339"},
			{Name: "daysLeft", Type: "int | null", Description: "null when the token never expires, negative once it has passed"},
			{Name: "expired", Type: "bool", Description: "read from the token, not confirmed with the server"},
		},

		ExitCodes: []int{0, 2},

		Examples: []Example{
			{
				Title:   "list the teams signed in on this machine",
				Command: "outplane team list",
				Argv:    []string{"outplane", "team", "list"},
				Risk:    RiskRead,
			},
			{
				Title:   "find the active team's slug, for a script",
				Command: "outplane team list --json --fields slug,active",
				Argv:    []string{"outplane", "team", "list", "--json", "--fields", "slug,active"},
				Risk:    RiskRead,
				OutputSample: map[string]any{
					"items": []any{
						map[string]any{"slug": "acme", "active": true},
						map[string]any{"slug": "beta", "active": false},
					},
					"total":     2,
					"truncated": false,
				},
			},
		},

		AutomationNotes: []string{
			"This lists teams with a stored credential, not teams you belong to. A team " +
				"missing from this list has not been signed in to on this machine; it does " +
				"not mean the team does not exist.",
			"An empty list is a normal result, not an error. It means nobody has signed in " +
				"on this machine.",
			"With OUTPLANE_TOKEN set, the active team is the token's own team, and it may " +
				"not appear in this list at all. The token wins over everything stored.",
			"expired is decoded from the token and is not a check with the server. A token " +
				"revoked in the console still reads as valid here.",
		},

		Related: []string{"team use", "login", "whoami", "status"},
		DocsURL: "https://docs.outplane.com/cli/team",
	}
}

func teamUse() Command {
	return Command{
		Path:  []string{"team", "use"},
		Short: "choose the team commands act on by default",
		Long: "Selects which stored credential later commands use.\n\n" +
			"This is a preference, not the last word. A token in the environment and " +
			"an explicit --team both outrank it, as does a linked directory. Run " +
			"`outplane status` to see what is actually in effect and why.\n\n" +
			"Makes no network request.",

		Risk: RiskWrite,
		// The point is to choose among stored credentials, so the interesting
		// failure is "that team is not one of them", not "you are not signed
		// in". Reporting the latter first would hide the list that fixes it.
		RequiresAuth: false,
		Session:      SessionAny,
		Idempotent:   true,

		// The team is the argument. A --team flag beside it would offer two
		// ways to name the same thing in one invocation, and `team use --team
		// acme` is a sentence somebody will write and be surprised by.
		SuppressGlobals: []string{"team"},

		Args: []Arg{
			{
				Name:     "team",
				Short:    "team slug or id, as shown by `outplane team list`",
				Required: true,
				Resolves: "team",
			},
		},

		OutputFields: []Field{
			{Name: "slug", Type: "string"},
			{Name: "teamId", Type: "string"},
			{Name: "changed", Type: "bool", Description: "false when this team was already active"},
			{
				Name: "effective",
				Type: "bool",
				Description: "false when something outranks this choice, such as OUTPLANE_TOKEN " +
					"or a linked directory",
			},
		},

		ErrorCodes: []string{"auth.team_not_signed_in"},
		ExitCodes:  []int{0, 2, 3},

		Examples: []Example{
			{
				Title:        "switch to another team",
				Command:      "outplane team use acme",
				Argv:         []string{"outplane", "team", "use", "acme"},
				Placeholders: map[string]string{"acme": "<TEAM_SLUG>"},
				Risk:         RiskWrite,
			},
			{
				Title:   "act on one team once, without switching",
				Command: "outplane app list --team beta",
				Argv:    []string{"outplane", "app", "list", "--team", "beta"},
				Risk:    RiskRead,
			},
		},

		AutomationNotes: []string{
			"This writes a preference to this machine's config. In CI, do not use it: set " +
				"OUTPLANE_TOKEN, which names its own team, or pass --team per command.",
			"Selecting a team the CLI holds no credential for fails. Signing in is what adds " +
				"one, and the team is chosen in the console while doing so.",
			"An effective value of false means the choice was saved but something outranks it " +
				"right now. The command still succeeds, because the preference is real and " +
				"will apply once the thing outranking it is gone.",
		},

		Related: []string{"team list", "login", "status", "link"},
		DocsURL: "https://docs.outplane.com/cli/team",
	}
}
