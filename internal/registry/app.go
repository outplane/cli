package registry

// Application commands.
//
// Platform facts that shape this group:
//
//   - The API has no lookup-by-name endpoint for anything. Every path parameter
//     is a GUID, so turning "checkout" into an id is entirely the client's job:
//     list the team's apps and match. That is what Arg.Resolves drives.
//   - Team-scoped list endpoints require the X-Team-Id header, and the header
//     is checked before any authentication logic runs. A missing header is a
//     400, not a 403, even with a perfectly good token.
//   - App list returns the whole set. There is no pagination, no filtering and
//     no server-side search, so --search filters on the client and the result
//     is complete rather than a page.

func init() {
	Register(
		appList(),
		appDelete(),
	)
}

// appList is the simplest command in the CLI and therefore the reference
// example. A new read-only command should look exactly like this one.
func appList() Command {
	return Command{
		Path:  []string{"app", "list"},
		Short: "list the applications in a team",
		Long: "Lists every application in the current team.\n\n" +
			"The team comes from --team, then OUTPLANE_TEAM_ID, then the linked " +
			"directory, then the token's own team. Run `outplane status` to see " +
			"which one is in effect and where it came from.",

		Risk:         RiskRead,
		RequiresAuth: true,
		Session:      SessionAny,
		Idempotent:   true,

		APICalls: []string{"GET /api/App/GetAppsByTeamId"},

		Flags: []Flag{
			{
				Name: "search",
				Type: "string",
				Description: "filter by name or display name. " +
					"Applied locally: the API returns the full list",
			},
		},

		OutputFields: []Field{
			{Name: "id", Type: "string"},
			{Name: "name", Type: "string", Description: "immutable internal name, used in URLs"},
			{Name: "displayName", Type: "string", Description: "editable label, may be empty"},
			{Name: "status", Type: "string"},
			{Name: "instances", Type: "int", Description: "configured replica count, 1 to 5"},
			{Name: "size", Type: "string", Description: "instance type code, e.g. op-20"},
			{Name: "url", Type: "string | null", Description: "public URL, if a public HTTP port exists"},
			{Name: "updatedAt", Type: "string", Description: "RFC 3339"},
		},

		ErrorCodes: []string{"context.no_team", "auth.token_invalid"},
		ExitCodes:  []int{0, 2, 3, 8},

		Examples: []Example{
			{
				Title:   "list the applications in the current team",
				Command: "outplane app list",
				Argv:    []string{"outplane", "app", "list"},
				Risk:    RiskRead,
			},
			{
				Title:   "list as JSON and pick out the names",
				Command: "outplane app list --json --fields name,status",
				Argv:    []string{"outplane", "app", "list", "--json", "--fields", "name,status"},
				Risk:    RiskRead,
				OutputSample: map[string]any{
					"items": []any{
						map[string]any{"name": "checkout", "status": "Running"},
						map[string]any{"name": "worker", "status": "Running"},
					},
					"total":     2,
					"truncated": false,
				},
			},
			{
				Title:        "list the applications in a specific team",
				Command:      "outplane app list --team acme",
				Argv:         []string{"outplane", "app", "list", "--team", "acme"},
				Placeholders: map[string]string{"acme": "<TEAM_SLUG>"},
				Risk:         RiskRead,
			},
		},

		AutomationNotes: []string{
			"The API returns every application in one response. There is no pagination, " +
				"so `total` is the complete count and `truncated` is always false.",
			"--search filters the response on the client. It does not reduce what is fetched.",
		},

		Related: []string{"app get", "app instances", "deploy list", "status"},

		DocsURL: "https://docs.outplane.com/cli/app",
	}
}

// appDelete is the reference example for a destructive command. Everything
// about the confirmation protocol is visible in this declaration.
func appDelete() Command {
	return Command{
		Path:  []string{"app", "delete"},
		Short: "permanently delete an application",
		Long: "Deletes an application and everything attached to it.\n\n" +
			"There is no undelete and no retention window. Detached volumes survive; " +
			"attached ones and custom domains do not. Run with --dry-run first to see " +
			"exactly what would be destroyed.",

		Risk:         RiskDestructive,
		RequiresAuth: true,
		Session:      SessionAny,
		Idempotent:   false,

		SupportsDryRun: true,

		APICalls: []string{"DELETE /api/App/DeleteApplication/{appId}"},

		Args: []Arg{
			{
				Name:     "app",
				Short:    "app name or id",
				Required: true,
				Resolves: "app",
				Pattern:  "^[a-zA-Z0-9]{5,45}$",
			},
		},

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
				Description: "the app's name, typed again. Required, because a single " +
					"--force is too easy for an agent to emit by accident",
			},
		},

		OutputFields: []Field{
			{Name: "deleted", Type: "bool"},
			{Name: "app", Type: "object", Description: "{id, name}"},
			{
				Name:        "cascaded",
				Type:        "object",
				Description: "what went with it: {volumes: [], domains: [], deployments: int}",
			},
		},

		ErrorCodes: []string{"app.not_found", "app.confirm_name_mismatch", "confirmation.required"},
		ExitCodes:  []int{0, 2, 3, 4, 5, 8},

		Examples: []Example{
			{
				Title:        "see exactly what deletion would destroy",
				Command:      "outplane app delete checkout --dry-run --json",
				Argv:         []string{"outplane", "app", "delete", "checkout", "--dry-run", "--json"},
				Placeholders: map[string]string{"checkout": "<APP_NAME>"},
				Risk:         RiskRead,
			},
			{
				Title:        "request deletion. Returns exit 4 with a command to replay",
				Command:      "outplane app delete checkout",
				Argv:         []string{"outplane", "app", "delete", "checkout"},
				Placeholders: map[string]string{"checkout": "<APP_NAME>"},
				Risk:         RiskDestructive,
			},
			{
				Title:        "the confirmed form, which a human approves",
				Command:      "outplane app delete checkout --yes --confirm-name checkout",
				Argv:         []string{"outplane", "app", "delete", "checkout", "--yes", "--confirm-name", "checkout"},
				Placeholders: map[string]string{"checkout": "<APP_NAME>"},
				Risk:         RiskDestructive,
			},
		},

		AutomationNotes: []string{
			"This command never prompts. Without confirmation it exits 4 and returns the exact " +
				"command to replay in the error's confirmCommand field.",
			"Exit 4 is not a failure. It means the confirmation belongs to a human.",
			"Under a detected agent harness it exits 4 even when --yes and --confirm-name are " +
				"supplied, so the approval gate stays outside the CLI.",
			"Deletion is irreversible. There is no undelete and no retention window.",
		},

		Related: []string{"app list", "app get", "volume detach", "volume list"},

		DocsURL: "https://docs.outplane.com/cli/app",
	}
}
