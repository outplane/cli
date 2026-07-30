package registry

// status answers one question: what will the next command do, and why.
//
// It exists because the CLI resolves a team from six possible places, and when
// the answer surprises somebody, "which one won" is the only useful thing to
// know. Every other command hides that chain; this one prints it.
//
// It is the network-touching counterpart to `whoami`. whoami reads the stored
// token and never makes a request, which is what lets it answer while the API
// is down. status verifies, which is what lets it say the token has been
// revoked. Neither could do the other's job without becoming worse at its own.

func init() {
	Register(status())
}

func status() Command {
	return Command{
		Path:  []string{"status"},
		Short: "show which team, token and API the next command will use",
		Long: "Reports the resolved context and where each part of it came from.\n\n" +
			"A team can be selected six ways, and this shows which one is in effect. " +
			"Run it whenever a command acts on a team you did not expect.\n\n" +
			"Unless --offline is passed, it also makes one request to confirm the " +
			"credential still works, because a token revoked in the console looks " +
			"perfectly valid until something tries to use it.",

		Risk: RiskRead,
		// Deliberately false, and this is the whole point of the command. "I
		// cannot authenticate" is the state status exists to explain, so
		// refusing to run without a credential would withhold the diagnosis at
		// exactly the moment it is wanted.
		RequiresAuth: false,
		Session:      SessionAny,
		Idempotent:   true,

		APICalls: []string{"GET /api/LogMonitor/LogQueryVerify"},

		Flags: []Flag{
			{
				Name:        "offline",
				Type:        "bool",
				Default:     "false",
				Description: "skip the credential check and report only local state",
			},
		},

		OutputFields: []Field{
			{Name: "signedIn", Type: "bool", Description: "a credential was resolved"},
			{Name: "teamSlug", Type: "string | null"},
			{Name: "teamId", Type: "string | null"},
			{
				Name: "teamSource",
				Type: "string | null",
				Description: "which of the six places the team came from " +
					"(flag | environment | link file | config file | token claim)",
			},
			{Name: "tokenSource", Type: "string | null", Description: "same set, for the credential"},
			{Name: "expiresAt", Type: "string | null", Description: "RFC 3339, read from the token"},
			{Name: "daysLeft", Type: "int | null", Description: "null when the token never expires, negative once it has passed"},
			{Name: "expired", Type: "bool"},
			{Name: "apiUrl", Type: "string"},
			{Name: "apiUrlSource", Type: "string"},
			{
				Name: "credentialValid",
				Type: "bool | null",
				Description: "the result of the check. null when --offline, " +
					"or when the API could not be reached at all",
			},
			{
				Name:        "problem",
				Type:        "string | null",
				Description: "what is wrong, in one sentence, when something is",
			},
			{Name: "linkedDirectory", Type: "string | null", Description: "path of the link file in effect"},
			{Name: "configDirectory", Type: "string"},
			{Name: "version", Type: "string"},
		},

		// Exits 0 even when nothing is configured. See the automation notes.
		ExitCodes: []int{0, 2},

		Examples: []Example{
			{
				Title:   "see what the next command will act on",
				Command: "outplane status",
				Argv:    []string{"outplane", "status"},
				Risk:    RiskRead,
			},
			{
				Title:   "check local state only, making no request",
				Command: "outplane status --offline",
				Argv:    []string{"outplane", "status", "--offline"},
				Risk:    RiskRead,
			},
			{
				Title:   "decide in a script whether to sign in",
				Command: "outplane status --json --fields signedIn,credentialValid",
				Argv:    []string{"outplane", "status", "--json", "--fields", "signedIn,credentialValid"},
				Risk:    RiskRead,
				OutputSample: map[string]any{
					"signedIn":        true,
					"credentialValid": true,
				},
			},
		},

		AutomationNotes: []string{
			"This command exits 0 even when nothing is configured and even when the token " +
				"is rejected. It is a report, not an assertion: the findings are in the " +
				"fields. Use `outplane whoami`, which exits 3, to assert that a credential " +
				"exists.",
			"credentialValid is null rather than false when the API could not be reached. " +
				"A network failure is not evidence that a token is bad, and treating the two " +
				"the same is how a CI job deletes a working credential during an outage.",
			"teamSource names the winner of the resolution chain. The order is: --token, " +
				"OUTPLANE_TOKEN, --team, OUTPLANE_TEAM_ID, the directory link, then the team " +
				"set by `outplane team use`.",
			"With --offline no request is made, so credentialValid is null and an expired or " +
				"revoked token is not detected.",
		},

		Related: []string{"whoami", "team list", "team use", "login", "link"},
		DocsURL: "https://docs.outplane.com/cli/status",
	}
}
