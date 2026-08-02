package registry

// The ports an application serves.
//
// This group is the env group with different nouns, on purpose: list, get, set
// and unset, the application named with --app rather than positionally, --deploy
// on both mutations, and the same sentence about a change not reaching the
// running application. Two groups that behave alike should read alike.
//
// One platform fact does differ, and every declaration here has to carry it.
// Environment variables have a merging endpoint, so `env set` cannot touch a
// variable it was not told about. Ports have only a replacing one: whatever is
// missing from the request is deleted. The CLI therefore reads the current set
// and sends it back with the change applied, which is invisible in normal use
// and has one consequence worth publishing. Two callers changing different
// ports at the same time can lose one of the changes. That is in the automation
// notes of both mutations, and --dry-run prints the whole set that would be
// sent rather than the part that changed.

func init() {
	Register(
		portList(),
		portGet(),
		portSet(),
		portUnset(),
	)
}

// portAppFlag is the same declaration in all four commands. It is a flag rather
// than an argument for the reason env's is: the commands take a list of ports,
// so a leading positional could not be told from one of them.
func portAppFlag() Flag {
	return Flag{
		Name: "app",
		Type: "string",
		Description: "application name or id. Defaults to the linked app. " +
			"A flag rather than an argument, so that port numbers cannot be mistaken for it",
	}
}

func portDeployFlag() Flag {
	return Flag{
		Name:        "deploy",
		Type:        "bool",
		Default:     "false",
		Description: "deploy afterwards, so the change reaches the running app",
	}
}

// portSpecArg is the argument shared by set, and it is the same syntax
// `app create --port` takes, so that a port written once is written the same
// way everywhere.
func portSpecArg() Arg {
	return Arg{
		Name:     "port",
		Short:    "PORT[:SCHEME[:public|private]], repeatable. A part left out keeps what the port already had",
		Required: true,
		Variadic: true,
	}
}

func portList() Command {
	return Command{
		Path:  []string{"port", "list"},
		Short: "list the ports an application serves",
		Long: "Lists the ports an application serves, with how each one is reached.\n\n" +
			"A public HTTP port has a platform address; a public TCP port has a host and a " +
			"port number instead. A private port has neither and is reachable only from " +
			"inside the platform, which is what an application behind another one wants.\n\n" +
			"Custom domains are counted here and listed in full with --json, because a " +
			"domain is attached to a port and removing the port leaves the domain pointing " +
			"nowhere.",

		Risk:         RiskRead,
		RequiresAuth: true,
		Session:      SessionAny,
		Idempotent:   true,

		APICalls: []string{
			"GET /api/App/GetAppsByTeamId",
			"GET /api/App/GetAppById/{appId}",
		},

		Flags: []Flag{portAppFlag()},

		OutputFields: []Field{
			{Name: "port", Type: "int", Description: "the port the application listens on"},
			{Name: "portId", Type: "string", Description: "the port's own id, which attaching a custom domain needs"},
			{
				Name: "scheme", Type: "string", Enum: []string{"http", "h2c", "tcp"},
				Description: "how it is served. An unknown value arrives as unknown:N rather than being guessed",
			},
			{Name: "public", Type: "bool", Description: "whether it is reachable from outside the platform"},
			{
				Name:        "url",
				Type:        "string | null",
				Description: "the address, or null for a private port. TCP is host:port rather than a URL",
			},
			{Name: "domains", Type: "int", Description: "how many custom domains point at this port"},
			{Name: "customDomains", Type: "array", Description: "their full addresses"},
		},

		ErrorCodes: []string{"context.no_app", "app.not_found"},
		ExitCodes:  []int{0, 2, 3, 5, 8},

		Examples: []Example{
			{
				Title:   "list the linked application's ports",
				Command: "outplane port list",
				Argv:    []string{"outplane", "port", "list"},
				Risk:    RiskRead,
			},
			{
				Title:        "list another application's",
				Command:      "outplane port list --app checkout",
				Argv:         []string{"outplane", "port", "list", "--app", "checkout"},
				Placeholders: map[string]string{"checkout": "<APP_NAME>"},
				Risk:         RiskRead,
			},
			{
				Title:   "find the public ones in a script",
				Command: "outplane port list --json --fields port,public,url",
				Argv:    []string{"outplane", "port", "list", "--json", "--fields", "port,public,url"},
				Risk:    RiskRead,
				OutputSample: map[string]any{
					"items": []any{
						map[string]any{"port": 3000, "public": true, "url": "https://checkout-3000-acme.outplane.app"},
						map[string]any{"port": 5432, "public": false, "url": nil},
					},
					"total":     2,
					"truncated": false,
				},
			},
		},

		AutomationNotes: []string{
			"url is null for a private port, which is not an error: a private port is " +
				"reachable from inside the platform and has no address at all.",
			"A public TCP port's url is a host and a port number, not a URL with a scheme. " +
				"The platform allocates the outside port number and it is not the one the " +
				"application listens on.",
			"portId is what `outplane domain point` needs. The port number is not enough, " +
				"because a domain is attached to the port record.",
			"An empty list means nothing can reach the application, including a deployment " +
				"that is otherwise healthy.",
		},

		Related: []string{"port set", "port unset", "domain point", "app get"},
		DocsURL: "https://docs.outplane.com/cli/port",
	}
}

func portGet() Command {
	return Command{
		Path:  []string{"port", "get"},
		Short: "print one port's address",
		Long: "Prints the address of one port and nothing else.\n\n" +
			"Nothing else is the point: `curl $(outplane port get 3000)` has to work, so " +
			"there is no table and no commentary on standard output.\n\n" +
			"A private port has no address, and that is an error rather than an empty line. " +
			"An empty line inside a command substitution becomes a request to nowhere, and " +
			"the failure then appears somewhere with no connection to the cause.",

		Risk:         RiskRead,
		RequiresAuth: true,
		Session:      SessionAny,
		Idempotent:   true,

		// Text at both ends, like `env get` and `db url`: the value is what a
		// shell captures, and turning it into JSON when piped would break the
		// one thing this command exists for.
		Output: &OutputMode{TTY: "text", Piped: "text"},

		APICalls: []string{
			"GET /api/App/GetAppsByTeamId",
			"GET /api/App/GetAppById/{appId}",
		},

		Args: []Arg{
			{Name: "port", Short: "the port number", Required: true},
		},

		Flags: []Flag{portAppFlag()},

		OutputFields: []Field{
			{Name: "port", Type: "int"},
			{Name: "portId", Type: "string"},
			{Name: "scheme", Type: "string", Enum: []string{"http", "h2c", "tcp"}},
			{Name: "public", Type: "bool"},
			{Name: "url", Type: "string", Description: "the address. Text mode prints this alone"},
			{Name: "customDomains", Type: "array"},
		},

		ErrorCodes: []string{
			"port.not_found",
			"port.no_address",
			"usage.bad_port",
			"context.no_app",
			"app.not_found",
		},
		ExitCodes: []int{0, 2, 3, 5, 8},

		Examples: []Example{
			{
				Title:   "print one port's address",
				Command: "outplane port get 3000",
				Argv:    []string{"outplane", "port", "get", "3000"},
				Risk:    RiskRead,
			},
			{
				Title:   "call the application at it",
				Command: "curl \"$(outplane port get 3000)/health\"",
				Argv:    []string{"outplane", "port", "get", "3000"},
				Risk:    RiskRead,
			},
			{
				Title:        "read the whole record instead",
				Command:      "outplane port get 3000 --app checkout --json",
				Argv:         []string{"outplane", "port", "get", "3000", "--app", "checkout", "--json"},
				Placeholders: map[string]string{"checkout": "<APP_NAME>"},
				Risk:         RiskRead,
				OutputSample: map[string]any{
					"port":   3000,
					"scheme": "http",
					"public": true,
					"url":    "https://checkout-3000-acme.outplane.app",
				},
			},
		},

		AutomationNotes: []string{
			"Text mode writes the address and a newline, with nothing else on standard " +
				"output, which is what makes command substitution safe. Notes and errors " +
				"go to standard error as everywhere else.",
			"Piped output stays text rather than becoming JSON, unlike most commands here. " +
				"--json still gives the object for a caller that wants the record.",
			"A private port exits 5 with port.no_address. It is not a missing port, and " +
				"`port list` shows it.",
			"A port that does not exist exits 5 with port.not_found and the error names " +
				"the ports that do.",
		},

		Related: []string{"port list", "port set", "app get", "env get"},
		DocsURL: "https://docs.outplane.com/cli/port",
	}
}

func portSet() Command {
	return Command{
		Path:  []string{"port", "set"},
		Short: "open a port, or change one that is already open",
		Long: "Opens ports on an application, or changes the ones already open.\n\n" +
			"A part left out of the argument keeps what the port already had, so " +
			"`port set 3000` on a public port leaves it public. On a port that is not open " +
			"yet there is nothing to keep and the defaults apply: private, HTTP.\n\n" +
			"Ports not named are left exactly as they are.\n\n" +
			"The running application keeps its old ports until it is deployed again. " +
			"--deploy does that immediately.",

		Risk:         RiskWrite,
		RequiresAuth: true,
		Session:      SessionAny,
		Idempotent:   true,

		SupportsDryRun: true,

		APICalls: []string{
			"GET /api/App/GetAppById/{appId}",
			"PUT /api/AppSetting/UpdateAppPorts/{appId}",
			"POST /api/AppDeployment/CreateAppDeployment/{appId}",
		},

		Args:  []Arg{portSpecArg()},
		Flags: []Flag{portAppFlag(), portDeployFlag()},

		OutputFields: []Field{
			{Name: "action", Type: "string", Enum: []string{"set", "unset"}},
			{Name: "numbers", Type: "array", Description: "the ports named on the command line"},
			{Name: "app", Type: "string"},
			{Name: "appId", Type: "string"},
			{Name: "serving", Type: "int", Description: "how many ports the application serves afterwards"},
			{
				Name: "ports",
				Type: "array",
				Description: "every port that was sent, {port, scheme, public}. This is the " +
					"whole set, not the part that changed, because the request replaces",
			},
			{Name: "changed", Type: "bool", Description: "false for a dry run"},
			{
				Name:        "deploymentId",
				Type:        "int | null",
				Description: "the deployment --deploy started, or null when it was not given",
			},
		},

		ErrorCodes: []string{
			"app.port_invalid",
			"app.scheme_invalid",
			"app.port_duplicate",
			"usage.bad_port",
			"usage.missing_argument",
			"context.no_app",
			"app.not_found",
		},
		ExitCodes: []int{0, 2, 3, 5, 8},

		Examples: []Example{
			{
				Title:   "open a public HTTP port",
				Command: "outplane port set 3000:http:public",
				Argv:    []string{"outplane", "port", "set", "3000:http:public"},
				Risk:    RiskWrite,
			},
			{
				Title:   "open several at once",
				Command: "outplane port set 3000:http:public 5432:tcp:public 9000",
				Argv:    []string{"outplane", "port", "set", "3000:http:public", "5432:tcp:public", "9000"},
				Risk:    RiskWrite,
			},
			{
				Title:   "make an open port public without touching its scheme",
				Command: "outplane port set 3000::public --deploy",
				Argv:    []string{"outplane", "port", "set", "3000::public", "--deploy"},
				Risk:    RiskWrite,
			},
			{
				Title:   "see the whole set that would be sent",
				Command: "outplane port set 8080 --dry-run --json",
				Argv:    []string{"outplane", "port", "set", "8080", "--dry-run", "--json"},
				Risk:    RiskRead,
				OutputSample: map[string]any{
					"action":  "set",
					"numbers": []any{8080},
					"serving": 3,
					"ports": []any{
						map[string]any{"port": 3000, "scheme": "http", "public": true},
						map[string]any{"port": 5432, "scheme": "tcp", "public": false},
						map[string]any{"port": 8080, "scheme": "http", "public": false},
					},
					"changed": false,
				},
			},
		},

		AutomationNotes: []string{
			"A part left out keeps what the port already had. `port set 3000` does not make " +
				"a public port private; `3000::private` does. On a port that is not open " +
				"yet, an omitted part takes the create-time default of private HTTP.",
			"The API replaces the whole set rather than merging, so this command reads the " +
				"current ports and sends them back with the change applied. Two callers " +
				"changing different ports at the same time can therefore lose one of the " +
				"changes, which is not possible with `env set`.",
			"ports in the result is everything that was sent, not the part that changed. " +
				"That is the honest report for a replacing request and it is what --dry-run " +
				"prints.",
			"Saving does not restart anything. Without --deploy the running application " +
				"keeps the ports it started with.",
			"Making a TCP port public allocates an outside port number, which is not the " +
				"one the application listens on. `port list` reports the address afterwards.",
			"deploymentId is null unless --deploy was given. A queued deployment is not a " +
				"finished one; follow it with `outplane deploy logs`.",
		},

		Related: []string{"port list", "port unset", "domain point", "deploy create"},
		DocsURL: "https://docs.outplane.com/cli/port",
	}
}

func portUnset() Command {
	return Command{
		Path:  []string{"port", "unset"},
		Short: "stop an application serving a port",
		Long: "Closes ports on an application.\n\n" +
			"Every number is checked before anything is sent, so a typo in the third one " +
			"does not leave the first two closed.\n\n" +
			"Ports not named are left exactly as they are. The running application keeps " +
			"its old ports until it is deployed again.",

		Risk:         RiskWrite,
		RequiresAuth: true,
		Session:      SessionAny,
		Idempotent:   true,

		SupportsDryRun: true,

		APICalls: []string{
			"GET /api/App/GetAppById/{appId}",
			"PUT /api/AppSetting/UpdateAppPorts/{appId}",
			"POST /api/AppDeployment/CreateAppDeployment/{appId}",
		},

		Args: []Arg{
			{
				Name:     "port",
				Short:    "the port number, repeatable",
				Required: true,
				Variadic: true,
			},
		},
		Flags: []Flag{portAppFlag(), portDeployFlag()},

		OutputFields: []Field{
			{Name: "action", Type: "string", Enum: []string{"set", "unset"}},
			{Name: "numbers", Type: "array", Description: "the ports named on the command line"},
			{Name: "app", Type: "string"},
			{Name: "appId", Type: "string"},
			{Name: "serving", Type: "int", Description: "how many ports the application serves afterwards"},
			{Name: "ports", Type: "array", Description: "every port that was sent, {port, scheme, public}"},
			{Name: "changed", Type: "bool", Description: "false for a dry run"},
			{Name: "deploymentId", Type: "int | null"},
		},

		ErrorCodes: []string{
			"port.not_found",
			"usage.bad_port",
			"usage.missing_argument",
			"context.no_app",
			"app.not_found",
		},
		ExitCodes: []int{0, 2, 3, 5, 8},

		Examples: []Example{
			{
				Title:   "see what would be left",
				Command: "outplane port unset 8080 --dry-run",
				Argv:    []string{"outplane", "port", "unset", "8080", "--dry-run"},
				Risk:    RiskRead,
			},
			{
				Title:   "close a port",
				Command: "outplane port unset 8080",
				Argv:    []string{"outplane", "port", "unset", "8080"},
				Risk:    RiskWrite,
			},
			{
				Title:        "close several on another application, and apply it",
				Command:      "outplane port unset 8080 9000 --app checkout --deploy",
				Argv:         []string{"outplane", "port", "unset", "8080", "9000", "--app", "checkout", "--deploy"},
				Placeholders: map[string]string{"checkout": "<APP_NAME>"},
				Risk:         RiskWrite,
			},
			{
				Title:   "read the result in a pipeline",
				Command: "outplane port unset 8080 --json --fields numbers,serving,changed",
				Argv:    []string{"outplane", "port", "unset", "8080", "--json", "--fields", "numbers,serving,changed"},
				Risk:    RiskWrite,
			},
		},

		AutomationNotes: []string{
			"A number the application does not serve is an error, exit 5 with " +
				"port.not_found, and nothing is sent. The error names the ports it does " +
				"serve.",
			"A custom domain attached to a closed port stays, and points nowhere. The " +
				"platform does not delete it and neither does this command; " +
				"`outplane domain list` shows a route with no application.",
			"Closing every port leaves an application nothing can reach, and that is " +
				"allowed. The request is not refused.",
			"The API replaces the whole set rather than merging, so this command sends " +
				"every remaining port. ports in the result is that set.",
			"Saving does not restart anything. Without --deploy the running application " +
				"keeps serving the port until its next deployment.",
		},

		Related: []string{"port list", "port set", "domain list", "deploy create"},
		DocsURL: "https://docs.outplane.com/cli/port",
	}
}
