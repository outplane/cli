package registry

// Who can reach an application, by address.
//
// The same seven commands as the env group, in the same order, with the same
// --app flag on the two that attach and detach. The thing has the same shape, a
// team-level object attached to applications and detached by the attachment's
// own id, so a reader who knows one knows this.
//
// Two platform facts every declaration here has to carry:
//
//   - Every rule is an allow rule. The server declares a Deny mode and leaves
//     it unimplemented, so a profile is an allowlist: attaching one means
//     everything not listed stops reaching the application. There is no mode to
//     choose and nothing here offers one.
//   - Updating replaces the whole rule list. `ip-profile set --rule` therefore
//     replaces rather than adds, which is said everywhere it could be
//     misunderstood, because adding silently is how somebody keeps a rule they
//     meant to drop.

func init() {
	Register(
		ipProfileList(),
		ipProfileGet(),
		ipProfileCreate(),
		ipProfileSet(),
		ipProfileDelete(),
		ipProfileAssign(),
		ipProfileUnassign(),
	)
}

func ipProfileFields() []Field {
	return []Field{
		{Name: "id", Type: "string"},
		{Name: "name", Type: "string"},
		{Name: "description", Type: "string | null"},
		{Name: "rules", Type: "int", Description: "how many networks it allows"},
		{Name: "assignments", Type: "int", Description: "how many applications use it"},
		{
			Name: "ruleList", Type: "array",
			Description: "the networks themselves, {cidr, description}. Sorted, and JSON only",
		},
		{
			Name: "assignedApps", Type: "array",
			Description: "the applications using it, {id, app, appId}. id is the assignment's " +
				"own id, which is what detaching needs",
		},
		{Name: "createdAt", Type: "string | null", Description: "RFC 3339, UTC"},
		{Name: "changed", Type: "bool", Description: "false for a dry run"},
	}
}

func ipProfileAppFlag() Flag {
	return Flag{
		Name: "app",
		Type: "string",
		Description: "application name or id. Defaults to the linked app. " +
			"A flag rather than an argument, because the profile is the argument",
	}
}

func ipRuleFlag(what string) Flag {
	return Flag{
		Name: "rule", Type: "strings", Repeatable: true,
		Description: "CIDR[=description], repeatable. " + what,
	}
}

func ipProfileList() Command {
	return Command{
		Path:  []string{"ip-profile", "list"},
		Short: "list the team's IP access profiles",
		Long: "Lists the profiles this team has, with how many networks each allows and how " +
			"many applications use it.\n\n" +
			"A profile is an allowlist. An application it is attached to can be reached " +
			"from the networks listed and from nowhere else; an application with no " +
			"profile attached is reachable from anywhere.",

		Risk:         RiskRead,
		RequiresAuth: true,
		Session:      SessionAny,
		Idempotent:   true,

		APICalls: []string{"GET /api/IPAccessProfile/GetByTeamId"},

		OutputFields: ipProfileFields(),

		ErrorCodes: []string{"context.no_team"},
		ExitCodes:  []int{0, 2, 3, 8},

		Examples: []Example{
			{
				Title:   "what this team has",
				Command: "outplane ip-profile list",
				Argv:    []string{"outplane", "ip-profile", "list"},
				Risk:    RiskRead,
			},
			{
				Title:   "find the ones nothing uses",
				Command: "outplane ip-profile list --json --fields name,assignments",
				Argv:    []string{"outplane", "ip-profile", "list", "--json", "--fields", "name,assignments"},
				Risk:    RiskRead,
				OutputSample: map[string]any{
					"items":     []any{map[string]any{"name": "office", "assignments": 2}},
					"total":     1,
					"truncated": false,
				},
			},
			{
				Title:   "read it as a table",
				Command: "outplane ip-profile list -o text",
				Argv:    []string{"outplane", "ip-profile", "list", "-o", "text"},
				Risk:    RiskRead,
			},
		},

		AutomationNotes: []string{
			"The list carries counts rather than the rules themselves. `ip-profile get` " +
				"returns the networks and the applications using them.",
			"An empty list means no application in this team restricts who can reach it.",
			"Every rule allows. The platform declares a deny mode and does not implement " +
				"it, so a profile can only ever narrow what reaches an application.",
		},

		Related: []string{"ip-profile get", "ip-profile assign", "ip-profile create", "app get"},
		DocsURL: "https://docs.outplane.com/cli/ip-profile",
	}
}

func ipProfileGet() Command {
	return Command{
		Path:  []string{"ip-profile", "get"},
		Short: "show one profile, its rules and what uses it",
		Long: "Shows a profile in full: every network it allows and every application it is " +
			"attached to.\n\n" +
			"The applications matter as much as the rules. Changing a rule on a profile " +
			"that is attached to something changes who can reach a running application, so " +
			"this is what to read before changing one.",

		Risk:         RiskRead,
		RequiresAuth: true,
		Session:      SessionAny,
		Idempotent:   true,

		APICalls: []string{
			"GET /api/IPAccessProfile/GetByTeamId",
			"GET /api/IPAccessProfile/GetById/{profileId}",
		},

		Args:         []Arg{{Name: "profile", Short: "profile name or id", Required: true}},
		OutputFields: ipProfileFields(),

		ErrorCodes: []string{"ipprofile.not_found", "usage.missing_argument", "context.no_team"},
		ExitCodes:  []int{0, 2, 3, 5, 8},

		Examples: []Example{
			{
				Title:        "show a profile",
				Command:      "outplane ip-profile get office",
				Argv:         []string{"outplane", "ip-profile", "get", "office"},
				Placeholders: map[string]string{"office": "<PROFILE_NAME>"},
				Risk:         RiskRead,
			},
			{
				Title:        "read the networks in a pipeline",
				Command:      "outplane ip-profile get office --json --fields ruleList",
				Argv:         []string{"outplane", "ip-profile", "get", "office", "--json", "--fields", "ruleList"},
				Placeholders: map[string]string{"office": "<PROFILE_NAME>"},
				Risk:         RiskRead,
				OutputSample: map[string]any{
					"ruleList": []any{map[string]any{"cidr": "203.0.113.0/24", "description": "office"}},
				},
			},
			{
				Title:        "see which applications a change would affect",
				Command:      "outplane ip-profile get office --json --fields assignedApps",
				Argv:         []string{"outplane", "ip-profile", "get", "office", "--json", "--fields", "assignedApps"},
				Placeholders: map[string]string{"office": "<PROFILE_NAME>"},
				Risk:         RiskRead,
			},
		},

		AutomationNotes: []string{
			"The id inside assignedApps is the assignment's own id, not the application's. " +
				"It is what the platform removes an attachment by, and `ip-profile " +
				"unassign` looks it up rather than asking for it.",
			"An empty ruleList on an attached profile means the application is reachable " +
				"from nowhere, which is a working configuration and almost never the " +
				"intended one.",
			"Two requests, not one: the API addresses profiles by id, so a name has to be " +
				"resolved against the list first.",
		},

		Related: []string{"ip-profile list", "ip-profile set", "ip-profile assign", "ip-profile delete"},
		DocsURL: "https://docs.outplane.com/cli/ip-profile",
	}
}

func ipProfileCreate() Command {
	return Command{
		Path:  []string{"ip-profile", "create"},
		Short: "create an IP access profile",
		Long: "Creates a profile: a list of networks that may reach an application.\n\n" +
			"Creating one changes nothing on its own. It reaches an application when it is " +
			"assigned, and that is the moment traffic starts being turned away.\n\n" +
			"Networks are written in CIDR notation. One address is a /32 for IPv4 and a " +
			"/128 for IPv6; a bare address is refused rather than guessed at.",

		Risk:         RiskWrite,
		RequiresAuth: true,
		Session:      SessionAny,

		// The server refuses a duplicate name rather than replacing, so running
		// this twice is not the same as running it once.
		Idempotent: false,

		SupportsDryRun: true,

		APICalls: []string{"POST /api/IPAccessProfile/Create"},

		Args: []Arg{{Name: "name", Short: "what to call the profile", Required: true}},

		Flags: []Flag{
			ipRuleFlag("Each one is an allow rule"),
			{Name: "description", Type: "string", Description: "what the profile is for"},
		},

		OutputFields: ipProfileFields(),

		ErrorCodes: []string{
			"ipprofile.name_required",
			"ipprofile.name_invalid",
			"ipprofile.description_invalid",
			"ipprofile.cidr_required",
			"ipprofile.cidr_invalid",
			"ipprofile.rule_duplicate",
			"usage.missing_argument",
			"context.no_team",
		},
		ExitCodes: []int{0, 2, 3, 6, 8},

		Examples: []Example{
			{
				Title:   "allow one office network",
				Command: "outplane ip-profile create office --rule 203.0.113.0/24=head office",
				Argv: []string{"outplane", "ip-profile", "create", "office",
					"--rule", "203.0.113.0/24=head office"},
				Placeholders: map[string]string{"office": "<PROFILE_NAME>"},
				Risk:         RiskWrite,
			},
			{
				Title:   "several networks at once",
				Command: "outplane ip-profile create office --rule 203.0.113.0/24 --rule 198.51.100.7/32=vpn",
				Argv: []string{"outplane", "ip-profile", "create", "office",
					"--rule", "203.0.113.0/24", "--rule", "198.51.100.7/32=vpn"},
				Placeholders: map[string]string{"office": "<PROFILE_NAME>"},
				Risk:         RiskWrite,
			},
			{
				Title:   "check the rules would be accepted, without creating anything",
				Command: "outplane ip-profile create office --rule 203.0.113.0/24 --dry-run --json",
				Argv: []string{"outplane", "ip-profile", "create", "office",
					"--rule", "203.0.113.0/24", "--dry-run", "--json"},
				Placeholders: map[string]string{"office": "<PROFILE_NAME>"},
				Risk:         RiskRead,
				OutputSample: map[string]any{"name": "office", "rules": 1, "changed": false},
			},
		},

		AutomationNotes: []string{
			"A profile with no rules is accepted and allows nothing. Assigned to an " +
				"application, it makes that application reachable from nowhere. " +
				"--rule \"\" on its own is how to ask for that, the same way an empty " +
				"value clears a build filter.",
			"A bare address is refused with ipprofile.cidr_invalid rather than being read " +
				"as a /32, because guessing a prefix length is guessing how much of a " +
				"network to let in.",
			"Creating changes nothing that is running. `ip-profile assign` is what applies " +
				"it, and it applies immediately rather than at the next deployment.",
			"The same name twice is refused by the server, so this is not idempotent.",
		},

		Related: []string{"ip-profile assign", "ip-profile set", "ip-profile list", "ip-profile delete"},
		DocsURL: "https://docs.outplane.com/cli/ip-profile",
	}
}

func ipProfileSet() Command {
	return Command{
		Path:  []string{"ip-profile", "set"},
		Short: "change a profile's rules, name or description",
		Long: "Changes a profile.\n\n" +
			"--rule replaces the whole list rather than adding to it. That is the API's " +
			"shape and it is also the safer one: a profile is an allowlist, and adding " +
			"silently is how a rule nobody meant to keep survives a change.\n\n" +
			"Anything not named is read and sent back unchanged, so changing a description " +
			"does not clear the rules.\n\n" +
			"A change to an assigned profile applies immediately, to a running " +
			"application. This is the one command in the group that can take an " +
			"application off the network without a deployment.",

		Risk:         RiskWrite,
		RequiresAuth: true,
		Session:      SessionAny,
		Idempotent:   true,

		SupportsDryRun: true,

		APICalls: []string{
			"GET /api/IPAccessProfile/GetById/{profileId}",
			"PUT /api/IPAccessProfile/Update/{profileId}",
		},

		Args: []Arg{{Name: "profile", Short: "profile name or id", Required: true}},

		Flags: []Flag{
			ipRuleFlag("Replaces every existing rule"),
			{Name: "name", Type: "string", Description: "rename the profile"},
			{Name: "description", Type: "string", Description: "what the profile is for"},
		},

		OutputFields: ipProfileFields(),

		ErrorCodes: []string{
			"ipprofile.not_found",
			"ipprofile.name_invalid",
			"ipprofile.description_invalid",
			"ipprofile.cidr_invalid",
			"ipprofile.rule_duplicate",
			"usage.no_change",
			"usage.missing_argument",
			"context.no_team",
		},
		ExitCodes: []int{0, 2, 3, 5, 8},

		Examples: []Example{
			{
				Title:   "see what the new list would be, and who it would affect",
				Command: "outplane ip-profile set office --rule 203.0.113.0/24 --dry-run --json",
				Argv: []string{"outplane", "ip-profile", "set", "office",
					"--rule", "203.0.113.0/24", "--dry-run", "--json"},
				Placeholders: map[string]string{"office": "<PROFILE_NAME>"},
				Risk:         RiskRead,
				OutputSample: map[string]any{"name": "office", "rules": 1, "changed": false},
			},
			{
				Title:   "replace the rules",
				Command: "outplane ip-profile set office --rule 203.0.113.0/24 --rule 198.51.100.7/32=vpn",
				Argv: []string{"outplane", "ip-profile", "set", "office",
					"--rule", "203.0.113.0/24", "--rule", "198.51.100.7/32=vpn"},
				Placeholders: map[string]string{"office": "<PROFILE_NAME>"},
				Risk:         RiskWrite,
			},
			{
				Title:        "change only the description, leaving the rules alone",
				Command:      "outplane ip-profile set office --description \"head office and VPN\" --json --fields rules,changed",
				Argv:         []string{"outplane", "ip-profile", "set", "office", "--description", "head office and VPN", "--json", "--fields", "rules,changed"},
				Placeholders: map[string]string{"office": "<PROFILE_NAME>"},
				Risk:         RiskWrite,
			},
		},

		AutomationNotes: []string{
			"--rule replaces the entire list. Sending one rule to a profile that had four " +
				"leaves it with one, and the other three are gone. --rule \"\" leaves none.",
			"A flag that is not given keeps its value: the endpoint replaces everything it " +
				"is given, so this command reads the profile first and sends it back with " +
				"the change applied.",
			"The change applies immediately to every application the profile is attached " +
				"to, with no deployment. The result names them, and `ip-profile get` names " +
				"them beforehand.",
			"Sending no flags at all is refused with usage.no_change rather than being a " +
				"request that rewrites the profile with itself.",
		},

		Related: []string{"ip-profile get", "ip-profile create", "ip-profile assign", "ip-profile list"},
		DocsURL: "https://docs.outplane.com/cli/ip-profile",
	}
}

func ipProfileDelete() Command {
	return Command{
		Path:  []string{"ip-profile", "delete"},
		Short: "permanently delete an IP access profile",
		Long: "Deletes a profile and its rules.\n\n" +
			"The platform refuses while any application still uses it, and says so. " +
			"Detach it everywhere first with `ip-profile unassign`.\n\n" +
			"Nothing restores the rules afterwards.",

		Risk:         RiskDestructive,
		RequiresAuth: true,
		Session:      SessionAny,
		Idempotent:   true,

		SupportsDryRun: true,

		APICalls: []string{
			"GET /api/IPAccessProfile/GetById/{profileId}",
			"DELETE /api/IPAccessProfile/Delete/{profileId}",
		},

		Args: []Arg{{Name: "profile", Short: "profile name or id", Required: true}},

		Flags: []Flag{
			{
				Name: "yes", Short: "y", Type: "bool", Default: "false",
				Description: "acknowledge the deletion. Not sufficient on its own",
			},
			{
				Name: "confirm-name", Type: "string",
				Description: "the profile's name, repeated. Guards against deleting the wrong one",
			},
		},

		OutputFields: ipProfileFields(),

		ErrorCodes: []string{
			"ipprofile.not_found",
			"ipprofile.confirm_name_mismatch",
			"confirmation.required",
			"usage.missing_argument",
			"context.no_team",
		},
		ExitCodes: []int{0, 2, 3, 4, 5, 8},

		Examples: []Example{
			{
				Title:        "check what would go, and what still uses it",
				Command:      "outplane ip-profile delete office --dry-run --json",
				Argv:         []string{"outplane", "ip-profile", "delete", "office", "--dry-run", "--json"},
				Placeholders: map[string]string{"office": "<PROFILE_NAME>"},
				Risk:         RiskRead,
				OutputSample: map[string]any{"name": "office", "rules": 2, "assignments": 0, "changed": false},
			},
			{
				Title:        "request deletion. Returns exit 4 with a command to replay",
				Command:      "outplane ip-profile delete office",
				Argv:         []string{"outplane", "ip-profile", "delete", "office"},
				Placeholders: map[string]string{"office": "<PROFILE_NAME>"},
				Risk:         RiskWrite,
			},
			{
				Title:        "the confirmed form",
				Command:      "outplane ip-profile delete office --yes --confirm-name office",
				Argv:         []string{"outplane", "ip-profile", "delete", "office", "--yes", "--confirm-name", "office"},
				Placeholders: map[string]string{"office": "<PROFILE_NAME>"},
				Risk:         RiskDestructive,
			},
		},

		AutomationNotes: []string{
			"The platform refuses while the profile is assigned to anything, with its own " +
				"message. That is the server's rule and this command does not predict it; " +
				"`ip-profile get` lists what still uses it.",
			"Refused under an agent harness, exit 4 with confirmation.required, whatever " +
				"flags are given.",
			"Outside a harness, both --yes and --confirm-name are required and the name " +
				"has to match exactly.",
		},

		Related: []string{"ip-profile unassign", "ip-profile get", "ip-profile list"},
		DocsURL: "https://docs.outplane.com/cli/ip-profile",
	}
}

func ipProfileAssign() Command {
	return Command{
		Path:  []string{"ip-profile", "assign"},
		Short: "restrict an application to a profile's networks",
		Long: "Attaches a profile to an application.\n\n" +
			"This is the moment traffic starts being turned away: from now on the " +
			"application answers the networks in the profile and nobody else.\n\n" +
			"It applies immediately, without a deployment, which makes it the fastest way " +
			"in this CLI to take a running application off the public internet, in both " +
			"the intended and the unintended sense.",

		Risk:         RiskWrite,
		RequiresAuth: true,
		Session:      SessionAny,
		Idempotent:   true,

		SupportsDryRun: true,

		APICalls: []string{
			"GET /api/IPAccessProfile/GetById/{profileId}",
			"GET /api/App/GetAppsByTeamId",
			"POST /api/IPAccessProfile/Assign",
		},

		Args:  []Arg{{Name: "profile", Short: "profile name or id", Required: true}},
		Flags: []Flag{ipProfileAppFlag()},

		OutputFields: []Field{
			{Name: "action", Type: "string", Enum: []string{"assign", "unassign"}},
			{Name: "profile", Type: "string"},
			{Name: "profileId", Type: "string"},
			{Name: "app", Type: "string"},
			{Name: "appId", Type: "string"},
			{Name: "rules", Type: "int", Description: "how many networks the application will answer"},
			{
				Name:        "changed",
				Type:        "bool",
				Description: "false for a dry run, and false when it was already attached",
			},
		},

		ErrorCodes: []string{
			"ipprofile.not_found",
			"app.not_found",
			"context.no_app",
			"usage.missing_argument",
		},
		ExitCodes: []int{0, 2, 3, 5, 8},

		Examples: []Example{
			{
				Title:   "see what it would restrict, without restricting it",
				Command: "outplane ip-profile assign office --app checkout --dry-run --json",
				Argv: []string{"outplane", "ip-profile", "assign", "office", "--app", "checkout",
					"--dry-run", "--json"},
				Placeholders: map[string]string{"office": "<PROFILE_NAME>", "checkout": "<APP_NAME>"},
				Risk:         RiskRead,
				OutputSample: map[string]any{
					"action": "assign", "profile": "office", "app": "checkout",
					"rules": 2, "changed": false,
				},
			},
			{
				Title:        "restrict an application",
				Command:      "outplane ip-profile assign office --app checkout",
				Argv:         []string{"outplane", "ip-profile", "assign", "office", "--app", "checkout"},
				Placeholders: map[string]string{"office": "<PROFILE_NAME>", "checkout": "<APP_NAME>"},
				Risk:         RiskWrite,
			},
			{
				Title:        "read the result in a pipeline",
				Command:      "outplane ip-profile assign office --app checkout --json --fields app,rules,changed",
				Argv:         []string{"outplane", "ip-profile", "assign", "office", "--app", "checkout", "--json", "--fields", "app,rules,changed"},
				Placeholders: map[string]string{"office": "<PROFILE_NAME>", "checkout": "<APP_NAME>"},
				Risk:         RiskWrite,
			},
		},

		AutomationNotes: []string{
			"Applies immediately. Unlike an environment variable or a port, this does not " +
				"wait for a deployment: the application stops answering other addresses as " +
				"soon as the request returns.",
			"Idempotent. Attaching something already attached reports changed false and " +
				"exits 0.",
			"A profile with no rules makes the application reachable from nowhere. The " +
				"request is not refused, and the result reports rules 0.",
			"More than one profile can be attached to the same application. They add up: " +
				"an address allowed by any attached profile gets through.",
		},

		Related: []string{"ip-profile unassign", "ip-profile get", "ip-profile create", "app get"},
		DocsURL: "https://docs.outplane.com/cli/ip-profile",
	}
}

func ipProfileUnassign() Command {
	return Command{
		Path:  []string{"ip-profile", "unassign"},
		Short: "stop an application using a profile",
		Long: "Detaches a profile from an application.\n\n" +
			"The application is reachable from anywhere again, unless another profile is " +
			"still attached to it. This applies immediately, without a deployment.\n\n" +
			"The platform removes an attachment by the attachment's own id rather than by " +
			"the pair, so the attachment is looked up first. That is one extra request and " +
			"it is what turns a missing attachment into a sentence rather than a 404.",

		Risk:         RiskWrite,
		RequiresAuth: true,
		Session:      SessionAny,
		Idempotent:   true,

		SupportsDryRun: true,

		APICalls: []string{
			"GET /api/IPAccessProfile/GetById/{profileId}",
			"GET /api/App/GetAppsByTeamId",
			"DELETE /api/IPAccessProfile/Unassign/{assignmentId}",
		},

		Args:  []Arg{{Name: "profile", Short: "profile name or id", Required: true}},
		Flags: []Flag{ipProfileAppFlag()},

		OutputFields: []Field{
			{Name: "action", Type: "string", Enum: []string{"assign", "unassign"}},
			{Name: "profile", Type: "string"},
			{Name: "profileId", Type: "string"},
			{Name: "app", Type: "string"},
			{Name: "appId", Type: "string"},
			{Name: "rules", Type: "int"},
			{
				Name:        "changed",
				Type:        "bool",
				Description: "false for a dry run, and false when it was not attached",
			},
		},

		ErrorCodes: []string{
			"ipprofile.not_found",
			"app.not_found",
			"context.no_app",
			"usage.missing_argument",
		},
		ExitCodes: []int{0, 2, 3, 5, 8},

		Examples: []Example{
			{
				Title:   "see what would open up, without opening it",
				Command: "outplane ip-profile unassign office --app checkout --dry-run --json",
				Argv: []string{"outplane", "ip-profile", "unassign", "office", "--app", "checkout",
					"--dry-run", "--json"},
				Placeholders: map[string]string{"office": "<PROFILE_NAME>", "checkout": "<APP_NAME>"},
				Risk:         RiskRead,
			},
			{
				Title:        "stop restricting an application",
				Command:      "outplane ip-profile unassign office --app checkout",
				Argv:         []string{"outplane", "ip-profile", "unassign", "office", "--app", "checkout"},
				Placeholders: map[string]string{"office": "<PROFILE_NAME>", "checkout": "<APP_NAME>"},
				Risk:         RiskWrite,
			},
			{
				Title:        "read the result in a pipeline",
				Command:      "outplane ip-profile unassign office --app checkout --json --fields app,changed",
				Argv:         []string{"outplane", "ip-profile", "unassign", "office", "--app", "checkout", "--json", "--fields", "app,changed"},
				Placeholders: map[string]string{"office": "<PROFILE_NAME>", "checkout": "<APP_NAME>"},
				Risk:         RiskWrite,
			},
		},

		AutomationNotes: []string{
			"Applies immediately, with no deployment. The application answers every " +
				"address again as soon as the request returns, unless another profile is " +
				"still attached.",
			"Idempotent. Detaching something that was not attached reports changed false " +
				"and exits 0.",
			"Deleting a profile is refused while anything still uses it, so this is the " +
				"command that has to run first.",
		},

		Related: []string{"ip-profile assign", "ip-profile get", "ip-profile delete"},
		DocsURL: "https://docs.outplane.com/cli/ip-profile",
	}
}
