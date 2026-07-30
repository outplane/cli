package registry

// Authentication commands.
//
// Facts that shape this group:
//
//   - A token is scoped to exactly one team, permanently, because the team is a
//     claim inside it. Signing in therefore stores a credential PER TEAM, and
//     signing in again adds another rather than replacing the first.
//   - `login` deliberately has no team flag. Before signing in, a user has no
//     way to know a team slug, so asking for one would be asking them for the
//     thing they are trying to discover. The team is chosen in the console,
//     which is the only place their list of teams exists.
//   - The token is shown by the console exactly once, on creation. There is no
//     endpoint that returns it again, so losing it means revoking and starting
//     over.

func init() {
	Register(
		login(),
		logout(),
		whoami(),
	)
}

func login() Command {
	return Command{
		Path:  []string{"login"},
		Short: "sign in to a team",
		Long: "Signs in by storing an API token for one team.\n\n" +
			"Tokens are created in the console, where your teams are listed. Run this " +
			"again to sign in to a second team; existing credentials are kept, and " +
			"`outplane team use` switches between them.",

		Risk: RiskWrite,
		// The whole point of this command is to obtain a credential, so it
		// cannot require one.
		RequiresAuth: false,
		Session:      SessionAny,
		// Signing in with the same token twice leaves the same state.
		Idempotent: true,

		APICalls: []string{"GET /api/LogMonitor/LogQueryVerify"},

		// The team comes from the console, so a team flag here would be an
		// option that does nothing. See the note at the top of this file.
		SuppressGlobals: []string{"team"},

		Flags: []Flag{
			{
				Name:        "token-stdin",
				Type:        "bool",
				Default:     "false",
				Description: "read the token from standard input instead of prompting",
			},
			{
				Name:        "token",
				Type:        "string",
				Description: "the token itself",
				Discouraged: "argv is visible in process lists and CI logs. Use --token-stdin, " +
					"or set OUTPLANE_TOKEN and skip signing in altogether",
			},
			{
				Name:        "no-browser",
				Type:        "bool",
				Default:     "false",
				Description: "print the console URL instead of opening it",
			},
		},

		OutputFields: []Field{
			{Name: "teamSlug", Type: "string"},
			{Name: "teamId", Type: "string"},
			{Name: "expiresAt", Type: "string | null", Description: "RFC 3339, or null if it never expires"},
			{Name: "changed", Type: "bool", Description: "false when this credential was already stored"},
		},

		ErrorCodes: []string{"auth.token_invalid", "auth.token_malformed"},
		ExitCodes:  []int{0, 2, 3, 8},

		Examples: []Example{
			{
				Title:   "sign in, choosing the team in the console",
				Command: "outplane login",
				Argv:    []string{"outplane", "login"},
				Risk:    RiskWrite,
			},
			{
				Title:   "sign in without a browser, on a remote machine",
				Command: "outplane login --no-browser",
				Argv:    []string{"outplane", "login", "--no-browser"},
				Risk:    RiskWrite,
			},
			{
				Title:   "sign in from a script, keeping the token out of argv",
				Command: "cat token.txt | outplane login --token-stdin",
				Argv:    []string{"outplane", "login", "--token-stdin"},
				Risk:    RiskWrite,
			},
		},

		AutomationNotes: []string{
			"In CI, do not sign in. Set OUTPLANE_TOKEN and every command will use it; " +
				"the token names its own team.",
			"A token belongs to one team. Signing in again adds a second credential " +
				"rather than replacing the first.",
			"The console shows a token once, when it is created. It cannot be retrieved " +
				"afterwards, only revoked and replaced.",
			"There is no team flag: the team is chosen in the console.",
		},

		Related: []string{"logout", "whoami", "team list", "team use", "status"},
		DocsURL: "https://docs.outplane.com/cli/login",
	}
}

func logout() Command {
	return Command{
		Path:  []string{"logout"},
		Short: "remove a stored credential",
		Long: "Removes this machine's stored token for a team.\n\n" +
			"The token itself is not revoked and keeps working anywhere else it is " +
			"used. Revoke it in the console if that is what you want.",

		Risk:         RiskWrite,
		RequiresAuth: false,
		Session:      SessionAny,
		Idempotent:   true,

		Flags: []Flag{
			{
				Name:        "all",
				Type:        "bool",
				Default:     "false",
				Description: "remove the stored credentials for every team",
			},
		},

		OutputFields: []Field{
			{Name: "removed", Type: "string[]", Description: "team slugs whose credentials were removed"},
			{Name: "changed", Type: "bool"},
		},

		ExitCodes: []int{0, 2, 3},

		Examples: []Example{
			{
				Title:   "sign out of the active team",
				Command: "outplane logout",
				Argv:    []string{"outplane", "logout"},
				Risk:    RiskWrite,
			},
			{
				Title:   "sign out of one team, keeping the others",
				Command: "outplane logout --team acme",
				Argv:    []string{"outplane", "logout", "--team", "acme"},
				Risk:    RiskWrite,
			},
			{
				Title:   "remove every stored credential from this machine",
				Command: "outplane logout --all",
				Argv:    []string{"outplane", "logout", "--all"},
				Risk:    RiskWrite,
			},
		},

		AutomationNotes: []string{
			"This removes the local copy only. The token stays valid until it is " +
				"revoked in the console, so a leaked token needs revoking, not logging out.",
		},

		Related: []string{"login", "team list", "whoami"},
		DocsURL: "https://docs.outplane.com/cli/login",
	}
}

func whoami() Command {
	return Command{
		Path:  []string{"whoami"},
		Short: "show the active credential",
		Long: "Shows which team the CLI is currently acting as, and when its token expires.\n\n" +
			"This reads the stored token locally and makes no network request, so it " +
			"answers even when the API is unreachable.",

		Risk:         RiskRead,
		RequiresAuth: true,
		Session:      SessionAny,
		Idempotent:   true,

		OutputFields: []Field{
			{
				Name: "teamSlug",
				Type: "string | null",
				Description: "null for a token supplied through the environment, " +
					"which carries a team id but no slug",
			},
			{Name: "teamId", Type: "string", Description: "always present"},
			{
				Name:        "tokenName",
				Type:        "string | null",
				Description: "the label shown in the console, for a stored credential",
			},
			{Name: "expiresAt", Type: "string | null", Description: "RFC 3339, read from the token"},
			{Name: "daysLeft", Type: "int", Description: "-1 when the token never expires"},
			{
				Name:        "source",
				Type:        "string",
				Description: "where the credential came from (environment | config file | flag)",
			},
		},

		ErrorCodes: []string{"auth.not_authenticated", "auth.team_not_signed_in"},
		ExitCodes:  []int{0, 3},

		Examples: []Example{
			{
				Title:   "show the active credential",
				Command: "outplane whoami",
				Argv:    []string{"outplane", "whoami"},
				Risk:    RiskRead,
			},
			{
				Title:        "check which team a script will act as",
				Command:      "outplane whoami --json --fields teamSlug",
				Argv:         []string{"outplane", "whoami", "--json", "--fields", "teamSlug"},
				Risk:         RiskRead,
				OutputSample: map[string]any{"teamSlug": "acme"},
			},
			{
				Title:   "check a specific team's stored credential",
				Command: "outplane whoami --team acme",
				Argv:    []string{"outplane", "whoami", "--team", "acme"},
				Risk:    RiskRead,
			},
		},

		AutomationNotes: []string{
			"No network request is made. Expiry is read from the token itself, so a " +
				"token revoked in the console still appears here until a command uses it.",
			"With OUTPLANE_TOKEN set, teamSlug is null: a slug is only learned by signing " +
				"in, and teamId is the identifier every API call actually uses.",
		},

		Related: []string{"status", "team list", "login"},
		DocsURL: "https://docs.outplane.com/cli/whoami",
	}
}
