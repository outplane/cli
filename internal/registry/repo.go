package registry

// Repository listing.
//
// Named for what it returns rather than for the host it happens to read from,
// because a second provider would otherwise arrive as a second command doing
// the same job. Each entry carries its own provider instead.
//
// Two facts shape it:
//
//   - The list is per user, not per team. It comes from the connections of
//     whoever created the token, which is why an application cannot be created
//     from a repository a colleague can see and you cannot.
//   - The endpoint's paging arguments are applied to each connection separately
//     and the results concatenated, so a page number means nothing. Everything
//     is returned instead.

func init() {
	Register(repoList())
}

func repoList() Command {
	return Command{
		Path:    []string{"repos"},
		Aliases: []string{"repo"},
		Short:   "list the repositories you can deploy from",
		Long: "Lists every repository your account has connected to Out Plane.\n\n" +
			"This is your own list, not your team's. A repository a colleague can " +
			"deploy will not appear here unless you have access to it too, which is " +
			"how it works everywhere else that deploys from a repository.\n\n" +
			"The address for connecting more is printed every time, because the usual " +
			"problem is not an empty list but one repository missing from it.",

		Risk:         RiskRead,
		RequiresAuth: true,
		Session:      SessionAny,
		Idempotent:   true,

		APICalls: []string{"GET /api/GitProvider/Github/Repositories"},

		Flags: []Flag{
			{
				Name: "search",
				Type: "string",
				Description: "filter by name. " +
					"Applied locally: the API returns the full list",
			},
		},

		OutputFields: []Field{
			{Name: "fullName", Type: "string", Description: "owner/name, the form other commands take"},
			{Name: "name", Type: "string"},
			{Name: "provider", Type: "string", Description: "where it lives", Enum: []string{"github"}},
			{Name: "private", Type: "bool"},
			{Name: "defaultBranch", Type: "string", Description: "what a deployment builds unless told otherwise"},
			{Name: "language", Type: "string | null", Description: "as reported by the host"},
			{Name: "archived", Type: "bool", Description: "read-only upstream, still deployable"},
			{Name: "url", Type: "string"},
		},

		ErrorCodes: []string{"repos.not_connected", "auth.not_authenticated"},
		ExitCodes:  []int{0, 2, 3, 8},

		Examples: []Example{
			{
				Title:   "list the repositories you can deploy",
				Command: "outplane repos",
				Argv:    []string{"outplane", "repos"},
				Risk:    RiskRead,
			},
			{
				Title:   "find one by name",
				Command: "outplane repos --search checkout",
				Argv:    []string{"outplane", "repos", "--search", "checkout"},
				Risk:    RiskRead,
			},
			{
				Title:   "take just the names, for a script",
				Command: "outplane repos --json --fields fullName,defaultBranch",
				Argv:    []string{"outplane", "repos", "--json", "--fields", "fullName,defaultBranch"},
				Risk:    RiskRead,
				OutputSample: map[string]any{
					"items": []any{
						map[string]any{"fullName": "acme/checkout", "defaultBranch": "main"},
					},
					"total":     1,
					"truncated": false,
				},
			},
		},

		AutomationNotes: []string{
			"The list belongs to the user who created the token, not to the team. Two members " +
				"of one team can see different repositories here, and each can only create " +
				"applications from their own.",
			"Everything is returned in one response. The endpoint's paging arguments are " +
				"applied per connection and concatenated, so they cannot be used, and this " +
				"command does not offer them.",
			"An empty list means nothing is connected yet, not that the account has no " +
				"repositories. Connecting happens in a browser and cannot be done from here; " +
				"the address is in the error and in every successful run.",
			"A repository missing from a non-empty list needs its access granting at the same " +
				"address. That is the usual case, not the empty one.",
			"Repositories that are disabled upstream are left out, because they cannot be " +
				"cloned and so could never build. Archived ones are listed and marked.",
		},

		Related: []string{"app list", "deploy create", "status"},
		DocsURL: "https://docs.outplane.com/cli/repos",
	}
}
