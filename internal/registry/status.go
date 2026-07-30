package registry

// status answers one question: what will the next command do, and why.
//
// It exists because the CLI resolves a team from six possible places, and when
// the answer surprises somebody, "which one won" is the only useful thing to
// know. Every other command hides that chain; this one prints it.
//
// Local, like `whoami`. The two differ in what they are for rather than in what
// they touch: whoami answers "who am I" and exits 3 when there is no answer,
// which is what a script wants; status answers "what is configured and where
// did it come from" and always exits 0, which is what a person debugging wants.

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
			"Makes no network request, so it answers while the API is unreachable. " +
			"That also means it cannot tell you a token has been revoked; expiry it " +
			"can see, because the expiry is inside the token.",

		Risk: RiskRead,
		// Deliberately false, and this is the whole point of the command. "I
		// cannot authenticate" is the state status exists to explain, so
		// refusing to run without a credential would withhold the diagnosis at
		// exactly the moment it is wanted.
		RequiresAuth: false,
		Session:      SessionAny,
		Idempotent:   true,

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
				Title:   "find out why a command used the wrong team",
				Command: "outplane status --json --fields teamSlug,teamSource",
				Argv:    []string{"outplane", "status", "--json", "--fields", "teamSlug,teamSource"},
				Risk:    RiskRead,
				OutputSample: map[string]any{
					"teamSlug":   "acme",
					"teamSource": "link file",
				},
			},
			{
				Title:   "decide in a script whether to sign in",
				Command: "outplane status --json --fields signedIn,expired",
				Argv:    []string{"outplane", "status", "--json", "--fields", "signedIn,expired"},
				Risk:    RiskRead,
				OutputSample: map[string]any{
					"signedIn": true,
					"expired":  false,
				},
			},
		},

		AutomationNotes: []string{
			"This command exits 0 even when nothing is configured. It is a report, not an " +
				"assertion: the findings are in the fields. Use `outplane whoami`, which " +
				"exits 3, to assert that a credential exists.",
			"teamSource names the winner of the resolution chain. The order is: --token, " +
				"OUTPLANE_TOKEN, --team, OUTPLANE_TEAM_ID, the directory link, then the team " +
				"set by `outplane team use`.",
			"No network request is made. expired is decoded from the token, so a token revoked " +
				"in the console still reads as fine here and only fails on the next real " +
				"command.",
			"problem is a sentence for a human. Branch on the typed fields instead: signedIn, " +
				"expired, teamSource.",
		},

		Related: []string{"whoami", "team list", "team use", "login", "link"},
		DocsURL: "https://docs.outplane.com/cli/status",
	}
}
