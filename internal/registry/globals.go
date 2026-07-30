package registry

// GlobalFlags are the flags every command accepts.
//
// This list lives in the registry, beside the command declarations, because
// three surfaces have to agree about it: the help renderer, the machine-
// readable schema, and the flags actually registered with cobra. It previously
// existed twice, once for help and once for the schema, and the two drifted:
// the schema advertised --jq and --timeout, which do not exist, and omitted
// --token, which does. An agent reading that schema would emit a flag the CLI
// rejects and never learn about one it needs.
//
// Anything added here must also be registered in cmd/outplane. A flag that is
// documented but not registered is worse than one that is undocumented,
// because the documentation is what an agent plans against.
//
// Two registered flags are deliberately absent. --api-url is for local
// development and a future self-hosted deployment, and documenting it would
// invite people to point the CLI somewhere unsupported. --no-color is honoured
// but universal and dull; NO_COLOR and TERM=dumb do the same job.
func GlobalFlags() []Flag {
	return []Flag{
		{
			Name: "output", Short: "o", Type: "string", Default: "auto",
			Enum:        []string{"auto", "text", "json", "ndjson"},
			Description: "output format. auto means text on a terminal, json when piped",
		},
		{Name: "json", Type: "bool", Default: "false", Description: "shorthand for --output json"},
		{
			Name: "fields", Type: "string",
			Description: "limit structured output to these fields, comma separated. " +
				"An unknown name is an error, not an omission",
		},
		{Name: "team", Type: "string", Description: "team slug or id, overriding the linked team"},
		{
			Name: "token", Type: "string",
			Description: "API token",
			Discouraged: "argv is visible in process lists and CI logs. Prefer OUTPLANE_TOKEN",
		},
		{Name: "quiet", Short: "q", Type: "bool", Default: "false", Description: "suppress everything except errors"},
		{
			Name: "dry-run", Type: "bool", Default: "false",
			Description: "print the request that would be sent, without sending it",
		},
		{
			Name: "yes", Short: "y", Type: "bool", Default: "false",
			Description: "acknowledge a reversible change. Never enough for a destructive one",
		},
	}
}
