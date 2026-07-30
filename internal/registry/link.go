package registry

// Directory linking.
//
// A link is a small JSON file at .outplane/link.json saying which team, and
// optionally which application, the directory it sits in belongs to. Commands
// run inside that directory target it without anyone passing a flag.
//
// Two facts shape both commands:
//
//   - The file is found by walking UP from the working directory, so a link in
//     a repository root covers every directory beneath it. That is the useful
//     behaviour and also the surprising one, which is why both commands print
//     the path they acted on rather than assuming it is nearby.
//   - The file holds no credential. It names a team and an app, both of which
//     appear in ordinary URLs, so committing it to a repository is safe and is
//     usually what a team wants: a colleague who clones the project inherits
//     the right target.

func init() {
	Register(
		link(),
		unlink(),
	)
}

func link() Command {
	return Command{
		Path:  []string{"link"},
		Short: "point this directory at a team, and optionally an application",
		Long: "Writes .outplane/link.json so commands run here need no --team.\n\n" +
			"With no argument it links the team alone, which suits a repository " +
			"holding several applications. Name an application and commands that " +
			"take one will default to it.\n\n" +
			"The file contains no credential. It is safe to commit, and a colleague " +
			"who clones the repository inherits the same target.",

		Risk: RiskWrite,
		// Resolving an application name needs a credential, and so does knowing
		// which team to record.
		RequiresAuth: true,
		Session:      SessionAny,
		// Linking to the same target twice leaves the same file.
		Idempotent: true,

		APICalls: []string{"GET /api/App/GetAppsByTeamId"},

		Args: []Arg{
			{
				Name:     "app",
				Short:    "application name or id. Omit to link the team only",
				Required: false,
				Resolves: "app",
			},
		},

		OutputFields: []Field{
			{Name: "path", Type: "string", Description: "the file that was written"},
			{Name: "teamSlug", Type: "string"},
			{Name: "teamId", Type: "string"},
			{Name: "appName", Type: "string | null", Description: "null when only the team was linked"},
			{Name: "appId", Type: "string | null"},
			{Name: "changed", Type: "bool", Description: "false when the file already said this"},
		},

		ErrorCodes: []string{"app.not_found", "app.ambiguous", "auth.not_authenticated", "link.unreadable"},
		ExitCodes:  []int{0, 2, 3, 5, 8},

		Examples: []Example{
			{
				Title:   "link this directory to the active team",
				Command: "outplane link",
				Argv:    []string{"outplane", "link"},
				Risk:    RiskWrite,
			},
			{
				Title:        "link to a specific application",
				Command:      "outplane link checkout",
				Argv:         []string{"outplane", "link", "checkout"},
				Placeholders: map[string]string{"checkout": "<APP_NAME>"},
				Risk:         RiskWrite,
			},
			{
				Title:        "link to an application in another team",
				Command:      "outplane link checkout --team acme",
				Argv:         []string{"outplane", "link", "checkout", "--team", "acme"},
				Placeholders: map[string]string{"checkout": "<APP_NAME>", "acme": "<TEAM_SLUG>"},
				Risk:         RiskWrite,
			},
		},

		AutomationNotes: []string{
			"The link is written in the current directory. A link already present in a " +
				"parent directory is not edited; the new one shadows it for this subtree.",
			"A link outranks `outplane team use` but loses to --team and to OUTPLANE_TOKEN. " +
				"Run `outplane status` to see which one is winning.",
			"In CI, do not link. Set OUTPLANE_TOKEN, which names its own team and outranks " +
				"any link a checkout happens to contain.",
			"An application reference is matched exactly against id, then name, then display " +
				"name. There is no fuzzy matching, and a display name shared by two " +
				"applications is an error rather than a guess.",
		},

		Related: []string{"unlink", "status", "team use", "app list"},
		DocsURL: "https://docs.outplane.com/cli/link",
	}
}

func unlink() Command {
	return Command{
		Path:  []string{"unlink"},
		Short: "remove this directory's link",
		Long: "Deletes the .outplane/link.json that is in effect here.\n\n" +
			"Because links are found by walking up, the file removed may live in a " +
			"parent directory and may be serving other directories beside this one. " +
			"The path is always printed for that reason.\n\n" +
			"Makes no network request.",

		Risk: RiskWrite,
		// Deleting a local file needs no credential, and refusing to tidy up
		// because a token expired would be absurd.
		RequiresAuth: false,
		Session:      SessionAny,
		Idempotent:   true,

		OutputFields: []Field{
			{Name: "removed", Type: "bool"},
			{Name: "path", Type: "string | null", Description: "the file that was deleted, if any"},
			{Name: "changed", Type: "bool"},
		},

		ExitCodes: []int{0, 2},

		Examples: []Example{
			{
				Title:   "remove the link in effect here",
				Command: "outplane unlink",
				Argv:    []string{"outplane", "unlink"},
				Risk:    RiskWrite,
			},
		},

		AutomationNotes: []string{
			"Removing nothing is a success, not an error, so a teardown script can run this " +
				"unconditionally. changed says whether a file was actually deleted.",
			"A link file that cannot be parsed is removed rather than refused. Repairing that " +
				"is what this command is for, and while it exists every command needing a " +
				"team fails with link.unreadable.",
			"The file removed is the one in effect, which may sit in a parent directory. " +
				"Read path in the result to see what was actually deleted.",
			"The empty .outplane directory is removed too, so nothing is left behind for " +
				"somebody to commit by accident.",
		},

		Related: []string{"link", "status"},
		DocsURL: "https://docs.outplane.com/cli/link",
	}
}
