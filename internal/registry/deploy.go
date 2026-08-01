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
	)
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
			"app restart",
			"logs",
		},

		DocsURL: "https://docs.outplane.com/cli/deploy",
	}
}
