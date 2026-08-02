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

	// Retryable says whether the same command could plausibly work if run
	// again without changing anything. It is the difference between a caller
	// backing off and a caller looping forever.
	Retryable bool `json:"retryable"`
}

// ExitCodes is the whole table, in the order somebody reads it.
func ExitCodes() []ExitCode {
	return []ExitCode{
		{Code: 0, What: "the command did what it said"},
		{Code: 1, Kind: string(KindInternal), What: "something failed that this CLI did not anticipate"},
		{Code: 2, Kind: string(KindUsage), What: "the invocation is wrong: a bad flag, a missing argument, an invalid value"},
		{Code: 3, Kind: string(KindAuth), What: "the credential was rejected, is missing, or belongs to another team"},
		{Code: 4, Kind: string(KindConfirmation), What: "the change needs confirming, and the error carries the exact command that would proceed"},
		{Code: 5, Kind: string(KindNotFound), What: "the thing named does not exist"},
		{Code: 6, Kind: string(KindConflict), What: "the platform refuses in this state: a name taken, a file already there"},
		{Code: 7, Kind: string(KindQuota), What: "a plan limit. Not rate limiting, and retrying never helps"},
		{Code: 8, Kind: string(KindUpstream), What: "the server or the network failed", Retryable: true},
		{Code: 9, Kind: string(KindUpgradeRequired), What: "the API no longer serves this release. Run `outplane update`"},
		{Code: 124, Kind: string(KindTimeout), What: "a wait ran out. The operation may still be running on the server", Retryable: true},
		{Code: 130, Kind: string(KindInterrupted), What: "interrupted"},
	}
}
