package registry

// The agent skill, installed into whichever coding tools are on this machine.
//
// The skill has its own repository and its own releases, and is installed by
// tools that have never heard of this CLI. This group is a third door onto the
// same thing, for somebody who already has the CLI in their hand. It therefore
// fetches rather than carrying a copy: a skill compiled into this binary would
// be exactly as old as the binary, and publishing the skill separately exists
// precisely so that it does not have to be.
//
// Nothing here touches Out Plane. It reads a public repository and writes files
// in a home directory, so there is no credential and no team.

func init() {
	Register(
		skillsInstall(),
		skillsList(),
		skillsUpdate(),
		skillsRemove(),
	)
}

// agentFlag is shared by every command here, so the set of tools cannot drift
// between installing into one and removing from another.
func agentFlag() Flag {
	return Flag{
		Name: "agent", Type: "string",
		Description: "which coding tool to act on. Defaults to every one detected on this machine",
		Enum:        []string{"claude-code", "cursor", "codex", "opencode"},
	}
}

func projectFlag() Flag {
	return Flag{
		Name: "project", Type: "bool", Default: "false",
		Description: "act on this directory's skills folder rather than the home one, so the skill can be committed with the repository",
	}
}

func skillsFields() []Field {
	return []Field{
		{Name: "agent", Type: "string", Description: "the coding tool"},
		{Name: "path", Type: "string", Description: "the skills directory acted on"},
		{Name: "version", Type: "string | null", Description: "the skill version now in that directory"},
		{Name: "changed", Type: "bool", Description: "false when it already matched, and for a dry run"},
	}
}

func skillsInstall() Command {
	return Command{
		Path:  []string{"skills", "install"},
		Short: "install the Out Plane agent skill into your coding tools",
		Long: "Downloads the newest release of the Out Plane agent skill and writes it " +
			"into every coding tool detected on this machine.\n\n" +
			"The skill teaches an agent which command answers which question, what needs " +
			"a deployment and what does not, and how the confirmation protocol works. It " +
			"loads on its own when a conversation is about Out Plane.\n\n" +
			"It comes from github.com/outplane/skills, which is where it is released and " +
			"reviewed. This command is one of several ways to install it; the plugin " +
			"marketplaces carry the same skill.\n\n" +
			"Restart the coding tool afterwards so it picks the skill up.",

		Risk: RiskWrite,
		// A public repository and a home directory. Neither needs a credential,
		// and this has to work before anybody has signed in.
		RequiresAuth:   false,
		Session:        SessionAny,
		Idempotent:     true,
		SupportsDryRun: true,

		SuppressGlobals: []string{"team"},

		Flags: []Flag{
			agentFlag(),
			projectFlag(),
			{
				Name: "ref", Type: "string",
				Description: "install a particular release of the skill, such as v0.1.0. Defaults to the newest",
			},
		},

		OutputFields: skillsFields(),

		Examples: []Example{
			{
				Title: "see which tools would get it, and which version",
				Argv:  []string{"outplane", "skills", "install", "--dry-run", "--json"},
				Risk:  RiskRead,
			},
			{
				Title: "install it everywhere it is wanted",
				Argv:  []string{"outplane", "skills", "install"},
				Risk:  RiskWrite,
			},
			{
				Title: "install it into this repository, so the team shares it",
				Argv:  []string{"outplane", "skills", "install", "--agent", "claude-code", "--project"},
				Risk:  RiskWrite,
			},
		},

		AutomationNotes: []string{
			"Writes files on this machine and nothing on Out Plane. No credential is used and no team is involved.",
			"A tool is detected by its configuration directory, so a tool that has never been opened is not found. --agent installs into one by name regardless.",
			"The install replaces the skill directory rather than merging into it, so a file removed upstream disappears here too.",
			"The running coding tool does not reload a skill on its own. Tell the user to restart it.",
			"--project writes into the current directory, which means the skill is committed with the repository and everyone working in it gets the same one.",
		},

		ErrorCodes: []string{
			"skills.no_agent_found",
			"skills.agent_unknown",
			"skills.no_project_dir",
			"skills.fetch_failed",
			"skills.write_failed",
		},
		ExitCodes: []int{0, 1, 2, 5, 8},

		Related: []string{"skills list", "skills update", "skills remove"},

		DocsURL: "https://docs.outplane.com/cli/skills",
	}
}

func skillsList() Command {
	return Command{
		Path:  []string{"skills", "list"},
		Short: "show where the agent skill is installed, and which version",
		Long: "Reports every coding tool detected on this machine, whether the Out Plane " +
			"skill is installed for it, and which version is there.\n\n" +
			"Reads local files only. It does not ask the repository what the newest " +
			"version is, so it is safe to run with no network.",

		Risk:         RiskRead,
		RequiresAuth: false,
		Session:      SessionAny,
		Idempotent:   true,

		SuppressGlobals: []string{"team"},

		Flags: []Flag{agentFlag(), projectFlag()},

		OutputFields: []Field{
			{Name: "agent", Type: "string"},
			{Name: "path", Type: "string", Description: "where its skills live"},
			{Name: "installed", Type: "bool"},
			{Name: "version", Type: "string | null", Description: "null when it is not installed"},
		},

		Examples: []Example{
			{
				Title: "what is installed where",
				Argv:  []string{"outplane", "skills", "list"},
				Risk:  RiskRead,
			},
			{
				Title: "read it in a script",
				Argv:  []string{"outplane", "skills", "list", "--json", "--fields", "agent,installed,version"},
				Risk:  RiskRead,
			},
			{
				Title: "just this repository's copy",
				Argv:  []string{"outplane", "skills", "list", "--project", "--json"},
				Risk:  RiskRead,
			},
		},

		AutomationNotes: []string{
			"Local only. It reports what is on disk and never asks the repository what the newest release is, so it cannot say whether an installed skill is out of date.",
			"A tool with no configuration directory on this machine is left out entirely rather than reported as not installed.",
			"The version is read from the installed skill's own frontmatter, so a skill installed by a plugin marketplace is reported the same way as one installed here.",
		},

		ErrorCodes: []string{"skills.no_agent_found", "skills.agent_unknown", "skills.no_project_dir"},
		ExitCodes:  []int{0, 1, 2, 5},

		Related: []string{"skills install", "skills update", "skills remove"},

		DocsURL: "https://docs.outplane.com/cli/skills",
	}
}

func skillsUpdate() Command {
	return Command{
		Path:  []string{"skills", "update"},
		Short: "replace an installed agent skill with the newest release",
		Long: "Fetches the newest release of the skill and writes it wherever one is " +
			"already installed.\n\n" +
			"Unlike install, this touches only the tools that already have the skill: " +
			"updating is not the command that decides a tool should have one.",

		Risk:           RiskWrite,
		RequiresAuth:   false,
		Session:        SessionAny,
		Idempotent:     true,
		SupportsDryRun: true,

		SuppressGlobals: []string{"team"},

		Flags: []Flag{agentFlag(), projectFlag()},

		OutputFields: skillsFields(),

		Examples: []Example{
			{
				Title: "see what would change",
				Argv:  []string{"outplane", "skills", "update", "--dry-run", "--json"},
				Risk:  RiskRead,
			},
			{
				Title: "update every installed copy",
				Argv:  []string{"outplane", "skills", "update"},
				Risk:  RiskWrite,
			},
			{
				Title: "update one tool",
				Argv:  []string{"outplane", "skills", "update", "--agent", "cursor"},
				Risk:  RiskWrite,
			},
		},

		AutomationNotes: []string{
			"Only tools that already have the skill are touched. Use `outplane skills install` to add it somewhere new.",
			"changed is false where the installed version already matched, and the command still exits 0.",
			"The coding tool has to be restarted before it reads the new copy.",
		},

		ErrorCodes: []string{
			"skills.not_installed",
			"skills.agent_unknown",
			"skills.no_project_dir",
			"skills.fetch_failed",
			"skills.write_failed",
		},
		ExitCodes: []int{0, 1, 2, 5, 8},

		Related: []string{"skills install", "skills list", "skills remove"},

		DocsURL: "https://docs.outplane.com/cli/skills",
	}
}

func skillsRemove() Command {
	return Command{
		Path:  []string{"skills", "remove"},
		Short: "remove the Out Plane agent skill from your coding tools",
		Long: "Deletes the skill directory from every tool that has it.\n\n" +
			"Nothing else is touched, and the skill can be installed again at any time, " +
			"so this is not a decision that needs confirming.",

		Risk:           RiskWrite,
		RequiresAuth:   false,
		Session:        SessionAny,
		Idempotent:     true,
		SupportsDryRun: true,

		SuppressGlobals: []string{"team"},

		Flags: []Flag{agentFlag(), projectFlag()},

		OutputFields: []Field{
			{Name: "agent", Type: "string"},
			{Name: "path", Type: "string"},
			{Name: "changed", Type: "bool", Description: "false when there was nothing there, and for a dry run"},
		},

		Examples: []Example{
			{
				Title: "see what would be removed",
				Argv:  []string{"outplane", "skills", "remove", "--dry-run", "--json"},
				Risk:  RiskRead,
			},
			{
				Title: "remove it everywhere",
				Argv:  []string{"outplane", "skills", "remove"},
				Risk:  RiskWrite,
			},
			{
				Title: "remove it from one tool",
				Argv:  []string{"outplane", "skills", "remove", "--agent", "codex"},
				Risk:  RiskWrite,
			},
		},

		AutomationNotes: []string{
			"Removing something that is not there reports changed false and exits 0, so a teardown script can run this unconditionally.",
			"Only the Out Plane skill directory is deleted. Other skills in the same folder are left alone.",
		},

		ErrorCodes: []string{"skills.agent_unknown", "skills.no_project_dir", "skills.write_failed"},
		ExitCodes:  []int{0, 1, 2},

		Related: []string{"skills install", "skills list", "skills update"},

		DocsURL: "https://docs.outplane.com/cli/skills",
	}
}
