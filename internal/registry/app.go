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
		appCreate(),
		appScale(),
		appPause(),
		appResume(),
		appInstances(),
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
				Description: "every port: {portId, port, scheme, public, url, customDomains}. " +
					"scheme is http, h2c or tcp; url is null on a private port; customDomains " +
					"are full addresses, not host names; portId is what a custom domain binds to",
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

// appCreate is the command with the most ways to be wrong, which is why it
// checks every one of them before sending anything.
//
// Two platform facts shape it. Creating an application also deploys it: the
// server starts the first deployment itself, so there is no dormant state and
// no separate deploy step. And a name is permanent, because it becomes part of
// every address the application answers on; the display name is the editable
// one.
func appCreate() Command {
	return Command{
		Path:  []string{"app", "create"},
		Short: "create an application and deploy it",
		Long: "Creates an application from a repository or from a container image.\n\n" +
			"Creating also deploys: the platform starts the first deployment as part of " +
			"creation, so the command returns a deployment id and something is already " +
			"building when it does.\n\n" +
			"The name cannot be changed afterwards. It appears in every address the " +
			"application answers on, which is why it takes only letters and numbers.",

		Risk:         RiskWrite,
		RequiresAuth: true,

		// A repository the caller can reach is resolved through their GitHub
		// installation, which belongs to a person rather than to a team. An
		// image needs no such thing, but the command is declared once.
		Session: SessionUser,

		// Not idempotent: running it twice creates one application and then
		// fails, because the name is taken.
		Idempotent:     false,
		SupportsDryRun: true,

		APICalls: []string{"POST /api/App/CreateApp"},

		Args: []Arg{
			{
				Name:     "name",
				Short:    "letters and numbers, 5 to 45 characters. Permanent",
				Required: true,
				Pattern:  "^[a-zA-Z0-9]{5,45}$",
			},
		},

		Flags: []Flag{
			{
				Name:        "repo",
				Type:        "string",
				Description: "repository as owner/name. Requires --branch. Not valid with --image",
			},
			{
				Name:        "branch",
				Type:        "string",
				Description: "branch to deploy, usually main. Required with --repo",
			},
			{
				Name: "public-repo",
				Type: "bool", Default: "false",
				Description: "the repository is public, so no installation is needed to read it",
			},
			{
				Name:        "image",
				Type:        "string",
				Description: "container image to run, such as nginx:latest. Not valid with --repo",
			},
			{
				Name: "build", Type: "string", Default: "dockerfile",
				Enum:        []string{"dockerfile", "buildpack"},
				Description: "how the repository becomes an image. Ignored for --image",
			},
			{
				Name:        "dir",
				Type:        "string",
				Description: "sub-directory to build, when the repository holds more than one app",
			},
			{
				Name:        "start-command",
				Type:        "string",
				Description: "overrides the image's own command",
			},
			{
				Name: "port", Type: "strings",
				Description: "PORT[:SCHEME[:public|private]], repeatable. " +
					"Defaults to http and private, e.g. 3000 or 3000:http:public",
			},
			{
				Name: "env", Type: "strings",
				Description: "KEY=VALUE, repeatable. Values are never printed back",
			},
			{
				Name: "size", Type: "string", Default: "op-20",
				Enum:        []string{"op-20", "op-22", "op-34", "op-46", "op-58", "op-70", "op-82", "op-94"},
				Description: "instance type. Larger ones may need a paid plan",
			},
			{
				Name: "instances", Type: "int", Default: "1",
				Description: "replica count, 1 to 5",
			},
			{
				Name: "volume", Type: "strings",
				Description: "VOLUME_ID:/path, repeatable. The volume must already exist and " +
					"be detached",
			},
			{
				Name: "env-group", Type: "strings",
				Description: "id of a shared variable group to assign, repeatable",
			},
		},

		OutputFields: []Field{
			{Name: "name", Type: "string"},
			{Name: "appId", Type: "string | null", Description: "null for a dry run"},
			{
				Name:        "deploymentId",
				Type:        "int | null",
				Description: "the deployment creation started. Queued, not finished",
			},
			{Name: "source", Type: "string", Enum: []string{"github", "container-registry"}},
			{Name: "repository", Type: "string | null"},
			{Name: "branch", Type: "string | null"},
			{Name: "imageRef", Type: "string | null"},
			{
				Name:        "buildMethod",
				Type:        "string",
				Description: "dockerfile, buildpack, or prebuilt-image for an image app whatever --build said",
			},
			{Name: "size", Type: "string"},
			{Name: "instances", Type: "int"},
			{Name: "ports", Type: "array", Description: "{port, scheme, public}"},
			{Name: "envCount", Type: "int", Description: "how many variables were set. Values are never returned"},
			{Name: "changed", Type: "bool", Description: "false for a dry run"},
		},

		ErrorCodes: []string{
			"app.name_invalid",
			"app.name_reserved",
			"app.source_required",
			"app.source_conflict",
			"app.repository_invalid",
			"app.branch_required",
			"app.size_invalid",
			"app.instances_invalid",
			"app.port_invalid",
			"app.port_duplicate",
			"app.repository_unavailable",
			"app.mount_invalid",
			"app.mount_duplicate",
			"usage.bad_mount",
			"usage.bad_port",
			"usage.bad_assignment",
			"quota.limit_reached",
		},
		ExitCodes: []int{0, 2, 3, 7, 8},

		Examples: []Example{
			{
				Title:        "from a repository",
				Command:      "outplane app create checkout --repo acme/checkout --branch main --port 3000:http:public",
				Argv:         []string{"outplane", "app", "create", "checkout", "--repo", "acme/checkout", "--branch", "main", "--port", "3000:http:public"},
				Placeholders: map[string]string{"checkout": "<APP_NAME>", "acme/checkout": "<OWNER>/<REPO>"},
				Risk:         RiskWrite,
			},
			{
				Title:        "from a container image",
				Command:      "outplane app create proxy01 --image nginx:latest --port 80:http:public",
				Argv:         []string{"outplane", "app", "create", "proxy01", "--image", "nginx:latest", "--port", "80:http:public"},
				Placeholders: map[string]string{"proxy01": "<APP_NAME>"},
				Risk:         RiskWrite,
			},
			{
				Title:        "with variables and a larger instance",
				Command:      "outplane app create worker01 --image redis:7 --port 6379:tcp --env MODE=queue --size op-34",
				Argv:         []string{"outplane", "app", "create", "worker01", "--image", "redis:7", "--port", "6379:tcp", "--env", "MODE=queue", "--size", "op-34"},
				Placeholders: map[string]string{"worker01": "<APP_NAME>"},
				Risk:         RiskWrite,
			},
			{
				Title:        "check the request without sending it",
				Command:      "outplane app create checkout --repo acme/checkout --branch main --dry-run",
				Argv:         []string{"outplane", "app", "create", "checkout", "--repo", "acme/checkout", "--branch", "main", "--dry-run"},
				Placeholders: map[string]string{"checkout": "<APP_NAME>", "acme/checkout": "<OWNER>/<REPO>"},
				Risk:         RiskRead,
			},
		},

		AutomationNotes: []string{
			"Creating deploys. deploymentId is a queued deployment, not a finished one; follow " +
				"it with `outplane deploy logs <id>` or check `outplane deploy get <id> <name>`.",
			"The name is permanent and appears in the public address. Letters and numbers only, " +
				"five characters or more, and a set of infrastructure-sounding names is refused.",
			"--repo needs --branch; --image forbids one. Passing both sources is an error rather " +
				"than a preference.",
			"A private repository is read through the GitHub installation of the user whose " +
				"token this is. --public-repo skips that, and is the only way to create from a " +
				"repository the platform has no installation for.",
			"app.repository_unavailable covers three situations the server does not " +
				"distinguish: the repository does not exist, the GitHub App is not installed " +
				"at all, or it is installed without access to this one. The error carries the " +
				"page that fixes the last two.",
			"A port is private unless it says public. A private port is reachable by other " +
				"applications and by a custom domain, and has no platform address.",
			"Everything the server would refuse is checked first, so an error names the field. " +
				"The exceptions are the plan limit and the name already being taken, which only " +
				"the server knows.",
			"Variable values are never echoed back. envCount reports how many were set.",
			"--volume and --env-group take ids of things that already exist. Attaching is " +
				"best effort on the server: one it cannot attach is skipped and the creation " +
				"succeeds anyway, so confirm with `outplane app get` when it matters.",
		},

		Related: []string{"app list", "app get", "deploy create", "env set", "app delete"},
		DocsURL: "https://docs.outplane.com/cli/app",
	}
}

// The running-state commands: how many, how large, and whether at all.
//
// They share a property nothing else on this platform has. An environment
// variable waits for the next deployment; these refresh the workload
// immediately, so the effect is visible in seconds and needs no deploy.

func appScale() Command {
	return Command{
		Path:  []string{"app", "scale"},
		Short: "change how many instances an application runs, and how large they are",
		Long: "Sets the replica count, the instance size, or both.\n\n" +
			"Whichever one you do not pass keeps its current value. That is not a " +
			"convenience: the endpoint replaces both together and defaults the count to " +
			"one, so a command that sent only a size would quietly scale the application " +
			"down.\n\n" +
			"The change is applied to the running application without a deployment.",

		Risk:         RiskWrite,
		RequiresAuth: true,
		Session:      SessionAny,
		Idempotent:   true,

		SupportsDryRun: true,

		APICalls: []string{
			"GET /api/App/GetAppsByTeamId",
			"PUT /api/AppSetting/UpdateScaleSettings/{appId}",
		},

		Args: []Arg{
			{
				Name:     "app",
				Short:    "app name or id. Defaults to the linked app",
				Required: false,
				Resolves: "app",
			},
		},

		Flags: []Flag{
			{Name: "instances", Type: "int", Description: "replica count, 1 to 5"},
			{
				Name: "size", Type: "string",
				Enum:        []string{"op-20", "op-22", "op-34", "op-46", "op-58", "op-70", "op-82", "op-94"},
				Description: "instance type. The larger ones need a paid plan",
			},
		},

		OutputFields: []Field{
			{Name: "app", Type: "string"},
			{Name: "appId", Type: "string"},
			{Name: "instances", Type: "int", Description: "what it will run"},
			{Name: "size", Type: "string"},
			{Name: "previousInstances", Type: "int"},
			{Name: "previousSize", Type: "string"},
			{Name: "changed", Type: "bool", Description: "false when it already matched, and for a dry run"},
		},

		ErrorCodes: []string{
			"app.instances_invalid",
			"app.size_invalid",
			"usage.missing_argument",
			"quota.limit_reached",
			"app.not_found",
			"context.no_app",
		},
		ExitCodes: []int{0, 2, 3, 5, 7, 8},

		Examples: []Example{
			{
				Title:        "run three copies",
				Command:      "outplane app scale checkout --instances 3",
				Argv:         []string{"outplane", "app", "scale", "checkout", "--instances", "3"},
				Placeholders: map[string]string{"checkout": "<APP_NAME>"},
				Risk:         RiskWrite,
			},
			{
				Title:        "give it more memory, keeping the count",
				Command:      "outplane app scale checkout --size op-34",
				Argv:         []string{"outplane", "app", "scale", "checkout", "--size", "op-34"},
				Placeholders: map[string]string{"checkout": "<APP_NAME>"},
				Risk:         RiskWrite,
			},
		},

		AutomationNotes: []string{
			"The flag you omit is filled in from the current setting, which is read first. " +
				"Two callers scaling the same application at the same time can therefore " +
				"overwrite each other; the API offers no partial update.",
			"Applied without a deployment. The instances change within seconds, and " +
				"`outplane app instances` shows it happening.",
			"changed is false when the application already matched, and the command still " +
				"exits 0: it is a statement of the desired state, not an action.",
			"A size above the plan's allowance is exit 7, not a validation error. The list of " +
				"allowed sizes is the team's, not the platform's.",
		},

		Related: []string{"app instances", "app pause", "app resume", "app get"},
		DocsURL: "https://docs.outplane.com/cli/app",
	}
}

func appPause() Command {
	return pauseCommand("pause", "stop an application without deleting it",
		"Stops every instance.\n\n"+
			"The configured scale is untouched, so resuming returns to the same number of "+
			"instances rather than to one. Nothing else is removed: the application, its "+
			"variables, its domains and its volumes all stay.\n\n"+
			"A paused application costs nothing to run and keeps its address.")
}

func appResume() Command {
	return pauseCommand("resume", "start a paused application",
		"Starts the application again, at the scale it was configured for.\n\n"+
			"It runs the image from its last deployment: resuming is not a rebuild.")
}

// pauseCommand is the shared declaration. The two commands differ in one word,
// and writing that word twice is how a pair like this drifts apart.
func pauseCommand(verb, short, long string) Command {
	return Command{
		Path:  []string{"app", verb},
		Short: short,
		Long:  long,

		Risk:         RiskWrite,
		RequiresAuth: true,
		Session:      SessionAny,
		Idempotent:   true,

		SupportsDryRun: true,

		APICalls: []string{
			"GET /api/App/GetAppsByTeamId",
			"PUT /api/AppSetting/UpdatePauseState/{appId}",
		},

		Args: []Arg{
			{
				Name:     "app",
				Short:    "app name or id. Defaults to the linked app",
				Required: false,
				Resolves: "app",
			},
		},

		OutputFields: []Field{
			{Name: "app", Type: "string"},
			{Name: "appId", Type: "string"},
			{Name: "paused", Type: "bool", Description: "the state the application is now in"},
			{Name: "instances", Type: "int", Description: "the configured count, which pausing does not change"},
			{Name: "changed", Type: "bool", Description: "false when it was already in that state"},
		},

		ErrorCodes: []string{"app.not_found", "context.no_app"},
		ExitCodes:  []int{0, 2, 3, 5, 8},

		Examples: []Example{
			{
				Title:        verb + " an application",
				Command:      "outplane app " + verb + " checkout",
				Argv:         []string{"outplane", "app", verb, "checkout"},
				Placeholders: map[string]string{"checkout": "<APP_NAME>"},
				Risk:         RiskWrite,
			},
		},

		AutomationNotes: []string{
			"Idempotent. Pausing something already paused changes nothing, reports " +
				"changed false, and exits 0.",
			"Applied immediately, without a deployment.",
			"The configured replica count is kept, so resuming returns to the scale that was " +
				"set rather than to a single instance.",
			"A paused application still reports its last deployment's state in " +
				"deploymentStatus. status is what says paused.",
		},

		Related: []string{"app pause", "app resume", "app scale", "app list"},
		DocsURL: "https://docs.outplane.com/cli/app",
	}
}

func appInstances() Command {
	return Command{
		Path:  []string{"app", "instances"},
		Short: "list the instances an application is actually running",
		Long: "Lists the running copies of an application, with what each one is doing.\n\n" +
			"This is read from the cluster rather than from the record, so it disagrees " +
			"with the configured count exactly when that is worth knowing: during a " +
			"rollout, while an instance restarts, or when one cannot start at all.",

		Risk:         RiskRead,
		RequiresAuth: true,
		Session:      SessionAny,
		Idempotent:   true,

		APICalls: []string{
			"GET /api/App/GetAppsByTeamId",
			"GET /api/App/GetInstances/{appId}",
		},

		Args: []Arg{
			{
				Name:     "app",
				Short:    "app name or id. Defaults to the linked app",
				Required: false,
				Resolves: "app",
			},
		},

		OutputFields: []Field{
			{Name: "name", Type: "string", Description: "the instance's own name, which changes on every restart"},
			{
				Name: "phase",
				Type: "string",
				Description: "the runtime's word for what it is doing, passed through unchanged: " +
					"Pending, Running, Succeeded, Failed or Unknown",
			},
			{
				Name: "ready",
				Type: "bool",
				Description: "whether it is taking traffic. Running and not ready is the " +
					"interesting state during a rollout",
			},
			{Name: "container", Type: "string"},
			{Name: "startedAt", Type: "string | null", Description: "RFC 3339, UTC"},
		},

		ErrorCodes: []string{"app.not_found", "context.no_app"},
		ExitCodes:  []int{0, 2, 3, 5, 8},

		Examples: []Example{
			{
				Title:        "what is running right now",
				Command:      "outplane app instances checkout",
				Argv:         []string{"outplane", "app", "instances", "checkout"},
				Placeholders: map[string]string{"checkout": "<APP_NAME>"},
				Risk:         RiskRead,
			},
			{
				Title:   "wait for a rollout in a script",
				Command: "outplane app instances --json --fields name,ready",
				Argv:    []string{"outplane", "app", "instances", "--json", "--fields", "name,ready"},
				Risk:    RiskRead,
				OutputSample: map[string]any{
					"items": []any{
						map[string]any{"name": "checkout-7d9f-abcde", "ready": true},
						map[string]any{"name": "checkout-7d9f-fghij", "ready": false},
					},
					"total":     2,
					"truncated": false,
				},
			},
		},

		AutomationNotes: []string{
			"total is what is running, which is not the configured count. `app list` reports " +
				"the configuration; a difference between them is a rollout, a restart or a " +
				"failure to schedule.",
			"phase comes from the container runtime and is passed through unchanged, so a " +
				"value this release has never seen still arrives intact.",
			"An instance name changes every time it restarts. Do not store one.",
			"A paused application runs nothing, so this is empty and that is not an error.",
		},

		Related: []string{"app scale", "app pause", "logs", "metrics"},
		DocsURL: "https://docs.outplane.com/cli/app",
	}
}

// appDelete is the reference example for a destructive command. Everything
// about the confirmation protocol is visible in this declaration.
func appDelete() Command {
	return Command{
		Path:  []string{"app", "delete"},
		Short: "permanently delete an application",
		Long: "Deletes an application and its deployment history.\n\n" +
			"There is no undelete and no retention window.\n\n" +
			"The platform refuses while certain things still exist, a custom domain and an " +
			"attached volume among them, and its refusal says which. Those are the server's " +
			"rules and it is the only thing that knows the whole list, so this command does " +
			"not try to predict them.",

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
			{Name: "deleted", Type: "bool", Description: "false for a dry run"},
			{Name: "app", Type: "string", Description: "the application's immutable name"},
			{Name: "appId", Type: "string"},
		},

		ErrorCodes: []string{
			"confirmation.required",
			"app.confirm_name_mismatch",
			"app.delete_blocked",
			"app.not_found",
			"usage.empty_argument",
		},
		ExitCodes: []int{0, 2, 3, 4, 5, 8},

		Examples: []Example{
			{
				Title:        "confirm which application the name resolves to, before confirming anything",
				Command:      "outplane app delete checkout --dry-run --json",
				Argv:         []string{"outplane", "app", "delete", "checkout", "--dry-run", "--json"},
				Placeholders: map[string]string{"checkout": "<APP_NAME>"},
				Risk:         RiskRead,
				OutputSample: map[string]any{"deleted": false, "app": "checkout"},
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
				"command to replay in the error's confirm_command field.",
			"Exit 4 is not a failure. It means the confirmation belongs to somebody else.",
			"Under a detected agent harness it exits 4 even when --yes and --confirm-name are " +
				"supplied. A flag is not a safety boundary, because an agent that read this " +
				"note can emit any flag in it; the harness's own approval step is the gate.",
			"Some things stop a deletion instead of being removed with it: a custom domain " +
				"and an attached volume are two. The server owns that list and its refusal " +
				"names the rule, so --dry-run reports what would be deleted rather than " +
				"predicting whether it can be.",
			"--confirm-name is matched against the immutable name, not the display name.",
			"Deletion is irreversible. There is no undelete and no retention window.",
		},

		Related: []string{"app list", "app get", "deploy list"},

		DocsURL: "https://docs.outplane.com/cli/app",
	}
}
