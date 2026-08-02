package registry

// Deploy commands.
//
// Platform facts that shape this group, recorded here so nobody has to
// rediscover them from the API:
//
//   - Deployments come from a Git repository or a container registry. There is
//     no local-directory upload, and there cannot be one without new server
//     work.
//   - There is no rollback. The image registry keeps only the three most
//     recently pushed tags per repository, so images for older deployments are
//     physically gone. A rollback command would fail more often than it worked,
//     so we do not ship one and we say so in the help text.
//   - There is no cancel. AppDeploymentStatus has a Canceled value but no
//     endpoint sets it.
//   - Build logs are byte-offset polling, not a stream. The client resends the
//     offset it got back to fetch the next chunk.

func init() {
	Register(
		deployCreate(),
		deployList(),
		deployGet(),
	)
}

// deployList is the history, for one application or for the team.
//
// Two endpoints answer it and they return the same row. Only the team-wide one
// paginates, and it pages from one rather than from zero; there is no cursor
// and no total, so "the last twenty" is the first page of twenty and there is
// nothing honest to report about what lies beyond it.
func deployList() Command {
	return Command{
		Path:  []string{"deploy", "list"},
		Short: "list recent deployments",
		Long: "Lists deployments, newest first.\n\n" +
			"With no argument it covers the whole team, which is the view that answers " +
			"what has been happening. Name an application to see only its history.",

		Risk:         RiskRead,
		RequiresAuth: true,
		Session:      SessionAny,
		Idempotent:   true,

		APICalls: []string{
			"GET /api/AppDeployment/GetAppDeploymentsByTeamId/{page}/{pageSize}",
			"GET /api/AppDeployment/GetAppDeploymentsByAppId/{appId}",
		},

		Args: []Arg{
			{
				Name:     "app",
				Short:    "app name or id. Omit for the whole team",
				Required: false,
				Resolves: "app",
			},
		},

		Flags: []Flag{
			{
				Name:        "limit",
				Type:        "int",
				Default:     "20",
				Description: "how many of the most recent deployments to show",
			},
		},

		OutputFields: []Field{
			{Name: "deploymentId", Type: "int", Description: "the id `deploy get` and `deploy logs` take"},
			{Name: "app", Type: "string | null", Description: "which application it belongs to"},
			{Name: "appId", Type: "string | null"},
			{
				Name: "status",
				Type: "string",
				Enum: []string{"queued", "building", "deploying", "ready", "failed", "crashed", "canceled"},
			},
			{Name: "branch", Type: "string | null", Description: "for a Git-sourced app"},
			{Name: "imageRef", Type: "string | null", Description: "for a container-registry app"},
			{Name: "commitMessage", Type: "string | null"},
			{Name: "startedAt", Type: "string", Description: "RFC 3339, UTC"},
			{
				Name:        "duration",
				Type:        "string | null",
				Description: "the server's own figure for the build, already humanised. Empty for an app that builds nothing",
			},
		},

		ErrorCodes: []string{"app.not_found", "usage.bad_limit", "context.no_team"},
		ExitCodes:  []int{0, 2, 3, 5, 8},

		Examples: []Example{
			{
				Title:   "what has been deployed lately",
				Command: "outplane deploy list",
				Argv:    []string{"outplane", "deploy", "list"},
				Risk:    RiskRead,
			},
			{
				Title:        "one application's history",
				Command:      "outplane deploy list checkout --limit 5",
				Argv:         []string{"outplane", "deploy", "list", "checkout", "--limit", "5"},
				Placeholders: map[string]string{"checkout": "<APP_NAME>"},
				Risk:         RiskRead,
			},
			{
				Title:   "find the last failure",
				Command: "outplane deploy list --json --fields deploymentId,app,status",
				Argv:    []string{"outplane", "deploy", "list", "--json", "--fields", "deploymentId,app,status"},
				Risk:    RiskRead,
				OutputSample: map[string]any{
					"items": []any{
						map[string]any{"deploymentId": 412, "app": "checkout", "status": "ready"},
						map[string]any{"deploymentId": 411, "app": "worker", "status": "failed"},
					},
					"total":     2,
					"truncated": false,
				},
			},
		},

		AutomationNotes: []string{
			"Newest first, ordered by deploymentId. The server returns them oldest first; " +
				"the CLI reverses that, because the row anybody wants is the last one.",
			"truncated is true when more deployments exist than --limit asked for. There is " +
				"no cursor: raise the limit or narrow to one application.",
			"total means what it can. For one application the endpoint returns everything, so " +
				"it is the complete history. Across a team the endpoint paginates and offers " +
				"no count, so total is what was returned and truncated is what tells you more " +
				"exist.",
			"A canceled deployment was superseded by a newer one. Nothing went wrong.",
			"duration is empty for an application that deploys a ready-made image, because " +
				"nothing was built.",
		},

		Related: []string{"deploy get", "deploy logs", "deploy create", "app list"},
		DocsURL: "https://docs.outplane.com/cli/deploy",
	}
}

// deployGet is the only command whose application argument comes second. The
// id is what a reader has in hand, and the API's path needs both.
func deployGet() Command {
	return Command{
		Path:  []string{"deploy", "get"},
		Short: "show one deployment",
		Long: "Reports one deployment: how it ended, what it built and how long it took.\n\n" +
			"The application is needed as well as the id, because the platform addresses a " +
			"deployment by both. In a linked directory it is filled in for you.",

		Risk:         RiskRead,
		RequiresAuth: true,
		Session:      SessionAny,
		Idempotent:   true,

		APICalls: []string{"GET /api/AppDeployment/GetAppDeploymentById/{appId}/{deploymentId}"},

		Args: []Arg{
			{Name: "deployment", Short: "deployment id, a number", Required: true},
			{
				Name:     "app",
				Short:    "app name or id. Defaults to the linked app",
				Required: false,
				Resolves: "app",
			},
		},

		OutputFields: []Field{
			{Name: "deploymentId", Type: "int"},
			{Name: "app", Type: "string"},
			{Name: "appId", Type: "string"},
			{
				Name: "status",
				Type: "string",
				Enum: []string{"queued", "building", "deploying", "ready", "failed", "crashed", "canceled"},
			},
			{Name: "branch", Type: "string | null"},
			{Name: "imageRef", Type: "string | null"},
			{Name: "commitMessage", Type: "string | null"},
			{Name: "startedAt", Type: "string", Description: "RFC 3339, UTC"},
			{Name: "duration", Type: "string | null", Description: "the build only, humanised by the server"},
		},

		ErrorCodes: []string{
			"usage.bad_deployment_id",
			"usage.missing_argument",
			"app.not_found",
			"context.no_app",
		},
		ExitCodes: []int{0, 2, 3, 5, 8},

		Examples: []Example{
			{
				Title:        "check a deployment by id",
				Command:      "outplane deploy get 412 checkout",
				Argv:         []string{"outplane", "deploy", "get", "412", "checkout"},
				Placeholders: map[string]string{"checkout": "<APP_NAME>", "412": "<DEPLOYMENT_ID>"},
				Risk:         RiskRead,
			},
			{
				Title:        "in a linked directory, the id is enough",
				Command:      "outplane deploy get 412",
				Argv:         []string{"outplane", "deploy", "get", "412"},
				Placeholders: map[string]string{"412": "<DEPLOYMENT_ID>"},
				Risk:         RiskRead,
			},
		},

		AutomationNotes: []string{
			"The application comes second, after the id. It is the one command in the CLI " +
				"whose application is not the first argument, because the id is what a caller " +
				"is holding when it needs this.",
			"status is the deployment's own state and says nothing about whether the app is " +
				"healthy now. A newer deployment may have replaced it.",
			"Poll this rather than deploy create --wait when the wait has to survive the " +
				"process being interrupted.",
		},

		Related: []string{"deploy list", "deploy logs", "deploy create"},
		DocsURL: "https://docs.outplane.com/cli/deploy",
	}
}

// deployCreate is the most-typed command in the CLI. It is also the one an
// agent is most likely to get wrong, because without --wait or --follow the
// API returns as soon as the build is queued, and "queued" reads like success.
// That is what the AutomationNotes below exist to prevent.
func deployCreate() Command {
	return Command{
		Path: []string{"deploy", "create"},

		// Visible, not hidden. This is the command people will actually type,
		// and a shortcut nobody can discover is not a shortcut.
		Aliases: []string{"deploy"},

		Short: "build and deploy an application",
		Long: "Builds the application from its configured source and deploys the result.\n\n" +
			"The source is whatever the app was created with: a Git repository and branch, " +
			"or a container image. Use --image to deploy a specific image, which is only " +
			"valid for apps whose source is a container registry.",

		Risk:         RiskWrite,
		RequiresAuth: true,
		Session:      SessionAny,

		// Not idempotent: every invocation creates a new deployment record and
		// starts a new build, even if nothing changed.
		Idempotent: false,

		LongRunning:    true,
		Streams:        StreamNDJSON,
		SupportsDryRun: true,

		APICalls: []string{"POST /api/AppDeployment/CreateAppDeployment/{appId}"},

		Args: []Arg{
			{
				Name:     "app",
				Short:    "app name or id. Defaults to the linked app",
				Required: false,
				Resolves: "app",
				// Mirrors CreateAppRequestDtoValidator on the server. Kept
				// deliberately no stricter than the server's rule.
				Pattern: "^[a-zA-Z0-9]{5,45}$",
			},
		},

		Flags: []Flag{
			{
				Name: "image",
				Type: "string",
				Description: "container image reference to deploy. " +
					"Rejected locally for Git-sourced apps, which the server would reject anyway",
			},
			{
				Name:        "follow",
				Short:       "f",
				Type:        "bool",
				Default:     "false",
				Description: "stream build logs until the deployment reaches a final state",
			},
			{
				Name:        "wait",
				Type:        "bool",
				Default:     "false",
				Description: "block until the deployment finishes, without streaming logs",
			},
			{
				Name:        "timeout",
				Type:        "duration",
				Default:     "20m",
				Description: "how long --wait and --follow will wait before giving up",
			},
		},

		OutputFields: []Field{
			{Name: "deploymentId", Type: "int", Description: "identifier for deploy get and deploy logs"},
			{Name: "app", Type: "object", Description: "{id, name}"},
			{
				Name: "status",
				Type: "string",
				Description: "current state. An unrecognised value is reported as-is and never " +
					"interpreted, so a new server state cannot turn into a false result",
				Enum: []string{"queued", "building", "deploying", "ready", "failed", "crashed", "canceled"},
			},
			{Name: "branch", Type: "string | null", Description: "for a Git-sourced app"},
			{Name: "imageRef", Type: "string | null", Description: "for a container-registry app"},
			{Name: "commitMessage", Type: "string | null"},
			{Name: "startedAt", Type: "string", Description: "RFC 3339, UTC"},
			{
				Name: "duration",
				Type: "string | null",
				Description: "the server's own figure for the build, already humanised. It " +
					"excludes the release that follows, so a --wait call takes slightly longer " +
					"than this reports",
			},
			{Name: "changed", Type: "bool", Description: "true whenever a build was started"},
		},

		ErrorCodes: []string{
			"deploy.image_on_git_app",
			"deploy.failed",
			"deploy.timeout",
			"app.not_found",
			"app.ambiguous",
			"context.no_app",
			"usage.empty_argument",
			"quota.limit_reached",
		},
		ExitCodes: []int{0, 2, 3, 5, 7, 8, 124},

		Examples: []Example{
			{
				Title:   "deploy the linked app and wait for it to finish",
				Command: "outplane deploy create --wait",
				Argv:    []string{"outplane", "deploy", "create", "--wait"},
				Risk:    RiskWrite,
			},
			{
				Title:        "see what would be sent, without deploying",
				Command:      "outplane deploy create checkout --dry-run --json",
				Argv:         []string{"outplane", "deploy", "create", "checkout", "--dry-run", "--json"},
				Placeholders: map[string]string{"checkout": "<APP_NAME>"},
				// Safe to run as-is: --dry-run makes no request.
				Risk: RiskRead,
			},
			{
				Title:   "deploy a specific image and stream the build log as NDJSON",
				Command: "outplane deploy create checkout --image ghcr.io/acme/api:v1.4.0 --follow -o ndjson",
				Argv: []string{"outplane", "deploy", "create", "checkout",
					"--image", "ghcr.io/acme/api:v1.4.0", "--follow", "-o", "ndjson"},
				Placeholders: map[string]string{
					"checkout":                "<APP_NAME>",
					"ghcr.io/acme/api:v1.4.0": "<IMAGE_REF>",
				},
				Risk: RiskWrite,
				OutputSample: map[string]any{
					"deploymentId": 4821,
					"status":       "ready",
					"app":          map[string]any{"id": "…", "name": "checkout"},
					"imageRef":     "<IMAGE_REF>",
				},
			},
		},

		AutomationNotes: []string{
			"Without --wait or --follow this command returns as soon as the build is queued. " +
				"A queued build is not a finished deploy.",
			"To check the outcome, run: outplane deploy get <deploymentId> --json",
			"When a deploy fails, read the build output first: outplane deploy logs <deploymentId>",
			"There is no rollback command. The image registry keeps only the last three tags " +
				"per repository, so older deployment images no longer exist.",
			"There is no way to cancel a running deployment.",
		},

		Related: []string{
			"deploy get",
			"deploy logs",
			"deploy list",
			"logs",
		},

		DocsURL: "https://docs.outplane.com/cli/deploy",
	}
}
