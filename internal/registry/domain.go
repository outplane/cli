package registry

// Custom domains.
//
// A domain is a route rather than a name: the same host can carry several
// paths, each going to a different application. Every command therefore takes
// --path, and a host with more than one route cannot be addressed by host
// alone.
//
// Platform facts that shape the group:
//
//   - A route binds to a port record, not to an application, so pointing one
//     needs the port as well as the app.
//   - The port has to be HTTP. A TCP port is forwarded rather than proxied and
//     cannot carry a domain.
//   - The application has to have deployed successfully at least once, because
//     a route to something that never ran resolves to nothing.
//   - A certificate is issued automatically once DNS resolves, and there is
//     nothing to configure about it.

func init() {
	Register(
		domainList(),
		domainDNS(),
		domainAdd(),
		domainPoint(),
		domainUnpoint(),
		domainRemove(),
	)
}

func domainArg() Arg {
	return Arg{
		Name:     "host",
		Short:    "the domain, such as app.example.com",
		Required: true,
		Resolves: "domain",
	}
}

func pathFlag() Flag {
	// No default value, deliberately. The route's path is the root when none is
	// given, but the flag has to be able to say "not given": a host carrying one
	// route is addressed by host alone, and a default of "/" would make every
	// lookup a lookup for the root.
	return Flag{
		Name: "path", Type: "string",
		Description: "which route on the host, defaulting to the root. " +
			"Needed only when the host carries several",
	}
}

func domainFields() []Field {
	return []Field{
		{Name: "id", Type: "string | null"},
		{Name: "host", Type: "string"},
		{Name: "path", Type: "string", Description: "trailing slashes are stripped; the root is /"},
		{Name: "app", Type: "string | null", Description: "null while the route points nowhere"},
		{Name: "appId", Type: "string | null"},
		{Name: "portId", Type: "string | null", Description: "the port record the route binds to"},
		{Name: "ssl", Type: "bool", Description: "always true: a certificate is issued for every custom domain"},
		{Name: "url", Type: "string | null", Description: "the address the route answers on"},
	}
}

func domainList() Command {
	return Command{
		Path:  []string{"domain", "list"},
		Short: "list the team's custom domains",
		Long: "Lists every route: a host, a path, and the application it goes to.\n\n" +
			"A route with no application is registered and answers nothing, which is the " +
			"state a domain is added in.",

		Risk:         RiskRead,
		RequiresAuth: true,
		Session:      SessionAny,
		Idempotent:   true,

		APICalls: []string{"GET /api/CustomDomain/GetAll"},

		Flags: []Flag{
			{Name: "app", Type: "string", Description: "only the routes going to this application"},
		},

		OutputFields: domainFields(),

		ErrorCodes: []string{"app.not_found", "context.no_team"},
		ExitCodes:  []int{0, 2, 3, 5, 8},

		Examples: []Example{
			{
				Title:   "every domain in the team",
				Command: "outplane domain list",
				Argv:    []string{"outplane", "domain", "list"},
				Risk:    RiskRead,
			},
			{
				Title:        "what one application answers on",
				Command:      "outplane domain list --app checkout",
				Argv:         []string{"outplane", "domain", "list", "--app", "checkout"},
				Placeholders: map[string]string{"checkout": "<APP_NAME>"},
				Risk:         RiskRead,
			},
		},

		AutomationNotes: []string{
			"One host can appear several times, once per path. The pair identifies a route; " +
				"the host alone does not.",
			"app is null for a route that points nowhere. It is registered, holds the host, " +
				"and answers nothing.",
		},

		Related: []string{"domain add", "domain point", "domain dns"},
		DocsURL: "https://docs.outplane.com/cli/domain",
	}
}

func domainDNS() Command {
	return Command{
		Path:  []string{"domain", "dns"},
		Short: "show the DNS record a host needs",
		Long: "Prints the record to create at your DNS provider.\n\n" +
			"A subdomain takes a CNAME. An apex domain cannot have one and takes an A " +
			"record instead.\n\n" +
			"Nothing is asked of the platform, so this works before the domain is added and " +
			"without being signed in to the team that will hold it.",

		Risk:         RiskRead,
		RequiresAuth: false,
		Session:      SessionAny,
		Idempotent:   true,

		Args: []Arg{
			{Name: "host", Short: "the domain, such as app.example.com", Required: true},
		},

		OutputFields: []Field{
			{Name: "host", Type: "string"},
			{Name: "type", Type: "string", Enum: []string{"CNAME", "A"}},
			{Name: "name", Type: "string", Description: "the record's name in the zone: a label, or @ for the apex"},
			{Name: "value", Type: "string", Description: "what it points at"},
		},

		ErrorCodes: []string{"domain.host_invalid", "usage.missing_argument"},
		ExitCodes:  []int{0, 2},

		Examples: []Example{
			{
				Title:   "a subdomain",
				Command: "outplane domain dns app.example.com",
				Argv:    []string{"outplane", "domain", "dns", "app.example.com"},
				Risk:    RiskRead,
				OutputSample: map[string]any{
					"host":  "app.example.com",
					"type":  "CNAME",
					"name":  "app",
					"value": "domains-management.outplane.app",
				},
			},
			{
				Title:   "an apex domain",
				Command: "outplane domain dns example.com",
				Argv:    []string{"outplane", "domain", "dns", "example.com"},
				Risk:    RiskRead,
			},
		},

		AutomationNotes: []string{
			"Local. No request is made and no credential is needed, so this answers before " +
				"anything exists.",
			"A host with three or more labels is treated as a subdomain and gets a CNAME. " +
				"Two labels is an apex and gets an A record, because a zone cannot have a " +
				"CNAME at its root.",
			"The certificate follows automatically once the record resolves. There is nothing " +
				"else to configure.",
		},

		Related: []string{"domain add", "domain list"},
		DocsURL: "https://docs.outplane.com/cli/domain",
	}
}

func domainAdd() Command {
	return Command{
		Path:  []string{"domain", "add"},
		Short: "register a custom domain",
		Long: "Registers a host, and points it at an application when told where.\n\n" +
			"Without --app and --port the route exists and answers nothing, which is useful " +
			"when the DNS has to be set up before the application is ready.\n\n" +
			"The DNS record to create is printed either way, because a domain that resolves " +
			"nowhere is the usual first problem and this is always the fix.",

		Risk:           RiskWrite,
		RequiresAuth:   true,
		Session:        SessionAny,
		Idempotent:     false,
		SupportsDryRun: true,

		APICalls: []string{
			"GET /api/App/GetAppById/{appId}",
			"POST /api/CustomDomain/Create",
		},

		Args: []Arg{
			{Name: "host", Short: "the domain, such as app.example.com", Required: true},
		},

		Flags: []Flag{
			pathFlag(),
			{Name: "app", Type: "string", Description: "application to point it at"},
			{
				Name: "port", Type: "int",
				Description: "which of the application's ports. Optional when it serves one",
			},
		},

		OutputFields: append(domainFields(), Field{Name: "changed", Type: "bool"}),

		ErrorCodes: []string{
			"domain.host_required",
			"domain.host_invalid",
			"domain.path_invalid",
			"domain.path_reserved",
			"domain.app_required",
			"domain.port_not_found",
			"domain.no_ports",
			"quota.limit_reached",
			"app.not_found",
		},
		ExitCodes: []int{0, 2, 3, 5, 7, 8},

		Examples: []Example{
			{
				Title:        "a domain for an application",
				Command:      "outplane domain add app.example.com --app checkout --port 3000",
				Argv:         []string{"outplane", "domain", "add", "app.example.com", "--app", "checkout", "--port", "3000"},
				Placeholders: map[string]string{"checkout": "<APP_NAME>", "app.example.com": "<HOST>"},
				Risk:         RiskWrite,
			},
			{
				Title:        "register the host first, point it later",
				Command:      "outplane domain add app.example.com",
				Argv:         []string{"outplane", "domain", "add", "app.example.com"},
				Placeholders: map[string]string{"app.example.com": "<HOST>"},
				Risk:         RiskWrite,
			},
			{
				Title:        "a second route on the same host",
				Command:      "outplane domain add example.com --path /api --app api01 --port 8080",
				Argv:         []string{"outplane", "domain", "add", "example.com", "--path", "/api", "--app", "api01", "--port", "8080"},
				Placeholders: map[string]string{"example.com": "<HOST>", "api01": "<APP_NAME>"},
				Risk:         RiskWrite,
			},
		},

		AutomationNotes: []string{
			"The host and the path together have to be unique. The same host with a " +
				"different path is a second route and is allowed.",
			"/.well-known/acme-challenge is refused: the platform answers certificate " +
				"challenges there, and a route on it would stop the certificate renewing.",
			"A TCP port cannot take a domain, and neither can an application that has never " +
				"deployed successfully.",
			"Adding does not make it work. DNS has to point at the record this prints, and " +
				"the certificate follows once it resolves.",
			"How many domains a team may have is a plan limit, so exceeding it is exit 7.",
		},

		Related: []string{"domain dns", "domain point", "domain list"},
		DocsURL: "https://docs.outplane.com/cli/domain",
	}
}

func domainPoint() Command {
	return Command{
		Path:  []string{"domain", "point"},
		Short: "send a domain to an application",
		Long: "Points an existing route at one port of one application.\n\n" +
			"This is also how a domain is moved: pointing it somewhere else replaces where " +
			"it goes, with no gap in between.",

		Risk:           RiskWrite,
		RequiresAuth:   true,
		Session:        SessionAny,
		Idempotent:     true,
		SupportsDryRun: true,

		APICalls: []string{
			"GET /api/CustomDomain/GetAll",
			"GET /api/App/GetAppById/{appId}",
			"PUT /api/CustomDomain/Update/{id}",
		},

		Args: []Arg{domainArg()},

		Flags: []Flag{
			pathFlag(),
			{Name: "app", Type: "string", Description: "application to point it at. Required"},
			{
				Name: "port", Type: "int",
				Description: "which of its ports. Optional when it serves one",
			},
		},

		OutputFields: append(domainFields(), Field{Name: "changed", Type: "bool"}),

		ErrorCodes: []string{
			"domain.not_found",
			"domain.ambiguous",
			"domain.port_not_found",
			"domain.no_ports",
			"app.not_found",
			"usage.missing_argument",
		},
		ExitCodes: []int{0, 2, 3, 5, 8},

		Examples: []Example{
			{
				Title:        "point a registered domain",
				Command:      "outplane domain point app.example.com --app checkout --port 3000",
				Argv:         []string{"outplane", "domain", "point", "app.example.com", "--app", "checkout", "--port", "3000"},
				Placeholders: map[string]string{"checkout": "<APP_NAME>", "app.example.com": "<HOST>"},
				Risk:         RiskWrite,
			},
			{
				Title:        "one route of a host that carries several",
				Command:      "outplane domain point example.com --path /api --app api01",
				Argv:         []string{"outplane", "domain", "point", "example.com", "--path", "/api", "--app", "api01"},
				Placeholders: map[string]string{"example.com": "<HOST>", "api01": "<APP_NAME>"},
				Risk:         RiskWrite,
			},
		},

		AutomationNotes: []string{
			"A host carrying more than one route is ambiguous without --path, and the error " +
				"lists the paths rather than choosing.",
			"Moving a domain is this command, not a remove and an add: the route keeps its " +
				"certificate and there is no window where it answers nothing.",
			"The change reaches the edge within seconds. It needs no deployment.",
		},

		Related: []string{"domain unpoint", "domain add", "app get"},
		DocsURL: "https://docs.outplane.com/cli/domain",
	}
}

func domainUnpoint() Command {
	return Command{
		Path:  []string{"domain", "unpoint"},
		Short: "stop a domain going anywhere, keeping it registered",
		Long: "Detaches a route from its application.\n\n" +
			"The host stays registered and keeps its certificate; it simply answers nothing " +
			"until it is pointed again. Removing it is what gives the host up.",

		Risk:           RiskWrite,
		RequiresAuth:   true,
		Session:        SessionAny,
		Idempotent:     true,
		SupportsDryRun: true,

		APICalls: []string{
			"GET /api/CustomDomain/GetAll",
			"PUT /api/CustomDomain/Update/{id}",
		},

		Args:  []Arg{domainArg()},
		Flags: []Flag{pathFlag()},

		OutputFields: append(domainFields(), Field{Name: "changed", Type: "bool"}),

		ErrorCodes: []string{"domain.not_found", "domain.ambiguous", "usage.missing_argument"},
		ExitCodes:  []int{0, 2, 3, 5, 8},

		Examples: []Example{
			{
				Title:        "take a domain out of service without giving it up",
				Command:      "outplane domain unpoint app.example.com",
				Argv:         []string{"outplane", "domain", "unpoint", "app.example.com"},
				Placeholders: map[string]string{"app.example.com": "<HOST>"},
				Risk:         RiskWrite,
			},
		},

		AutomationNotes: []string{
			"Idempotent. A route that already points nowhere reports changed false and " +
				"exits 0.",
			"Traffic stops immediately. The certificate and the registration are kept, so " +
				"pointing it again needs no DNS change.",
			"This is what an application needs before it can be deleted, since the platform " +
				"refuses to delete one with a domain attached.",
		},

		Related: []string{"domain point", "domain remove", "app delete"},
		DocsURL: "https://docs.outplane.com/cli/domain",
	}
}

func domainRemove() Command {
	return Command{
		Path:  []string{"domain", "remove"},
		Short: "give up a custom domain",
		Long: "Removes a route.\n\n" +
			"Traffic stops at once and the certificate is given up. Adding the host again " +
			"issues a new one, which takes as long as the first did.",

		Risk:           RiskDestructive,
		RequiresAuth:   true,
		Session:        SessionAny,
		Idempotent:     false,
		SupportsDryRun: true,

		APICalls: []string{"DELETE /api/CustomDomain/Delete/{id}"},

		Args: []Arg{domainArg()},

		Flags: []Flag{
			pathFlag(),
			{
				Name: "yes", Short: "y", Type: "bool", Default: "false",
				Description: "acknowledge the removal. Not sufficient on its own",
			},
			{Name: "confirm-name", Type: "string", Description: "the host, typed again"},
		},

		OutputFields: append(domainFields(), Field{Name: "changed", Type: "bool"}),

		ErrorCodes: []string{
			"confirmation.required",
			"domain.confirm_name_mismatch",
			"domain.not_found",
			"domain.ambiguous",
		},
		ExitCodes: []int{0, 2, 3, 4, 5, 8},

		Examples: []Example{
			{
				Title:        "see what would stop answering",
				Command:      "outplane domain remove app.example.com --dry-run",
				Argv:         []string{"outplane", "domain", "remove", "app.example.com", "--dry-run"},
				Placeholders: map[string]string{"app.example.com": "<HOST>"},
				Risk:         RiskRead,
			},
			{
				Title:        "the confirmed form",
				Command:      "outplane domain remove app.example.com --yes --confirm-name app.example.com",
				Argv:         []string{"outplane", "domain", "remove", "app.example.com", "--yes", "--confirm-name", "app.example.com"},
				Placeholders: map[string]string{"app.example.com": "<HOST>"},
				Risk:         RiskDestructive,
			},
		},

		AutomationNotes: []string{
			"Never prompts. Without confirmation it exits 4 and returns the command to " +
				"replay in the error's confirm_command field.",
			"Under a detected agent harness it exits 4 even with both flags.",
			"To take a domain out of service and keep it, use `domain unpoint`. This gives " +
				"up the registration and the certificate.",
			"Traffic stops immediately, before any DNS change of yours takes effect.",
		},

		Related: []string{"domain unpoint", "domain list"},
		DocsURL: "https://docs.outplane.com/cli/domain",
	}
}
