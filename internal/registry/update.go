package registry

// update is the other half of the version floor the API enforces.
//
// When the API refuses a release it no longer serves, the error points here.
// That makes this command load-bearing rather than a convenience: an error
// telling somebody to run a command that does not exist is worse than no
// suggestion at all.
//
// It runs a package manager rather than replacing the binary itself. An npm
// installation is owned by npm, and a binary swapped underneath it is undone by
// the next install; the shell installation is upgraded by re-running the script
// that produced it. Neither needs this CLI to rewrite its own file while running
// from it.

func init() {
	Register(update())
}

func update() Command {
	return Command{
		Path:  []string{"update"},
		Short: "install the newest version of this CLI",
		Long: "Updates the CLI using whichever channel installed it.\n\n" +
			"The installation is identified from the path of the running binary: a " +
			"binary inside node_modules belongs to npm, and anything else came from " +
			"the install script. Run with --check to see what would happen before " +
			"anything does.\n\n" +
			"This replaces the CLI on this machine, not anything on Out Plane.",

		Risk: RiskWrite,
		// Updating is about this machine, so no credential is involved. It has
		// to work when the token has expired, and after the API has started
		// refusing this release outright.
		RequiresAuth: false,
		Session:      SessionAny,
		// Running it twice on the newest version changes nothing.
		Idempotent: true,

		Flags: []Flag{
			{
				Name:        "check",
				Type:        "bool",
				Default:     "false",
				Description: "report the install method and the command, without running it",
			},
		},

		// The team has nothing to do with which binary is on this machine.
		SuppressGlobals: []string{"team"},

		OutputFields: []Field{
			{Name: "method", Type: "string", Description: "npm | the install script | unknown"},
			{Name: "path", Type: "string", Description: "the binary that would be replaced"},
			{Name: "version", Type: "string", Description: "the version running now"},
			{
				Name:        "command",
				Type:        "string | null",
				Description: "what updates this installation, null when it is not managed",
			},
			{Name: "ran", Type: "bool", Description: "false with --check, and whenever nothing was run"},
		},

		ErrorCodes: []string{"update.unmanaged", "update.failed"},
		ExitCodes:  []int{0, 1, 2},

		Examples: []Example{
			{
				Title:   "see how this CLI would update itself",
				Command: "outplane update --check",
				Argv:    []string{"outplane", "update", "--check"},
				Risk:    RiskRead,
				OutputSample: map[string]any{
					"method":  "npm",
					"command": "npm install -g outplane@latest",
					"ran":     false,
				},
			},
			{
				Title:   "update now",
				Command: "outplane update",
				Argv:    []string{"outplane", "update"},
				Risk:    RiskWrite,
			},
		},

		AutomationNotes: []string{
			"--check never runs anything and makes no change, whatever the environment.",
			"An installation that is not managed by npm or the install script exits 2 with " +
				"update.unmanaged. Whatever put the binary there is what should replace it.",
			"Exit code 9 from any other command means the API has stopped serving this " +
				"release, and this is the command that fixes it.",
			"The new version is not loaded by the running process. Later commands use it; " +
				"this one finishes as the version it started as.",
		},

		Related: []string{"version", "status"},
		DocsURL: "https://docs.outplane.com/cli/update",
	}
}
