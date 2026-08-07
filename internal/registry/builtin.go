package registry

// The four commands that answer questions about the CLI itself.
//
// They are declared here and built in cmd/outplane/main.go, which is the
// exception RootWired exists for: each of them has to work before a config file
// is read and before a credential is resolved, so a machine with no
// installation at all can still ask what this binary is and what it can do.
//
// Declaring them is not decoration. `outplane schema` is the door an agent walks
// through to learn the surface, and until now that door was not listed on the
// map it prints. The same was true of `outplane help exit-codes`, which is the
// only place the exit-code contract is written down.

func init() {
	Register(
		schemaCommand(),
		helpCommand(),
		versionCommand(),
		completionCommand(),
	)
}

func schemaCommand() Command {
	return Command{
		Path:  []string{"schema"},
		Short: "print the machine-readable description of every command",
		Long: "Prints the whole command surface as one JSON document: every command with " +
			"its arguments, flags, output fields, exit codes, error codes and runnable " +
			"examples.\n\n" +
			"Give it a command path to print one command instead of all of them. That is " +
			"the form to reach for in a tool: the whole document is large, and a single " +
			"command carries the same global flags, error kinds and exit codes alongside it.\n\n" +
			"Reads nothing and sends nothing. It describes the binary you are holding, so " +
			"the answer is true for that binary and may differ from a newer one.",

		Risk:         RiskRead,
		RequiresAuth: false,
		Session:      SessionAny,
		Idempotent:   true,
		RootWired:    true,

		Args: []Arg{
			{
				Name:  "command",
				Short: "command path, such as `deploy create`. Omit for the whole surface",
			},
		},

		OutputFields: []Field{
			{Name: "schemaVersion", Type: "string", Description: "the shape of this document, not the CLI version"},
			{Name: "version", Type: "string", Description: "the CLI that produced it, or unstamped for a local build"},
			{Name: "commands", Type: "array", Description: "one object per command"},
			{Name: "globalArgs", Type: "array", Description: "flags every command accepts"},
			{Name: "errorKinds", Type: "array", Description: "error kind, its exit code, and whether a retry can help"},
			{Name: "riskLevels", Type: "array", Description: "what read, write and destructive mean here"},
		},

		ExitCodes:  []int{0, 2, 5},
		ErrorCodes: []string{"usage.unknown_command"},

		Examples: []Example{
			{
				Title:   "read the whole surface",
				Command: "outplane schema",
				Argv:    []string{"outplane", "schema"},
				Risk:    RiskRead,
			},
			{
				Title:   "read one command instead of all of them",
				Command: "outplane schema deploy create",
				Argv:    []string{"outplane", "schema", "deploy", "create"},
				Risk:    RiskRead,
			},
			{
				Title:   "list the commands that only read",
				Command: `outplane schema --json --fields commands | jq -r '.commands[] | select(.risk == "read") | .name'`,
				Argv:    []string{"outplane", "schema", "--json", "--fields", "commands"},
				Risk:    RiskRead,
			},
		},

		AutomationNotes: []string{
			"Output is JSON whether or not a terminal is attached, because there is no " +
				"human rendering of this document.",
			"version reads `unstamped` on a build that was not made from a release tag. " +
				"Treat that as unknown rather than as old.",
			"A single command still carries globalArgs, errorKinds and riskLevels, so one " +
				"fetch is enough to know how to call it and how to read its failure.",
		},

		Related: []string{"help", "version"},
		DocsURL: "https://docs.outplane.com/cli/schema",
	}
}

func helpCommand() Command {
	return Command{
		Path:  []string{"help"},
		Short: "explain a topic that is not a command",
		Long: "Prints reference material that belongs to the CLI as a whole rather than to " +
			"any one command: the exit codes, the error envelope, and how output formats " +
			"are chosen.\n\n" +
			"`outplane help exit-codes` is the one to know. It is the only place the exit " +
			"code contract is written out, and branching on an exit code is how a script " +
			"tells a missing resource from a refused action from a network failure.",

		Risk:         RiskRead,
		RequiresAuth: false,
		Session:      SessionAny,
		Idempotent:   true,
		RootWired:    true,

		Args: []Arg{
			{
				Name:  "topic",
				Short: "which topic to print. Omit to list the topics",
			},
		},

		ExitCodes:  []int{0, 2},
		ErrorCodes: []string{"usage.unknown_topic"},

		Examples: []Example{
			{
				Title:   "list the topics",
				Command: "outplane help",
				Argv:    []string{"outplane", "help"},
				Risk:    RiskRead,
			},
			{
				Title:   "read the exit code contract",
				Command: "outplane help exit-codes",
				Argv:    []string{"outplane", "help", "exit-codes"},
				Risk:    RiskRead,
			},
			{
				Title:   "read the shape of a failure",
				Command: "outplane help errors",
				Argv:    []string{"outplane", "help", "errors"},
				Risk:    RiskRead,
			},
		},

		AutomationNotes: []string{
			"These topics are prose for a person. The same facts are in `outplane schema` " +
				"as data, under errorKinds and each command's exitCodes, and that is what a " +
				"program should read.",
		},

		Related: []string{"schema"},
		DocsURL: "https://docs.outplane.com/cli/help",
	}
}

func versionCommand() Command {
	return Command{
		Path:  []string{"version"},
		Short: "print the version of this CLI",
		Long: "Prints the version this binary was built from.\n\n" +
			"A build that did not come from a release tag reports `unstamped`, which means " +
			"unknown rather than old. Use `outplane update` to install the newest release.",

		Risk:         RiskRead,
		RequiresAuth: false,
		Session:      SessionAny,
		Idempotent:   true,
		RootWired:    true,

		OutputFields: []Field{
			{Name: "version", Type: "string", Description: "semantic version, or unstamped"},
		},

		ExitCodes: []int{0},

		Examples: []Example{
			{
				Title:   "print the version",
				Command: "outplane version",
				Argv:    []string{"outplane", "version"},
				Risk:    RiskRead,
			},
			{
				Title:   "read it in a script",
				Command: "outplane version --json --fields version",
				Argv:    []string{"outplane", "version", "--json", "--fields", "version"},
				Risk:    RiskRead,
				OutputSample: map[string]any{
					"version": "0.2.8",
				},
			},
			{
				Title:   "check before assuming a flag exists",
				Command: "outplane version --json --fields version",
				Argv:    []string{"outplane", "version", "--json", "--fields", "version"},
				Risk:    RiskRead,
			},
		},

		AutomationNotes: []string{
			"The API answers a request from a CLI older than the minimum it serves with " +
				"exit code 9, so a version check is not required before every call. Read " +
				"this when a failure needs explaining, not as a gate.",
		},

		Related: []string{"update", "schema"},
		DocsURL: "https://docs.outplane.com/cli/version",
	}
}

func completionCommand() Command {
	return Command{
		Path:  []string{"completion"},
		Short: "print a shell completion script",
		Long: "Prints a completion script for bash, zsh, fish or PowerShell on standard " +
			"output. Source it from your shell's startup file to complete command names " +
			"and flags.\n\n" +
			"It writes to standard output rather than installing anything, so where the " +
			"script ends up is your decision.",

		Risk:         RiskRead,
		RequiresAuth: false,
		Session:      SessionAny,
		Idempotent:   true,
		RootWired:    true,

		Args: []Arg{
			{
				Name:     "shell",
				Short:    "which shell to generate for (bash | zsh | fish | powershell)",
				Required: true,
			},
		},

		ExitCodes: []int{0, 2},

		Examples: []Example{
			{
				Title:   "print the zsh script",
				Command: "outplane completion zsh",
				Argv:    []string{"outplane", "completion", "zsh"},
				Risk:    RiskRead,
			},
			{
				Title:   "install it for zsh",
				Command: "outplane completion zsh > \"${fpath[1]}/_outplane\"",
				Argv:    []string{"outplane", "completion", "zsh"},
				Risk:    RiskRead,
			},
			{
				Title:   "print the bash script",
				Command: "outplane completion bash",
				Argv:    []string{"outplane", "completion", "bash"},
				Risk:    RiskRead,
			},
		},

		AutomationNotes: []string{
			"Completion is for a person at a prompt. A program should read `outplane schema` " +
				"instead, which is the same information as data.",
		},

		Related: []string{"schema"},
		DocsURL: "https://docs.outplane.com/cli/completion",
	}
}
