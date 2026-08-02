package registry

// Persistent disks.
//
// Platform facts that shape this group:
//
//   - A disk belongs to one application at one path. There is no sharing, no
//     second mount, and no way to move it to a different path on the same
//     application without detaching first.
//   - Attaching and detaching are one endpoint, told apart by whether an
//     application is named. They are two commands here anyway: nobody detaches
//     on purpose by leaving an argument out.
//   - A mount reaches the running application at its next deployment, unlike
//     scaling and pausing which are immediate.
//   - Sizes are 10 to 50 GB and cannot be changed afterwards.

func init() {
	Register(
		volumeList(),
		volumeGet(),
		volumeCreate(),
		volumeAttach(),
		volumeDetach(),
		volumeDelete(),
	)
}

// volumeArg is the same declaration in the four commands that take one.
func volumeArg() Arg {
	return Arg{
		Name:     "volume",
		Short:    "volume name or id",
		Required: true,
		Resolves: "volume",
	}
}

// volumeFields are the fields every volume command reports, so that a caller
// reading one has read them all.
func volumeFields() []Field {
	return []Field{
		{Name: "id", Type: "string"},
		{Name: "name", Type: "string"},
		{Name: "sizeGb", Type: "int", Description: "10 to 50, and fixed once created"},
		{
			Name: "status",
			Type: "string",
			Enum: []string{"attached", "detached"},
		},
		{Name: "app", Type: "string | null", Description: "the application it is mounted on"},
		{Name: "appId", Type: "string | null"},
		{Name: "mountPath", Type: "string | null", Description: "where it appears inside the container"},
		{
			Name:        "accessMode",
			Type:        "string",
			Description: "always ReadWriteOnce: one instance mounts it at a time",
		},
		{Name: "createdAt", Type: "string | null", Description: "RFC 3339, UTC"},
	}
}

func volumeList() Command {
	return Command{
		Path:  []string{"volume", "list"},
		Short: "list the team's disks",
		Long: "Lists every persistent disk in the team, attached or not.\n\n" +
			"Use --app to see only what one application has mounted.",

		Risk:         RiskRead,
		RequiresAuth: true,
		Session:      SessionAny,
		Idempotent:   true,

		APICalls: []string{
			"GET /api/Volume/GetVolumesByTeamId",
			"GET /api/Volume/GetVolumesByAppId/{appId}",
		},

		Flags: []Flag{
			{
				Name:        "app",
				Type:        "string",
				Description: "only the disks mounted on this application",
			},
		},

		OutputFields: volumeFields(),

		ErrorCodes: []string{"app.not_found", "context.no_team"},
		ExitCodes:  []int{0, 2, 3, 5, 8},

		Examples: []Example{
			{
				Title:   "every disk in the team",
				Command: "outplane volume list",
				Argv:    []string{"outplane", "volume", "list"},
				Risk:    RiskRead,
			},
			{
				Title:        "what one application has mounted",
				Command:      "outplane volume list --app checkout",
				Argv:         []string{"outplane", "volume", "list", "--app", "checkout"},
				Placeholders: map[string]string{"checkout": "<APP_NAME>"},
				Risk:         RiskRead,
			},
			{
				Title:   "find the id to attach",
				Command: "outplane volume list --json --fields name,id,status",
				Argv:    []string{"outplane", "volume", "list", "--json", "--fields", "name,id,status"},
				Risk:    RiskRead,
				OutputSample: map[string]any{
					"items": []any{
						map[string]any{"name": "data", "id": "3f2b1c4e-…", "status": "detached"},
					},
					"total":     1,
					"truncated": false,
				},
			},
		},

		AutomationNotes: []string{
			"A detached disk still exists and still costs storage. Detaching is not deleting.",
			"Names are not enforced unique. Every command here takes an id as well, and the " +
				"id is what to store.",
		},

		Related: []string{"volume get", "volume attach", "app create", "app get"},
		DocsURL: "https://docs.outplane.com/cli/volume",
	}
}

func volumeGet() Command {
	return Command{
		Path:  []string{"volume", "get"},
		Short: "show one disk",
		Long:  "Reports one disk: its size, whether it is mounted, and where.",

		Risk:         RiskRead,
		RequiresAuth: true,
		Session:      SessionAny,
		Idempotent:   true,

		APICalls: []string{"GET /api/Volume/GetVolumesByTeamId"},

		Args:         []Arg{volumeArg()},
		OutputFields: volumeFields(),

		ErrorCodes: []string{"volume.not_found", "volume.ambiguous", "usage.missing_argument"},
		ExitCodes:  []int{0, 2, 3, 5, 8},

		Examples: []Example{
			{
				Title:        "show a disk",
				Command:      "outplane volume get data",
				Argv:         []string{"outplane", "volume", "get", "data"},
				Placeholders: map[string]string{"data": "<VOLUME_NAME>"},
				Risk:         RiskRead,
			},
		},

		AutomationNotes: []string{
			"There is no per-volume endpoint that takes a name, so this lists the team's " +
				"disks and matches. The cost is the same as `volume list`.",
			"Matching is exact, on the id first and then the name. Two disks with the same " +
				"name are reported as ambiguous rather than resolved to one of them.",
		},

		Related: []string{"volume list", "volume attach", "volume detach"},
		DocsURL: "https://docs.outplane.com/cli/volume",
	}
}

func volumeCreate() Command {
	return Command{
		Path:  []string{"volume", "create"},
		Short: "create a disk, and optionally mount it",
		Long: "Creates a persistent disk.\n\n" +
			"Give --app and --mount together to attach it in the same step. Without them " +
			"the disk is created detached, which is what you want when the application " +
			"that will use it does not exist yet.\n\n" +
			"The size is fixed once created, between 10 and 50 GB.",

		Risk:         RiskWrite,
		RequiresAuth: true,
		Session:      SessionAny,
		Idempotent:   false,

		SupportsDryRun: true,

		APICalls: []string{"POST /api/Volume/CreateVolume"},

		Args: []Arg{
			{
				Name:     "name",
				Short:    "letters, numbers, hyphens and underscores",
				Required: true,
				Pattern:  "^[a-zA-Z0-9][a-zA-Z0-9\\-_]*$",
			},
		},

		Flags: []Flag{
			{Name: "size", Type: "int", Description: "gigabytes, 10 to 50. Required"},
			{Name: "app", Type: "string", Description: "application to mount it on. Needs --mount"},
			{Name: "mount", Type: "string", Description: "path inside the container, such as /data"},
		},

		OutputFields: append(volumeFields(),
			Field{Name: "changed", Type: "bool", Description: "false for a dry run"}),

		ErrorCodes: []string{
			"volume.name_required",
			"volume.name_invalid",
			"volume.size_invalid",
			"volume.mount_invalid",
			"volume.attach_incomplete",
			"usage.bad_size",
			"quota.limit_reached",
			"app.not_found",
		},
		ExitCodes: []int{0, 2, 3, 5, 7, 8},

		Examples: []Example{
			{
				Title:        "a disk to attach later",
				Command:      "outplane volume create data --size 10",
				Argv:         []string{"outplane", "volume", "create", "data", "--size", "10"},
				Placeholders: map[string]string{"data": "<VOLUME_NAME>"},
				Risk:         RiskWrite,
			},
			{
				Title:        "create and mount in one step",
				Command:      "outplane volume create data --size 20 --app checkout --mount /var/lib/data",
				Argv:         []string{"outplane", "volume", "create", "data", "--size", "20", "--app", "checkout", "--mount", "/var/lib/data"},
				Placeholders: map[string]string{"data": "<VOLUME_NAME>", "checkout": "<APP_NAME>"},
				Risk:         RiskWrite,
			},
		},

		AutomationNotes: []string{
			"Creating and attaching are one request and two steps on the server: the disk is " +
				"created first and then mounted. If the mount fails the disk exists and is " +
				"detached, so attach it rather than running this again, which would make a " +
				"second disk.",
			"--app and --mount go together. One without the other is refused rather than " +
				"half-applied.",
			"The size cannot be changed afterwards. There is no resize.",
			"A mount reaches the running application at its next deployment.",
			"Team storage is a plan limit, so a large enough disk is exit 7 rather than a " +
				"validation error.",
		},

		Related: []string{"volume attach", "volume list", "app create"},
		DocsURL: "https://docs.outplane.com/cli/volume",
	}
}

func volumeAttach() Command {
	return Command{
		Path:  []string{"volume", "attach"},
		Short: "mount a disk on an application",
		Long: "Mounts an existing disk on an application at a path.\n\n" +
			"A disk belongs to one application at one path. To move it, detach it first: " +
			"the platform refuses to change the path underneath a running application.",

		Risk:         RiskWrite,
		RequiresAuth: true,
		Session:      SessionAny,
		Idempotent:   true,

		SupportsDryRun: true,

		APICalls: []string{"PUT /api/Volume/UpdateVolume/{volumeId}"},

		Args: []Arg{volumeArg()},

		Flags: []Flag{
			{Name: "app", Type: "string", Description: "application to mount it on. Required"},
			{Name: "mount", Type: "string", Description: "path inside the container. Required"},
		},

		OutputFields: append(volumeFields(),
			Field{Name: "changed", Type: "bool"}),

		ErrorCodes: []string{
			"volume.not_found",
			"volume.attach_incomplete",
			"volume.mount_invalid",
			"app.not_found",
			"usage.missing_argument",
		},
		ExitCodes: []int{0, 2, 3, 5, 8},

		Examples: []Example{
			{
				Title:        "mount a disk",
				Command:      "outplane volume attach data --app checkout --mount /var/lib/data",
				Argv:         []string{"outplane", "volume", "attach", "data", "--app", "checkout", "--mount", "/var/lib/data"},
				Placeholders: map[string]string{"data": "<VOLUME_NAME>", "checkout": "<APP_NAME>"},
				Risk:         RiskWrite,
			},
		},

		AutomationNotes: []string{
			"The mount reaches the running application at its next deployment, not " +
				"immediately. Scaling and pausing are the immediate ones.",
			"Moving a disk to a different path on the same application is refused. Detach and " +
				"attach again, which is the platform making the data's owner say so twice.",
			"A path already used by another disk on the same application is refused.",
			"The disk and the application have to be in the same team.",
		},

		Related: []string{"volume detach", "volume create", "volume list"},
		DocsURL: "https://docs.outplane.com/cli/volume",
	}
}

func volumeDetach() Command {
	return Command{
		Path:  []string{"volume", "detach"},
		Short: "unmount a disk, keeping it and its contents",
		Long: "Unmounts a disk from whatever application has it.\n\n" +
			"The disk and everything on it are kept. This is what has to happen before an " +
			"application can be deleted, and before a disk can be mounted somewhere else.",

		Risk:         RiskWrite,
		RequiresAuth: true,
		Session:      SessionAny,
		Idempotent:   true,

		SupportsDryRun: true,

		APICalls: []string{"PUT /api/Volume/UpdateVolume/{volumeId}"},

		Args:         []Arg{volumeArg()},
		OutputFields: append(volumeFields(), Field{Name: "changed", Type: "bool"}),

		ErrorCodes: []string{"volume.not_found", "usage.missing_argument"},
		ExitCodes:  []int{0, 2, 3, 5, 8},

		Examples: []Example{
			{
				Title:        "unmount a disk",
				Command:      "outplane volume detach data",
				Argv:         []string{"outplane", "volume", "detach", "data"},
				Placeholders: map[string]string{"data": "<VOLUME_NAME>"},
				Risk:         RiskWrite,
			},
		},

		AutomationNotes: []string{
			"Detaching is not deleting. The disk keeps its contents and its cost, and can be " +
				"mounted again.",
			"A disk that is not attached reports changed false and exits 0.",
			"The application loses the mount at its next deployment.",
		},

		Related: []string{"volume attach", "volume delete", "app delete"},
		DocsURL: "https://docs.outplane.com/cli/volume",
	}
}

func volumeDelete() Command {
	return Command{
		Path:  []string{"volume", "delete"},
		Short: "permanently destroy a disk and its contents",
		Long: "Deletes a disk.\n\n" +
			"Everything on it is destroyed and nothing restores it. There is no snapshot, " +
			"no retention window and no undelete.\n\n" +
			"An application can be recreated from its source; the data on a disk cannot be " +
			"recreated from anything, which is why this asks twice.",

		Risk:         RiskDestructive,
		RequiresAuth: true,
		Session:      SessionAny,
		Idempotent:   false,

		SupportsDryRun: true,

		APICalls: []string{"DELETE /api/Volume/DeleteVolume/{volumeId}"},

		Args: []Arg{volumeArg()},

		Flags: []Flag{
			{
				Name: "yes", Short: "y", Type: "bool", Default: "false",
				Description: "acknowledge the deletion. Not sufficient on its own",
			},
			{
				Name: "confirm-name", Type: "string",
				Description: "the disk's name, typed again",
			},
		},

		OutputFields: append(volumeFields(),
			Field{Name: "changed", Type: "bool", Description: "true only when it was destroyed"}),

		ErrorCodes: []string{
			"confirmation.required",
			"volume.confirm_name_mismatch",
			"volume.not_found",
		},
		ExitCodes: []int{0, 2, 3, 4, 5, 8},

		Examples: []Example{
			{
				Title:        "see what would be destroyed",
				Command:      "outplane volume delete data --dry-run",
				Argv:         []string{"outplane", "volume", "delete", "data", "--dry-run"},
				Placeholders: map[string]string{"data": "<VOLUME_NAME>"},
				Risk:         RiskRead,
			},
			{
				Title:        "the confirmed form, which a human approves",
				Command:      "outplane volume delete data --yes --confirm-name data",
				Argv:         []string{"outplane", "volume", "delete", "data", "--yes", "--confirm-name", "data"},
				Placeholders: map[string]string{"data": "<VOLUME_NAME>"},
				Risk:         RiskDestructive,
			},
		},

		AutomationNotes: []string{
			"This command never prompts. Without confirmation it exits 4 and returns the " +
				"command to replay in the error's confirm_command field.",
			"Under a detected agent harness it exits 4 even with both flags, so the approval " +
				"stays with whoever is accountable for the data.",
			"An attached disk cannot be deleted. Detach it first, which is the platform " +
				"making sure the application is not using it.",
			"Irreversible. There is no snapshot and no retention window.",
		},

		Related: []string{"volume detach", "volume list", "app delete"},
		DocsURL: "https://docs.outplane.com/cli/volume",
	}
}
