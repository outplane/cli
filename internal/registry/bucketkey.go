package registry

// Access keys on a bucket.
//
// A key's secret exists in one response, the one that creates the key, so
// `bucket key create` is the only place one appears and it has to reach stdout
// whatever the format. --fields is refused there because narrowing the result
// could throw away the only copy, after the key has been made.
//
// There is no `bucket key get`, because a key has nothing to read beyond what the
// list reports, and no rotate. Replacing a key is a new key and a delete.

func init() {
	Register(
		bucketKeyList(),
		bucketKeyCreate(),
		bucketKeyDelete(),
	)
}

// bucketPermissions is what a key may do. The registry cannot import core, so
// core's tests keep this list and core.BucketPermissions in step.
var bucketPermissions = []string{"read-write", "read-only"}

// bucketKeyFields are the fields every key command reports. A key carries its
// bucket, because a row on its own has to say what it reaches.
func bucketKeyFields() []Field {
	return []Field{
		{
			Name: "accessKeyId", Type: "string",
			Description: "the key's id, and what to store. Names are not enforced unique",
		},
		{Name: "name", Type: "string"},
		{
			Name: "permission", Type: "string",
			Enum:        bucketPermissions,
			Description: "what it may do in this bucket. A value this release does not know reads as unknown:N",
		},
		{Name: "bucket", Type: "string", Description: "the bucket it reaches"},
		{Name: "bucketId", Type: "string"},
	}
}

func bucketKeyList() Command {
	return Command{
		Path:  []string{"bucket", "key", "list"},
		Short: "list the access keys on a bucket",
		Long: "Lists the access keys on a bucket, and what each one may do.\n\n" +
			"A secret is not here. It appears once, when the key is created, and cannot be " +
			"read again.",

		Risk:         RiskRead,
		RequiresAuth: true,
		Session:      SessionAny,
		Idempotent:   true,

		APICalls: []string{
			"GET /api/Bucket/GetBucketsByTeamId",
			"GET /api/Bucket/GetAccessKeys/{bucketId}",
		},

		Args:         []Arg{bucketArg()},
		OutputFields: bucketKeyFields(),

		ErrorCodes: []string{
			"bucket.not_found",
			"usage.missing_argument",
			"usage.empty_argument",
			"context.no_team",
		},
		ExitCodes: []int{0, 2, 3, 5, 8},

		Examples: []Example{
			{
				Title:        "which keys reach a bucket",
				Command:      "outplane bucket key list uploads",
				Argv:         []string{"outplane", "bucket", "key", "list", "uploads"},
				Placeholders: map[string]string{"uploads": "<BUCKET_NAME>"},
				Risk:         RiskRead,
			},
			{
				Title:        "find the id to delete",
				Command:      "outplane bucket key list uploads --json --fields name,accessKeyId,permission",
				Argv:         []string{"outplane", "bucket", "key", "list", "uploads", "--json", "--fields", "name,accessKeyId,permission"},
				Placeholders: map[string]string{"uploads": "<BUCKET_NAME>"},
				Risk:         RiskRead,
				OutputSample: map[string]any{
					"items": []any{
						map[string]any{"name": "web", "accessKeyId": "<ACCESS_KEY_ID>", "permission": "read-write"},
					},
					"total":     1,
					"truncated": false,
				},
			},
			{
				Title:        "the same list as a table, in a pipe",
				Command:      "outplane bucket key list uploads -o text",
				Argv:         []string{"outplane", "bucket", "key", "list", "uploads", "-o", "text"},
				Placeholders: map[string]string{"uploads": "<BUCKET_NAME>"},
				Risk:         RiskRead,
			},
		},

		AutomationNotes: []string{
			"A secret never appears here and cannot be read again, by this or any other command.",
			"Names are not enforced unique, and accessKeyId is what to store.",
			"An empty list is not an error. A new bucket has no key.",
		},

		Related: []string{"bucket key create", "bucket key delete", "bucket get"},
		DocsURL: "https://docs.outplane.com/cli/bucket",
	}
}

func bucketKeyCreate() Command {
	return Command{
		Path:  []string{"bucket", "key", "create"},
		Short: "add an access key to a bucket, showing its secret once",
		Long: "Creates an access key that reaches this one bucket and no other.\n\n" +
			"The secret is in this result and nowhere else. It cannot be read again, so a lost " +
			"one means a new key. The result also carries the bucket's name, endpoint and " +
			"region, which is everything an S3 client needs.\n\n" +
			"Clients need virtual-hosted addressing. Path-style requests fail.",

		Risk:         RiskWrite,
		RequiresAuth: true,
		Session:      SessionAny,

		// A second run makes a second key with a second secret, or is refused.
		Idempotent: false,

		SupportsDryRun: true,

		SuppressGlobals: []string{"fields"},

		APICalls: []string{
			"GET /api/Bucket/GetBucketsByTeamId",
			"POST /api/Bucket/CreateAccessKey/{bucketId}",
		},

		Args: []Arg{
			bucketArg(),
			{
				Name:     "name",
				Short:    "1 to 100 letters, numbers, hyphens and underscores",
				Required: true,
				Pattern:  "^[a-zA-Z0-9][a-zA-Z0-9\\-_]*$",
			},
		},

		Flags: []Flag{
			{
				Name: "permission", Type: "string",
				Enum:        bucketPermissions,
				Description: "what the key may do in this bucket. Required",
			},
		},

		OutputFields: append(bucketKeyFields(),
			Field{
				Name: "secretAccessKey", Type: "string | null",
				Description: "the secret, shown this once. null for a dry run",
			},
			Field{Name: "bucketName", Type: "string", Description: "the name S3 clients use"},
			Field{
				Name: "endpoint", Type: "string",
				Description: "the S3 endpoint. Clients need virtual-hosted addressing; path-style requests fail",
			},
			Field{Name: "region", Type: "string", Description: "the region clients sign with, always auto"},
			Field{
				Name: "changed", Type: "bool",
				Description: "false for a dry run, where accessKeyId is null as well",
			},
		),

		ErrorCodes: []string{
			"bucket.key_name_invalid",
			"bucket.key_conflict",
			"bucket.permission_invalid",
			"bucket.not_found",
			"bucket.subscription_inactive",
			"usage.flag_not_applicable",
			"usage.missing_argument",
			"usage.empty_argument",
			"context.no_team",
		},
		ExitCodes: []int{0, 1, 2, 3, 5, 6, 7, 8, 124},

		Examples: []Example{
			{
				Title:        "check the request without creating a key",
				Command:      "outplane bucket key create uploads web --permission read-write --dry-run --json",
				Argv:         []string{"outplane", "bucket", "key", "create", "uploads", "web", "--permission", "read-write", "--dry-run", "--json"},
				Placeholders: map[string]string{"uploads": "<BUCKET_NAME>", "web": "<KEY_NAME>"},
				Risk:         RiskRead,
				OutputSample: map[string]any{
					"name": "web", "permission": "read-write", "accessKeyId": nil,
					"secretAccessKey": nil, "changed": false,
				},
			},
			{
				Title:        "a key an application can read and write with",
				Command:      "outplane bucket key create uploads web --permission read-write",
				Argv:         []string{"outplane", "bucket", "key", "create", "uploads", "web", "--permission", "read-write"},
				Placeholders: map[string]string{"uploads": "<BUCKET_NAME>", "web": "<KEY_NAME>"},
				Risk:         RiskWrite,
			},
			{
				Title:        "a key that can only read, with the whole result as JSON",
				Command:      "outplane bucket key create uploads reports --permission read-only --json",
				Argv:         []string{"outplane", "bucket", "key", "create", "uploads", "reports", "--permission", "read-only", "--json"},
				Placeholders: map[string]string{"uploads": "<BUCKET_NAME>", "reports": "<KEY_NAME>"},
				Risk:         RiskWrite,
				OutputSample: map[string]any{
					"name": "reports", "permission": "read-only", "accessKeyId": "<ACCESS_KEY_ID>",
					"secretAccessKey": "<SECRET_ACCESS_KEY>", "bucketName": "uploads-<SUFFIX>",
					"endpoint": "<ENDPOINT>", "region": "auto", "changed": true,
				},
			},
		},

		AutomationNotes: []string{
			"The secret appears once, in this result, in every format and under --quiet. No " +
				"other command returns it and it cannot be read again.",
			"The secret is on stdout, so it lands in the record of whatever ran this command: a " +
				"terminal's scrollback, a CI log, an agent's transcript. That makes this a command " +
				"for a person at their own terminal when an agent is driving.",
			"`outplane env push` reads an application's values from a file, which keeps them out " +
				"of shell history and out of argv.",
			"Clients use bucketName with virtual-hosted addressing and the region auto. Path-style " +
				"requests fail.",
			"A timeout, a dropped connection, a proxy's reply or a success with no secret in it can " +
				"leave a key nobody has the secret of. retryable is false in those cases, because a " +
				"retry makes another key, and `bucket key list` shows what exists. A 5xx the API wrote " +
				"itself stays retryable.",
			"There is no rotation. Replacing a key means a new key, moving the clients to it, and " +
				"then `bucket key delete` on the old one.",
			"--fields is refused with exit 2 before anything is sent, because narrowing the result " +
				"could discard the only copy of the secret.",
		},

		Related: []string{"bucket key list", "bucket key delete", "bucket get", "env set", "env push"},
		DocsURL: "https://docs.outplane.com/cli/bucket",
	}
}

func bucketKeyDelete() Command {
	return Command{
		Path:  []string{"bucket", "key", "delete"},
		Short: "remove an access key from a bucket",
		Long: "Deletes an access key.\n\n" +
			"Its secret can keep working for up to 15 minutes, and then every client still using " +
			"it stops working. Applications holding it are not updated, and the secret cannot be " +
			"read again, so nothing brings the key back.",

		Risk:         RiskDestructive,
		RequiresAuth: true,
		Session:      SessionAny,

		// A second run finds nothing to delete and exits 5.
		Idempotent: false,

		SupportsDryRun: true,

		APICalls: []string{
			"GET /api/Bucket/GetBucketsByTeamId",
			"GET /api/Bucket/GetAccessKeys/{bucketId}",
			"DELETE /api/Bucket/DeleteAccessKey/{bucketId}/{accessKeyId}",
		},

		Args: []Arg{
			bucketArg(),
			{Name: "key", Short: "access key id or name", Required: true},
		},

		Flags: []Flag{
			{
				Name: "yes", Short: "y", Type: "bool", Default: "false",
				Description: "acknowledge the deletion. Not sufficient on its own",
			},
			{
				Name: "confirm-name", Type: "string",
				Description: "the key's name, typed again, even when the key is given by id",
			},
		},

		OutputFields: append(bucketKeyFields(),
			Field{Name: "changed", Type: "bool", Description: "true only when it was deleted"}),

		ErrorCodes: []string{
			"confirmation.required",
			"bucket.confirm_name_mismatch",
			"bucket.key_not_found",
			"bucket.key_ambiguous",
			"bucket.not_found",
			"usage.missing_argument",
			"usage.empty_argument",
			"context.no_team",
		},
		ExitCodes: []int{0, 2, 3, 4, 5, 8},

		Examples: []Example{
			{
				Title:        "see which key would go",
				Command:      "outplane bucket key delete uploads web --dry-run",
				Argv:         []string{"outplane", "bucket", "key", "delete", "uploads", "web", "--dry-run"},
				Placeholders: map[string]string{"uploads": "<BUCKET_NAME>", "web": "<KEY_NAME>"},
				Risk:         RiskRead,
			},
			{
				Title:        "the confirmed form, which a human approves",
				Command:      "outplane bucket key delete uploads web --yes --confirm-name web",
				Argv:         []string{"outplane", "bucket", "key", "delete", "uploads", "web", "--yes", "--confirm-name", "web"},
				Placeholders: map[string]string{"uploads": "<BUCKET_NAME>", "web": "<KEY_NAME>"},
				Risk:         RiskDestructive,
			},
			{
				Title:        "check what the name resolves to",
				Command:      "outplane bucket key delete uploads web --dry-run --json",
				Argv:         []string{"outplane", "bucket", "key", "delete", "uploads", "web", "--dry-run", "--json"},
				Placeholders: map[string]string{"uploads": "<BUCKET_NAME>", "web": "<KEY_NAME>"},
				Risk:         RiskRead,
				OutputSample: map[string]any{"name": "web", "accessKeyId": "<ACCESS_KEY_ID>", "changed": false},
			},
		},

		AutomationNotes: []string{
			"This command never prompts. Without confirmation it exits 4 and returns the " +
				"command to replay in the error's confirm_command field, which names the key by " +
				"its id.",
			"Under a detected agent harness it exits 4 even with both flags.",
			"The old secret can keep working for up to 15 minutes after this succeeds.",
			"Applications holding it are not updated.",
			"Deleting a key by name fails when two keys share it, with bucket.key_ambiguous. The " +
				"id always works.",
			"bucket.key_not_found on a key that was just resolved can also mean its bucket went in " +
				"that moment. `bucket key list` tells the two apart.",
		},

		Related: []string{"bucket key list", "bucket key create", "bucket delete"},
		DocsURL: "https://docs.outplane.com/cli/bucket",
	}
}
