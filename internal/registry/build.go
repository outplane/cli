package registry

// How an application is built, and what it is called.
//
// Both commands write through an endpoint that stores every field it is given
// rather than the fields that changed, so both read first and send the whole
// picture, exactly as the port commands do. What that means for a caller is in
// the automation notes of each: a flag left out keeps its value, and an empty
// value is a value.
//
// The platform's asymmetries are the rest of it, and every one of them is a
// place somebody would otherwise be told nothing:
//
//   - An application that runs a prebuilt image has no build. The server
//     ignores the build method, the directory and the filters for one, without
//     saying so, so the CLI refuses them instead.
//   - The directory cannot be emptied. The server keeps the old one, which
//     looks like the change was ignored, because it was.
//   - The start command and both filters can be emptied, and emptying them is
//     how they are removed.
//   - A rename changes the label. Nothing changes the name in the address.

func init() {
	Register(
		buildGet(),
		buildSet(),
		appRename(),
	)
}

func buildAppFlag() Flag {
	return Flag{
		Name: "app",
		Type: "string",
		Description: "application name or id. Defaults to the linked app. " +
			"A flag rather than an argument, to match the rest of this group",
	}
}

func buildGet() Command {
	return Command{
		Path:  []string{"build", "get"},
		Short: "show how an application is built",
		Long: "Shows the settings an application is built and started with.\n\n" +
			"An application built from a repository reports its build method, the directory " +
			"the build runs in, the command that starts it and the two filters that decide " +
			"whether a push builds at all.\n\n" +
			"An application that runs a prebuilt image reports the image and the start " +
			"command, because nothing else applies: the image is built somewhere else.",

		Risk:         RiskRead,
		RequiresAuth: true,
		Session:      SessionAny,
		Idempotent:   true,

		APICalls: []string{
			"GET /api/App/GetAppsByTeamId",
			"GET /api/App/GetAppById/{appId}",
		},

		Flags: []Flag{buildAppFlag()},

		OutputFields: []Field{
			{Name: "app", Type: "string"},
			{Name: "appId", Type: "string"},
			{
				Name: "source", Type: "string", Enum: []string{"repository", "container-registry"},
				Description: "where the image comes from, which decides what can be set at all",
			},
			{
				Name: "buildMethod", Type: "string",
				Enum:        []string{"dockerfile", "buildpack", "prebuilt-image"},
				Description: "prebuilt-image means nothing is built here",
			},
			{Name: "directory", Type: "string | null", Description: "where the build runs, / being the repository root"},
			{Name: "startCommand", Type: "string | null", Description: "overrides the image's own command"},
			{
				Name: "includePaths", Type: "string | null",
				Description: "glob patterns, one per line. A push touching none of them does not build",
			},
			{
				Name: "ignorePaths", Type: "string | null",
				Description: "glob patterns, one per line. A push touching only these does not build",
			},
			{Name: "image", Type: "string | null", Description: "the reference a registry application runs"},
			{Name: "changed", Type: "bool", Description: "always false here; `build set` reports the change"},
			{Name: "deploymentId", Type: "int | null", Description: "always null here"},
		},

		ErrorCodes: []string{"context.no_app", "app.not_found"},
		ExitCodes:  []int{0, 2, 3, 5, 8},

		Examples: []Example{
			{
				Title:   "show the linked application's build settings",
				Command: "outplane build get",
				Argv:    []string{"outplane", "build", "get"},
				Risk:    RiskRead,
			},
			{
				Title:        "show another application's",
				Command:      "outplane build get --app checkout",
				Argv:         []string{"outplane", "build", "get", "--app", "checkout"},
				Placeholders: map[string]string{"checkout": "<APP_NAME>"},
				Risk:         RiskRead,
			},
			{
				Title:   "read one setting in a script",
				Command: "outplane build get --json --fields buildMethod,directory",
				Argv:    []string{"outplane", "build", "get", "--json", "--fields", "buildMethod,directory"},
				Risk:    RiskRead,
				OutputSample: map[string]any{
					"buildMethod": "dockerfile",
					"directory":   "/api",
				},
			},
		},

		AutomationNotes: []string{
			"source decides which fields mean anything. A container-registry application " +
				"has an image and a start command; its buildMethod is prebuilt-image and " +
				"its directory and filters are null and cannot be set.",
			"Both filters are one string with a pattern per line, which is how the platform " +
				"stores them. `build set` takes them as a repeatable flag instead.",
			"An empty filter means every push builds. It is null rather than an empty " +
				"string.",
		},

		Related: []string{"build set", "app get", "deploy create", "app rename"},
		DocsURL: "https://docs.outplane.com/cli/build",
	}
}

func buildSet() Command {
	return Command{
		Path:  []string{"build", "set"},
		Short: "change how an application is built",
		Long: "Changes one or more build settings.\n\n" +
			"A flag left out keeps its current value. An empty value is a value: " +
			"--start-command \"\" removes the start command, and --include-paths \"\" removes " +
			"the filter. The directory is the exception and cannot be emptied, because the " +
			"platform stores no empty directory; --dir / builds from the root.\n\n" +
			"An application that runs a prebuilt image takes --image and --start-command " +
			"only. The others are refused rather than sent, because the server would ignore " +
			"them without saying so.\n\n" +
			"These settings are read when an image is built, so the running application " +
			"keeps the ones it was built with until it is built again.",

		Risk:         RiskWrite,
		RequiresAuth: true,
		Session:      SessionAny,
		Idempotent:   true,

		SupportsDryRun: true,

		APICalls: []string{
			"GET /api/App/GetAppById/{appId}",
			"PUT /api/AppSetting/UpdateBuildDeploySettings/{appId}",
			"POST /api/AppDeployment/CreateAppDeployment/{appId}",
		},

		Flags: []Flag{
			buildAppFlag(),
			{
				Name: "method", Type: "string",
				Enum:        []string{"dockerfile", "buildpack"},
				Description: "how the repository becomes an image",
			},
			{
				Name: "dir", Type: "string",
				Description: "the directory the build runs in. / is the repository root, and it cannot be emptied",
			},
			{
				Name: "start-command", Type: "string",
				Description: "overrides the image's own command. An empty value removes it",
			},
			{
				Name: "include-paths", Type: "strings", Repeatable: true,
				Description: "build only when a push touches one of these globs, repeatable. " +
					"An empty value removes the filter",
			},
			{
				Name: "ignore-paths", Type: "strings", Repeatable: true,
				Description: "skip the build when a push touches only these globs, repeatable. " +
					"An empty value removes the filter",
			},
			{
				Name: "image", Type: "string",
				Description: "the image to run. Only for an application that runs a prebuilt one",
			},
			{
				Name: "deploy", Type: "bool", Default: "false",
				Description: "build and deploy afterwards, so the change takes effect now",
			},
		},

		OutputFields: []Field{
			{Name: "app", Type: "string"},
			{Name: "appId", Type: "string"},
			{Name: "source", Type: "string", Enum: []string{"repository", "container-registry"}},
			{
				Name: "buildMethod", Type: "string",
				Enum:        []string{"dockerfile", "buildpack", "prebuilt-image"},
				Description: "the value after the change, not the part that changed",
			},
			{Name: "directory", Type: "string | null"},
			{Name: "startCommand", Type: "string | null"},
			{Name: "includePaths", Type: "string | null"},
			{Name: "ignorePaths", Type: "string | null"},
			{Name: "image", Type: "string | null"},
			{Name: "changed", Type: "bool", Description: "false for a dry run"},
			{
				Name: "deploymentId", Type: "int | null",
				Description: "the deployment --deploy started, or null when it was not given",
			},
		},

		ErrorCodes: []string{
			"build.method_invalid",
			"build.directory_required",
			"build.image_required",
			"build.filter_too_long",
			"build.not_built_here",
			"build.not_an_image",
			"usage.no_change",
			"context.no_app",
			"app.not_found",
		},
		ExitCodes: []int{0, 2, 3, 5, 8},

		Examples: []Example{
			{
				Title:   "build with buildpacks instead of a Dockerfile",
				Command: "outplane build set --method buildpack",
				Argv:    []string{"outplane", "build", "set", "--method", "buildpack"},
				Risk:    RiskWrite,
			},
			{
				Title:   "build only when the API directory changes",
				Command: "outplane build set --include-paths 'api/**' --include-paths package.json",
				Argv: []string{"outplane", "build", "set", "--include-paths", "api/**",
					"--include-paths", "package.json"},
				Risk: RiskWrite,
			},
			{
				Title:   "remove the start command",
				Command: "outplane build set --start-command \"\"",
				Argv:    []string{"outplane", "build", "set", "--start-command", ""},
				Risk:    RiskWrite,
			},
			{
				Title:   "see what would be written, without writing it",
				Command: "outplane build set --dir /api --dry-run --json",
				Argv:    []string{"outplane", "build", "set", "--dir", "/api", "--dry-run", "--json"},
				Risk:    RiskRead,
				OutputSample: map[string]any{
					"source":       "repository",
					"buildMethod":  "dockerfile",
					"directory":    "/api",
					"startCommand": "node server.js",
					"changed":      false,
				},
			},
			{
				Title:        "move a registry application to a new image tag, and deploy it",
				Command:      "outplane build set --image ghcr.io/acme/api:1.4.2 --app checkout --deploy",
				Argv:         []string{"outplane", "build", "set", "--image", "ghcr.io/acme/api:1.4.2", "--app", "checkout", "--deploy"},
				Placeholders: map[string]string{"checkout": "<APP_NAME>", "ghcr.io/acme/api:1.4.2": "<IMAGE_REF>"},
				Risk:         RiskWrite,
			},
		},

		AutomationNotes: []string{
			"A flag that is not given keeps its current value. The endpoint writes every " +
				"field it is given, so this command reads the current settings and sends " +
				"them back with the change applied; two callers changing different settings " +
				"at the same time can therefore lose one of the changes.",
			"An empty value clears, for the start command and both filters. --dir cannot be " +
				"emptied and returns build.directory_required; --dir / is how to build from " +
				"the repository root.",
			"A container-registry application refuses --method, --dir and the filters with " +
				"build.not_built_here, and a repository application refuses --image with " +
				"build.not_an_image. Neither is sent, because the server would ignore them " +
				"in silence.",
			"The filters are a repeatable flag here and one newline-separated string in the " +
				"result, which is how the platform stores them.",
			"Nothing rebuilds on its own. Without --deploy the running application keeps " +
				"the image it was built from, which is a longer wait than an environment " +
				"variable: the change takes effect only after a build, not after a restart.",
			"Calling this twice with the same flags leaves the same state, so a retry after " +
				"a timeout is safe.",
		},

		Related: []string{"build get", "deploy create", "app get", "env set"},
		DocsURL: "https://docs.outplane.com/cli/build",
	}
}

func appRename() Command {
	return Command{
		Path:  []string{"app", "rename"},
		Short: "change the label an application is shown under",
		Long: "Changes the display name, which is the label the console and `app list` show.\n\n" +
			"It does not change the address. The name in a URL is fixed when the " +
			"application is created and no endpoint changes it, which is the opposite of " +
			"what a rename usually implies, so the command says it every time.\n\n" +
			"Letters, numbers, spaces and hyphens, up to 45 characters. Nothing else is " +
			"accepted, which is the server's rule rather than this one.",

		Risk:         RiskWrite,
		RequiresAuth: true,
		Session:      SessionAny,
		Idempotent:   true,

		SupportsDryRun: true,

		APICalls: []string{
			"GET /api/App/GetAppsByTeamId",
			"PUT /api/AppSetting/UpdateDisplayName/{appId}",
		},

		Args: []Arg{
			{Name: "display-name", Short: "the label to show it under", Required: true},
		},

		Flags: []Flag{buildAppFlag()},

		OutputFields: []Field{
			{Name: "app", Type: "string", Description: "the immutable name, which this does not change"},
			{Name: "appId", Type: "string"},
			{Name: "displayName", Type: "string", Description: "the new label"},
			{Name: "changed", Type: "bool", Description: "false for a dry run"},
		},

		ErrorCodes: []string{
			"app.display_name_required",
			"app.display_name_invalid",
			"usage.missing_argument",
			"context.no_app",
			"app.not_found",
		},
		ExitCodes: []int{0, 2, 3, 5, 8},

		Examples: []Example{
			{
				Title:   "rename the linked application",
				Command: "outplane app rename \"Checkout API\"",
				Argv:    []string{"outplane", "app", "rename", "Checkout API"},
				Risk:    RiskWrite,
			},
			{
				Title:        "rename another one",
				Command:      "outplane app rename \"Checkout API\" --app checkout",
				Argv:         []string{"outplane", "app", "rename", "Checkout API", "--app", "checkout"},
				Placeholders: map[string]string{"checkout": "<APP_NAME>"},
				Risk:         RiskWrite,
			},
			{
				Title:   "check the name would be accepted, without changing anything",
				Command: "outplane app rename \"Checkout API\" --dry-run --json",
				Argv:    []string{"outplane", "app", "rename", "Checkout API", "--dry-run", "--json"},
				Risk:    RiskRead,
				OutputSample: map[string]any{
					"app":         "checkout",
					"displayName": "Checkout API",
					"changed":     false,
				},
			},
		},

		AutomationNotes: []string{
			"The address does not change. app in the result is the name that appears in " +
				"URLs and it is fixed at creation; displayName is the label. Nothing on the " +
				"platform renames the first.",
			"Letters, numbers, spaces and hyphens only, 1 to 45 characters. An underscore, " +
				"a dot or a non-Latin letter is refused with app.display_name_invalid, " +
				"before any request is made.",
			"Nothing needs deploying. The label is not part of the workload, so the change " +
				"is visible immediately.",
			"There is no way to remove a display name. Setting it to the application's own " +
				"name is the closest thing.",
		},

		Related: []string{"app get", "app list", "build set"},
		DocsURL: "https://docs.outplane.com/cli/app",
	}
}
