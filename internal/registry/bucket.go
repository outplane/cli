package registry

// S3 compatible buckets.
//
// Platform facts that shape this group:
//
//   - A bucket has two names. The short one is what these commands take. Clients
//     use bucketName, which carries a suffix the platform adds.
//   - Clients need virtual-hosted addressing, with the bucket name in the host.
//     Path-style requests fail, because no address answers for them.
//   - A new bucket's address, a change of visibility and a deletion all take
//     minutes to spread, so a success here says the request was accepted.
//   - A new bucket has no key. The keys are in bucketkey.go.
//
// There is no bucket.ambiguous code: a name is unique in a team.

func init() {
	Register(
		bucketList(),
		bucketGet(),
		bucketCreate(),
		bucketUpdate(),
		bucketDelete(),
	)
}

// bucketArg is the same declaration in every command that takes one.
func bucketArg() Arg {
	return Arg{
		Name:     "bucket",
		Short:    "the bucket's name or id, not its bucketName",
		Required: true,
		Resolves: "bucket",
	}
}

// bucketFields are the fields every bucket command reports, so that a caller
// reading one has read them all.
func bucketFields() []Field {
	return []Field{
		{Name: "id", Type: "string"},
		{Name: "name", Type: "string", Description: "what the platform calls it, unique in the team"},
		{
			Name: "bucketName", Type: "string",
			Description: "the name S3 clients use. It carries a suffix, so it differs from name",
		},
		{
			Name: "public", Type: "bool",
			Description: "the setting last accepted: whether anyone with a file's link can read it. A change " +
				"takes minutes to reach every request. Listing stays off either way",
		},
		{
			Name: "endpoint", Type: "string",
			Description: "the S3 endpoint. Clients need virtual-hosted addressing; path-style requests fail",
		},
		{Name: "region", Type: "string", Description: "the region clients sign with, always auto"},
		{
			Name: "url", Type: "string",
			Description: "the bucket's own address. It serves a file without a signature only when the bucket " +
				"is public, and after a change to private it can keep doing so for some minutes",
		},
		{Name: "createdAt", Type: "string | null", Description: "RFC 3339, UTC"},
	}
}

func bucketList() Command {
	return Command{
		Path:  []string{"bucket", "list"},
		Short: "list the team's buckets",
		Long: "Lists every S3 compatible bucket in the team, public or private.\n\n" +
			"`outplane bucket get` reports what a client needs to reach one.",

		Risk:         RiskRead,
		RequiresAuth: true,
		Session:      SessionAny,
		Idempotent:   true,

		APICalls: []string{"GET /api/Bucket/GetBucketsByTeamId"},

		OutputFields: bucketFields(),

		ErrorCodes: []string{"context.no_team"},
		ExitCodes:  []int{0, 2, 3, 8},

		Examples: []Example{
			{
				Title:   "every bucket in the team",
				Command: "outplane bucket list",
				Argv:    []string{"outplane", "bucket", "list"},
				Risk:    RiskRead,
			},
			{
				Title:   "read the names clients use, in a script",
				Command: "outplane bucket list --json --fields name,bucketName,public",
				Argv:    []string{"outplane", "bucket", "list", "--json", "--fields", "name,bucketName,public"},
				Risk:    RiskRead,
				OutputSample: map[string]any{
					"items": []any{
						map[string]any{"name": "uploads", "bucketName": "uploads-<SUFFIX>", "public": false},
					},
					"total":     1,
					"truncated": false,
				},
			},
			{
				Title:   "the same list as a table, in a pipe",
				Command: "outplane bucket list -o text",
				Argv:    []string{"outplane", "bucket", "list", "-o", "text"},
				Risk:    RiskRead,
			},
		},

		AutomationNotes: []string{
			"An empty list is not an error. A team starts with no buckets.",
			"bucketName, not name, is what goes in an S3 client.",
			"A bucket whose create timed out appears here if the create finished.",
		},

		Related: []string{"bucket get", "bucket create", "volume list"},
		DocsURL: "https://docs.outplane.com/cli/bucket",
	}
}

func bucketGet() Command {
	return Command{
		Path:  []string{"bucket", "get"},
		Short: "show one bucket and how clients reach it",
		Long: "Reports one bucket: whether it is public, and the name, endpoint and region an " +
			"S3 client needs.\n\n" +
			"Clients use bucketName, not the short name, and they need virtual-hosted " +
			"addressing. Path-style requests fail.",

		Risk:         RiskRead,
		RequiresAuth: true,
		Session:      SessionAny,
		Idempotent:   true,

		APICalls: []string{"GET /api/Bucket/GetBucketsByTeamId"},

		Args:         []Arg{bucketArg()},
		OutputFields: bucketFields(),

		ErrorCodes: []string{
			"bucket.not_found",
			"usage.missing_argument",
			"usage.empty_argument",
			"context.no_team",
		},
		ExitCodes: []int{0, 2, 3, 5, 8},

		Examples: []Example{
			{
				Title:        "show a bucket",
				Command:      "outplane bucket get uploads",
				Argv:         []string{"outplane", "bucket", "get", "uploads"},
				Placeholders: map[string]string{"uploads": "<BUCKET_NAME>"},
				Risk:         RiskRead,
			},
			{
				Title:        "read what a client needs, in a script",
				Command:      "outplane bucket get uploads --json --fields bucketName,endpoint,region",
				Argv:         []string{"outplane", "bucket", "get", "uploads", "--json", "--fields", "bucketName,endpoint,region"},
				Placeholders: map[string]string{"uploads": "<BUCKET_NAME>"},
				Risk:         RiskRead,
				OutputSample: map[string]any{
					"bucketName": "uploads-<SUFFIX>", "endpoint": "<ENDPOINT>", "region": "auto",
				},
			},
			{
				Title:        "see the whole record in a pipe",
				Command:      "outplane bucket get uploads -o text",
				Argv:         []string{"outplane", "bucket", "get", "uploads", "-o", "text"},
				Placeholders: map[string]string{"uploads": "<BUCKET_NAME>"},
				Risk:         RiskRead,
			},
		},

		AutomationNotes: []string{
			"There is no endpoint that takes a name, so this lists the team's buckets and " +
				"matches. The cost is the same as `bucket list`.",
			"Matching is exact, on the id first and then the name.",
			"Clients use bucketName, not name, and the region auto.",
			"Clients need virtual-hosted addressing, with bucketName in the host. Path-style " +
				"requests fail.",
			"url serves a file without a signature only when public is true. public is the setting " +
				"last accepted, so after a change to private, url can keep serving files without a " +
				"signature for some minutes.",
			"A new bucket's address can take a few minutes to start working, for S3 clients as " +
				"well as public links. A TLS error right after create means it is not ready yet.",
		},

		Related: []string{"bucket list", "bucket update", "bucket key list"},
		DocsURL: "https://docs.outplane.com/cli/bucket",
	}
}

func bucketCreate() Command {
	return Command{
		Path:  []string{"bucket", "create"},
		Short: "create a bucket, private unless told otherwise",
		Long: "Creates an S3 compatible bucket.\n\n" +
			"It is private by default: only requests signed with an access key can read its " +
			"files. With --public, anyone with a file's link can read it, and listing stays " +
			"off.\n\n" +
			"A new bucket has no key. `outplane bucket key create` makes one.",

		Risk:         RiskWrite,
		RequiresAuth: true,
		Session:      SessionAny,

		// A second run with the same name is refused rather than being a no-op.
		Idempotent: false,

		SupportsDryRun: true,

		APICalls: []string{"POST /api/Bucket/CreateBucket"},

		Args: []Arg{
			{
				Name:     "name",
				Short:    "3 to 38 lowercase letters, numbers and single hyphens",
				Required: true,
				Pattern:  "^[a-z0-9][a-z0-9-]{1,36}[a-z0-9]$",
			},
		},

		Flags: []Flag{
			{
				Name: "public", Type: "bool", Default: "false",
				Description: "let anyone with a file's link read it. Private without this",
			},
		},

		OutputFields: append(bucketFields(),
			Field{
				Name: "changed", Type: "bool",
				Description: "false for a dry run, where what only the server decides is null",
			}),

		ErrorCodes: []string{
			"bucket.name_invalid",
			"bucket.name_taken",
			"bucket.limit_reached",
			"bucket.subscription_inactive",
			"usage.missing_argument",
			"usage.empty_argument",
			"context.no_team",
		},
		ExitCodes: []int{0, 2, 3, 5, 6, 7, 8, 124},

		Examples: []Example{
			{
				Title:        "a private bucket",
				Command:      "outplane bucket create uploads",
				Argv:         []string{"outplane", "bucket", "create", "uploads"},
				Placeholders: map[string]string{"uploads": "<BUCKET_NAME>"},
				Risk:         RiskWrite,
			},
			{
				Title:        "a bucket whose files are served by link",
				Command:      "outplane bucket create assets --public",
				Argv:         []string{"outplane", "bucket", "create", "assets", "--public"},
				Placeholders: map[string]string{"assets": "<BUCKET_NAME>"},
				Risk:         RiskWrite,
			},
			{
				Title:        "check the request without creating anything",
				Command:      "outplane bucket create uploads --dry-run --json",
				Argv:         []string{"outplane", "bucket", "create", "uploads", "--dry-run", "--json"},
				Placeholders: map[string]string{"uploads": "<BUCKET_NAME>"},
				Risk:         RiskRead,
				OutputSample: map[string]any{"name": "uploads", "public": false, "bucketName": nil, "changed": false},
			},
		},

		AutomationNotes: []string{
			"A bucket is private unless --public is given.",
			"A new bucket's address can take a few minutes to start working, for S3 clients as " +
				"well as public links. A TLS error right after create means it is not ready yet.",
			"A new bucket has no key, and clients need one. `bucket key create` makes one and prints " +
				"its secret on stdout, which makes it a command for a person at their own terminal when " +
				"an agent is driving.",
			"A name taken in the team is exit 6, bucket.name_taken.",
			"The number of buckets a team can have is fixed, the same on every plan. Reaching it " +
				"is exit 7, bucket.limit_reached, and only deleting a bucket makes room.",
			"A team without an active subscription gets exit 7, bucket.subscription_inactive. " +
				"Billing is in the console.",
			"A timeout can still finish on the server, and `bucket list` says whether it did. " +
				"Running this again after one that finished is exit 6.",
		},

		Related: []string{"bucket get", "bucket list", "bucket key create"},
		DocsURL: "https://docs.outplane.com/cli/bucket",
	}
}

// bucketUpdate writes one setting alone, the same shape as `hangar update`.
func bucketUpdate() Command {
	return Command{
		Path:  []string{"bucket", "update"},
		Short: "make a bucket public or private",
		Long: "Changes who can read a bucket's files.\n\n" +
			"Public means anyone with a file's link can read it, and listing stays off. Private " +
			"means only requests signed with an access key can read its files.\n\n" +
			"Changes can take a few minutes to apply. Until then, some requests can see the old " +
			"setting.",

		Risk:         RiskWrite,
		RequiresAuth: true,
		Session:      SessionAny,
		Idempotent:   true,

		SupportsDryRun: true,

		APICalls: []string{
			"GET /api/Bucket/GetBucketsByTeamId",
			"PUT /api/Bucket/UpdateBucket/{bucketId}",
		},

		Args: []Arg{bucketArg()},

		Flags: []Flag{
			{
				Name: "public", Type: "bool", Default: "false",
				Description: "let anyone with a file's link read it. Not with --private",
			},
			{
				Name: "private", Type: "bool", Default: "false",
				Description: "let only requests signed with an access key read it. Not with --public",
			},
		},

		OutputFields: append(bucketFields(),
			Field{
				Name: "changed", Type: "bool",
				Description: "true when the request was accepted, which is not the same as having taken effect",
			}),

		ErrorCodes: []string{
			"usage.no_change",
			"usage.conflicting_flags",
			"bucket.not_found",
			"bucket.subscription_inactive",
			"usage.missing_argument",
			"usage.empty_argument",
			"context.no_team",
		},
		ExitCodes: []int{0, 2, 3, 5, 6, 7, 8},

		Examples: []Example{
			{
				Title:        "stop serving a bucket's files by link",
				Command:      "outplane bucket update uploads --private",
				Argv:         []string{"outplane", "bucket", "update", "uploads", "--private"},
				Placeholders: map[string]string{"uploads": "<BUCKET_NAME>"},
				Risk:         RiskWrite,
			},
			{
				Title:        "serve a bucket's files by link",
				Command:      "outplane bucket update uploads --public",
				Argv:         []string{"outplane", "bucket", "update", "uploads", "--public"},
				Placeholders: map[string]string{"uploads": "<BUCKET_NAME>"},
				Risk:         RiskWrite,
			},
			{
				Title:        "check the change without making it",
				Command:      "outplane bucket update uploads --public --dry-run --json",
				Argv:         []string{"outplane", "bucket", "update", "uploads", "--public", "--dry-run", "--json"},
				Placeholders: map[string]string{"uploads": "<BUCKET_NAME>"},
				Risk:         RiskRead,
				OutputSample: map[string]any{"name": "uploads", "public": true, "changed": false},
			},
		},

		AutomationNotes: []string{
			"Going public makes every existing file readable by its link. Listing stays off.",
			"--public has no confirmation and no gate under an agent harness. It is for when the " +
				"user asked for a public bucket.",
			"The change takes minutes to reach every request, and some requests see the old " +
				"setting until then. A connection already open can keep the old behaviour longer.",
			"changed true means the request was accepted, not that it has taken effect.",
			"The same value sends nothing and exits 0 with changed false. public is the setting last " +
				"accepted, so a change made minutes before can still be reaching some requests.",
			"No flag, or a flag set to false with nothing else, exits 2 with usage.no_change. " +
				"Both flags together exit 2 with usage.conflicting_flags.",
			"Going public needs an active subscription and can exit 7. Going private never does.",
		},

		Related: []string{"bucket get", "bucket list", "bucket key list"},
		DocsURL: "https://docs.outplane.com/cli/bucket",
	}
}

func bucketDelete() Command {
	return Command{
		Path:  []string{"bucket", "delete"},
		Short: "permanently destroy a bucket, its files and its keys",
		Long: "Deletes a bucket.\n\n" +
			"Every file in it and every key on it go too, and nothing restores them. The bucket " +
			"leaves the list at once. Its files and keys can keep answering for some minutes.\n\n" +
			"Applications holding a key are not updated, so they start failing once it stops " +
			"working.",

		Risk:         RiskDestructive,
		RequiresAuth: true,
		Session:      SessionAny,
		Idempotent:   false,

		SupportsDryRun: true,

		APICalls: []string{
			"GET /api/Bucket/GetBucketsByTeamId",
			"DELETE /api/Bucket/DeleteBucket/{bucketId}",
		},

		Args: []Arg{bucketArg()},

		Flags: []Flag{
			{
				Name: "yes", Short: "y", Type: "bool", Default: "false",
				Description: "acknowledge the deletion. Not sufficient on its own",
			},
			{
				Name: "confirm-name", Type: "string",
				Description: "the bucket's name, typed again",
			},
		},

		OutputFields: append(bucketFields(),
			Field{Name: "changed", Type: "bool", Description: "true only when it was destroyed"}),

		ErrorCodes: []string{
			"confirmation.required",
			"bucket.confirm_name_mismatch",
			"bucket.not_found",
			"usage.missing_argument",
			"usage.empty_argument",
			"context.no_team",
		},
		ExitCodes: []int{0, 2, 3, 4, 5, 8},

		Examples: []Example{
			{
				Title:        "see what would be destroyed",
				Command:      "outplane bucket delete uploads --dry-run",
				Argv:         []string{"outplane", "bucket", "delete", "uploads", "--dry-run"},
				Placeholders: map[string]string{"uploads": "<BUCKET_NAME>"},
				Risk:         RiskRead,
			},
			{
				Title:        "the confirmed form, which a human approves",
				Command:      "outplane bucket delete uploads --yes --confirm-name uploads",
				Argv:         []string{"outplane", "bucket", "delete", "uploads", "--yes", "--confirm-name", "uploads"},
				Placeholders: map[string]string{"uploads": "<BUCKET_NAME>"},
				Risk:         RiskDestructive,
			},
			{
				Title:        "check what the name resolves to",
				Command:      "outplane bucket delete uploads --dry-run --json",
				Argv:         []string{"outplane", "bucket", "delete", "uploads", "--dry-run", "--json"},
				Placeholders: map[string]string{"uploads": "<BUCKET_NAME>"},
				Risk:         RiskRead,
				OutputSample: map[string]any{"name": "uploads", "public": false, "changed": false},
			},
		},

		AutomationNotes: []string{
			"This command never prompts. Without confirmation it exits 4 and returns the " +
				"command to replay in the error's confirm_command field.",
			"Under a detected agent harness it exits 4 even with both flags, so the approval " +
				"stays with whoever is accountable for the data.",
			"Irreversible. Every file and every key goes, and there is no snapshot and no " +
				"retention window.",
			"The bucket leaves `bucket list` at once while the deletion finishes in the " +
				"background, so its files and keys can keep answering for some minutes.",
			"Applications holding a key are not updated.",
		},

		Related: []string{"bucket get", "bucket list", "bucket key list"},
		DocsURL: "https://docs.outplane.com/cli/bucket",
	}
}
