package clierr

// The exit code table, in one place, because three things need the same list
// and a copy of it would drift: `outplane help exit-codes` prints it, the
// schema publishes it, and Kind.ExitCode returns from it.
//
// The numbers are a public contract. Append only, never reuse, never change
// what one means: a caller pins them in a script nobody here can see.

// ExitCode is one entry in the table.
type ExitCode struct {
	Code int    `json:"code"`
	Kind string `json:"kind,omitempty"`
	What string `json:"what"`

	// Detail is the same thing said in full, for the schema and the
	// documentation, where there is room for a sentence and no column to fit.
	Detail string `json:"detail,omitempty"`

	// Retryable says whether the same command could plausibly work if run
	// again without changing anything. It is the difference between a caller
	// backing off and a caller looping forever.
	Retryable bool `json:"retryable"`
}

// ExitCodes is the whole table, in the order somebody reads it.
func ExitCodes() []ExitCode {
	return []ExitCode{
		{Code: 0, What: "the command did what it said"},
		{Code: 1, Kind: string(KindInternal),
			What:   "something failed that this CLI did not anticipate",
			Detail: "An unexpected failure in the CLI itself."},
		{Code: 2, Kind: string(KindUsage),
			What:   "the invocation is wrong: a bad flag, a missing argument, an invalid value",
			Detail: "Invalid arguments, unknown flag, or client-side validation failure."},
		{Code: 3, Kind: string(KindAuth),
			What:   "the credential was rejected, is missing, or belongs to another team",
			Detail: "Not authenticated, token revoked or expired, or forbidden for this team."},
		{Code: 4, Kind: string(KindConfirmation),
			What:   "the change needs confirming, and the error carries the exact command that would proceed",
			Detail: "A destructive operation stopped. Replay the command in confirm_command."},
		{Code: 5, Kind: string(KindNotFound),
			What:   "the thing named does not exist",
			Detail: "The named resource does not exist, or is not visible to this credential."},
		{Code: 6, Kind: string(KindConflict),
			What:   "the platform refuses in this state: a name taken, a file already there",
			Detail: "The resource already exists, or a concurrent change won."},
		{Code: 7, Kind: string(KindQuota),
			What:   "a plan limit. Not rate limiting, and retrying never helps",
			Detail: "Plan limit reached or payment required. Not a rate limit; retrying will not help."},
		{Code: 8, Kind: string(KindUpstream),
			What:      "the server or the network failed",
			Detail:    "The Out Plane API returned a server error.",
			Retryable: true},
		{Code: 9, Kind: string(KindUpgradeRequired),
			What:   "the API no longer serves this release. Run `outplane update`",
			Detail: "This release of the CLI is too old for the API. Updating is the only fix."},
		{Code: 124, Kind: string(KindTimeout),
			What:      "a wait ran out. The operation may still be running on the server",
			Detail:    "A client-side deadline expired. The server operation may still be running.",
			Retryable: true},
		{Code: 130, Kind: string(KindInterrupted),
			What:   "interrupted",
			Detail: "Cancelled by the user."},
	}
}
