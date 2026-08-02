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
		appGet(),
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
			{
				Name: "status",
				Type: "string",
				Description: "effective state: paused, ready, building, deploying, queued, " +
					"failed, crashed, canceled. The field to branch on",
			},
			{
				Name:        "deploymentStatus",
				Type:        "string",
				Description: "last deployment's own state, which pausing does not change",
			},
			{Name: "paused", Type: "bool"},
			{
				Name:        "instances",
				Type:        "int",
				Description: "configured replica count, 1 to 5. Unchanged by pausing",
			},
			{Name: "size", Type: "string", Description: "instance type code, e.g. op-20"},
			{
				Name: "source",
				Type: "string",
				Description: "where the image comes from: github, container-registry, " +
					"or unknown:N for a provider this release predates",
			},
			{
				Name:        "lastDeployedAt",
				Type:        "string",
				Description: "when the last deployment started. RFC 3339, UTC",
			},
			{
				Name: "updatedAt",
				Type: "string",
				Description: "when the app's own record last changed, which a deployment does " +
					"not touch. RFC 3339, UTC",
			},
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
						map[string]any{"name": "checkout", "status": "ready"},
						map[string]any{"name": "worker", "status": "paused"},
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
			"A paused app reports status \"paused\", not the state of its last deployment. " +
				"Filtering on status \"ready\" therefore never returns a stopped app; read " +
				"deploymentStatus to see what it will return to.",
			"status \"ready\" describes the last deployment, not a health check. An app can be " +
				"ready and still be serving errors.",
			"An unrecognised state decodes to \"unknown:N\", carrying the number the server sent. " +
				"This is how a new platform state reaches an older CLI; treat it as unknown " +
				"rather than as a failure.",
			"This command reports no URL. A public address needs the app's port, which is a " +
				"separate request per app, so listing does not fetch one. `app get` has it.",
			"lastDeployedAt and updatedAt are different questions. The first is when the app " +
				"last deployed; the second is when its own record last changed, which a " +
				"deployment does not touch. Sort on the first to find what is stale.",
		},

		Related: []string{"app get", "deploy list", "metrics", "status"},

		DocsURL: "https://docs.outplane.com/cli/app",
	}
}

// appGet is the command that answers "where is it running, and from what".
//
// It is the only read command that costs two requests: one to turn a name into
// an id, one for the detail. That is a property of the API rather than a choice
// here, and the notes say so, because an agent that runs this once per app in a
// loop is making twice the calls it thinks it is.
func appGet() Command {
	return Command{
		Path:  []string{"app", "get"},
		Short: "show one application in detail",
		Long: "Reports one application: where it is reachable, what it is built from, " +
			"how large it is and how its last deployment ended.\n\n" +
			"The application can be given by name or by id, and can be omitted entirely " +
			"in a linked directory.\n\n" +
			"Environment variables are not reported, in any format. Their values are " +
			"usually credentials, and a command people run in front of other people, in a " +
			"shared terminal or in a CI log, must not be able to put one on the screen.",

		Risk:         RiskRead,
		RequiresAuth: true,
		Session:      SessionAny,
		Idempotent:   true,

		APICalls: []string{
			"GET /api/App/GetAppsByTeamId",
			"GET /api/App/GetAppById/{appId}",
		},

		Args: []Arg{
			{
				Name:     "app",
				Short:    "app name or id. Defaults to the linked app",
				Required: false,
				Resolves: "app",
				Pattern:  "^[a-zA-Z0-9]{5,45}$",
			},
		},

		OutputFields: []Field{
			{Name: "id", Type: "string"},
			{Name: "name", Type: "string", Description: "immutable internal name, used in URLs"},
			{Name: "displayName", Type: "string | null", Description: "editable label"},
			{
				Name: "status",
				Type: "string",
				Description: "effective state: paused, ready, building, deploying, queued, " +
					"failed, crashed, canceled. The field to branch on",
			},
			{
				Name:        "deploymentStatus",
				Type:        "string",
				Description: "last deployment's own state, which pausing does not change",
			},
			{Name: "paused", Type: "bool"},
			{Name: "instances", Type: "int", Description: "configured replica count, 1 to 5"},
			{Name: "size", Type: "string", Description: "instance type code, e.g. op-20"},
			{
				Name:        "source",
				Type:        "string",
				Description: "where the image comes from: github, container-registry, or unknown:N",
			},
			{
				Name: "url",
				Type: "string | null",
				Description: "where the app answers: the first public port's address, or its " +
					"first custom domain when no port is public. null when it serves nothing " +
					"over HTTP",
			},
			{
				Name: "endpoints",
				Type: "array",
				Description: "every port: {port, scheme, public, url, customDomains}. scheme is " +
					"http, h2c or tcp; url is null on a private port; customDomains are full " +
					"addresses, not host names",
			},
			{Name: "repository", Type: "string | null", Description: "owner/repository, for a Git-sourced app"},
			{Name: "branch", Type: "string | null", Description: "the branch that is deployed"},
			{
				Name: "imageRef",
				Type: "string | null",
				Description: "the image, for a container-registry app. Never set at the same " +
					"time as repository",
			},
			{Name: "sourceUrl", Type: "string | null", Description: "the repository's web address"},
			{Name: "publicSource", Type: "bool", Description: "the source needs no credential to read"},
			{
				Name:        "buildMethod",
				Type:        "string",
				Description: "dockerfile, buildpack or prebuilt-image",
			},
			{Name: "directory", Type: "string | null", Description: "sub-directory built, when the repository holds more than one app"},
			{Name: "startCommand", Type: "string | null", Description: "override for the image's own command"},
			{Name: "commitMessage", Type: "string | null", Description: "of the deployed commit"},
			{
				Name:        "lastDeployedAt",
				Type:        "string",
				Description: "when the last deployment started. RFC 3339, UTC",
			},
			{Name: "createdAt", Type: "string", Description: "RFC 3339, UTC"},
			{
				Name: "updatedAt",
				Type: "string",
				Description: "when the app's own record last changed, which a deployment does " +
					"not touch. RFC 3339, UTC",
			},
		},

		ErrorCodes: []string{"app.not_found", "app.ambiguous", "context.no_app", "usage.empty_argument", "context.no_team"},
		ExitCodes:  []int{0, 2, 3, 5, 8},

		Examples: []Example{
			{
				Title:        "show one application",
				Command:      "outplane app get checkout",
				Argv:         []string{"outplane", "app", "get", "checkout"},
				Placeholders: map[string]string{"checkout": "<APP_NAME>"},
				Risk:         RiskRead,
			},
			{
				Title:   "show the app this directory is linked to",
				Command: "outplane app get",
				Argv:    []string{"outplane", "app", "get"},
				Risk:    RiskRead,
			},
			{
				Title:        "read just the public address",
				Command:      "outplane app get checkout --json --fields url",
				Argv:         []string{"outplane", "app", "get", "checkout", "--json", "--fields", "url"},
				Placeholders: map[string]string{"checkout": "<APP_NAME>"},
				Risk:         RiskRead,
				OutputSample: map[string]any{
					"url": "https://checkout-3000-acme.outplane.app",
				},
			},
			{
				Title:        "list every port the app serves",
				Command:      "outplane app get checkout --json --fields endpoints",
				Argv:         []string{"outplane", "app", "get", "checkout", "--json", "--fields", "endpoints"},
				Placeholders: map[string]string{"checkout": "<APP_NAME>"},
				Risk:         RiskRead,
				OutputSample: map[string]any{
					"endpoints": []any{
						map[string]any{
							"port":          3000,
							"scheme":        "http",
							"public":        true,
							"url":           "https://checkout-3000-acme.outplane.app",
							"customDomains": []any{"checkout.example.com"},
						},
					},
				},
			},
		},

		AutomationNotes: []string{
			"Returns one object, not a list. `app list` returns {items, total, truncated}; " +
				"this returns the fields directly.",
			"Environment variables are never included, in any format, and no flag adds them.",
			"url is the first address the app answers on: the platform address of the first " +
				"public port, or a custom domain when no port is public. An app with several " +
				"ports has one url and several endpoints, so read endpoints when the port " +
				"matters.",
			"A name is resolved by listing the team's applications first, so this costs two " +
				"requests. Passing the id skips nothing: the id is resolved the same way.",
			"repository and imageRef are mutually exclusive: a Git-sourced app has the first, " +
				"a container-registry app has the second. Read source to know which to expect.",
			"status describes the last deployment, not a health check. An app can be ready and " +
				"still be serving errors.",
			"A paused app reports status \"paused\" and keeps its own instances count. " +
				"deploymentStatus says what it will return to.",
		},

		Related: []string{"app list", "deploy create", "logs", "link"},

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

		Related: []string{"app list", "app get", "deploy list"},

		DocsURL: "https://docs.outplane.com/cli/app",
	}
}
