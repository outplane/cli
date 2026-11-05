package commands

import (
	"context"
	"errors"
	"strings"

	"github.com/outplane/cli/internal/clierr"
	"github.com/outplane/cli/internal/core"
	"github.com/outplane/cli/internal/output"
)

func init() {
	register("bucket list", bucketList)
	register("bucket get", bucketGet)
	register("bucket create", bucketCreate)
	register("bucket update", bucketUpdate)
	register("bucket delete", bucketDelete)
}

// S3 compatible buckets.
//
// A success here says the request was accepted. A new bucket's address, a change
// of visibility and a deletion all take minutes to spread, so each one says so
// on stderr, and the command's automation notes say it again for whoever ran
// with --quiet.

// bucketList reports the team's buckets.
func bucketList(ctx context.Context, req Request) (output.Table, error) {
	client, err := req.CLI.APIClient()
	if err != nil {
		return output.Table{}, err
	}

	buckets, err := core.ListBuckets(ctx, client)
	if err != nil {
		return output.Table{}, err
	}

	table := output.Table{
		Columns: []string{"name", "bucketName", "public", "createdAt"},
		Total:   len(buckets),
	}
	for _, b := range buckets {
		table.Rows = append(table.Rows, bucketRow(b))
	}
	return table, nil
}

// bucketGet reports one bucket.
func bucketGet(ctx context.Context, req Request) (output.Table, error) {
	bucket, err := targetBucket(ctx, req, "get")
	if err != nil {
		return output.Table{}, err
	}
	return output.Table{
		Single:  true,
		Columns: []string{"name", "id", "bucketName", "public", "endpoint", "region", "url", "createdAt"},
		Total:   1,
		Rows:    []map[string]any{bucketRow(bucket)},
	}, nil
}

// bucketCreate makes a bucket, private unless told otherwise.
//
// The server deletes the key it made while setting the bucket up, so a new
// bucket has none, and the note after a success says which command makes one.
func bucketCreate(ctx context.Context, req Request) (output.Table, error) {
	if len(req.Args) == 0 {
		return output.Table{}, clierr.New(clierr.KindUsage, "no name given").
			WithCode("usage.missing_argument").
			WithStep("create a bucket", withTeam(req, "outplane", "bucket", "create", "<NAME>")...)
	}

	name := req.Args[0]
	if err := core.CheckBucketName(name); err != nil {
		return output.Table{}, err
	}
	public := req.Flags.Bool("public")

	client, err := req.CLI.APIClient()
	if err != nil {
		return output.Table{}, err
	}

	if req.CLI.DryRun {
		req.CLI.Out.Note("Would create %s as a %s bucket. Nothing was sent.", name, visibility(public))
		return plannedBucketTable(name, public), nil
	}

	bucket, err := core.CreateBucket(ctx, client, name, public)
	if err != nil {
		return output.Table{}, explainBucketCreateRefusal(req, err, name)
	}

	req.CLI.Out.Note("Created %s. Clients use bucketName, %s.", bucket.Name, bucket.BucketName)
	req.CLI.Out.Note("A new bucket's address can take a few minutes to start working, for S3 clients " +
		"as well as public links.")
	req.CLI.Out.Note("Clients need a key. Its secret prints on stdout, so with an agent driving, this is a "+
		"command for a person at their own terminal: %s", strings.Join(withTeam(req, "outplane", "bucket", "key",
		"create", bucket.Name, "<KEY_NAME>", "--permission", "read-write"), " "))
	return bucketSingle(bucket, true, "name", "bucketName", "public", "url", "changed"), nil
}

// explainBucketCreateRefusal adds a way forward to the refusals only the server
// can make, keeping its message.
//
// The limit arrives as a plan limit and an inactive subscription as a Hobby
// team over its limit, and the generic advice for both is wrong here: the number
// of buckets is the same on every plan, and a subscription is not a plan limit.
func explainBucketCreateRefusal(req Request, err error, name string) error {
	e := clierr.AsError(err)
	if e == nil {
		return err
	}

	switch {
	case e.Kind == clierr.KindConflict:
		return e.WithCode("bucket.name_taken").
			WithHint("Names are unique in a team. A create that timed out may have finished.").
			WithStep("see the bucket with that name", withTeam(req, "outplane", "bucket", "get", name)...)
	case e.Code == "quota.limit_reached":
		e.NextSteps = nil
		return e.WithCode("bucket.limit_reached").
			WithHint("A team can have a fixed number of buckets, the same on every plan, and this one "+
				"has reached it. Retrying will not help; deleting one makes room.").
			WithStep("see the team's buckets", withTeam(req, "outplane", "bucket", "list")...)
	case e.Code == "quota.upgrade_required":
		return bucketSubscriptionInactive(e)
	}
	return err
}

// bucketUpdate makes a bucket public or private.
//
// Both flags are read by value and never through Given, which is true for any
// flag that was written: `--public=false` has to mean nothing was asked for
// rather than public. Both are checked before anything is sent, so a mistake in
// them costs no request.
func bucketUpdate(ctx context.Context, req Request) (output.Table, error) {
	public, private := req.Flags.Bool("public"), req.Flags.Bool("private")
	if public && private {
		return output.Table{}, clierr.New(clierr.KindUsage, "--public and --private are both set").
			WithCode("usage.conflicting_flags").
			WithHint("A bucket is one or the other. Give only the one you mean.")
	}
	if !public && !private {
		name := "<BUCKET_NAME>"
		if len(req.Args) > 0 && strings.TrimSpace(req.Args[0]) != "" {
			name = req.Args[0]
		}
		return output.Table{}, clierr.New(clierr.KindUsage, "nothing to change").
			WithCode("usage.no_change").
			WithHint("Name what it should be: --public or --private.").
			WithStep("see the current setting", withTeam(req, "outplane", "bucket", "get", name, "--fields", "public")...)
	}

	bucket, err := targetBucket(ctx, req, "update")
	if err != nil {
		return output.Table{}, err
	}

	client, err := req.CLI.APIClient()
	if err != nil {
		return output.Table{}, err
	}

	columns := []string{"name", "public", "url", "changed"}
	if bucket.Public == public {
		req.CLI.Out.Note("%s is already set to %s. A recent change can take a few minutes to apply.",
			bucket.Name, visibility(public))
		return bucketSingle(bucket, false, columns...), nil
	}

	if req.CLI.DryRun {
		req.CLI.Out.Note("Would make %s %s. Nothing was sent.", bucket.Name, visibility(public))
		bucket.Public = public
		return bucketSingle(bucket, false, columns...), nil
	}

	updated, err := core.SetBucketPublic(ctx, client, bucket.ID, public)
	if err != nil {
		return output.Table{}, explainBucketUpdateRefusal(req, err)
	}

	if public {
		req.CLI.Out.Note("Set %s to public. Anyone with a file's link can read it, and listing stays off.", updated.Name)
	} else {
		req.CLI.Out.Note("Set %s to private. Only requests signed with an access key can read its files.", updated.Name)
	}
	req.CLI.Out.Note("Changes can take a few minutes to apply. Until then, some requests can see the old setting.")
	return bucketSingle(updated, true, columns...), nil
}

// explainBucketUpdateRefusal names what only the server can refuse: a bucket
// deleted since it was resolved, and going public without a subscription.
func explainBucketUpdateRefusal(req Request, err error) error {
	e := clierr.AsError(err)
	if e == nil {
		return err
	}

	switch {
	case e.Kind == clierr.KindNotFound:
		return bucketGone(req, e)
	case e.Code == "quota.upgrade_required":
		return bucketSubscriptionInactive(e)
	}
	return err
}

// bucketDelete destroys a bucket, under app delete's protocol.
//
// The server deletes every file and key with no confirmation of its own, so
// this is the only place the caller is asked.
func bucketDelete(ctx context.Context, req Request) (output.Table, error) {
	bucket, err := targetBucket(ctx, req, "delete")
	if err != nil {
		return output.Table{}, err
	}

	client, err := req.CLI.APIClient()
	if err != nil {
		return output.Table{}, err
	}

	columns := []string{"name", "bucketName", "public", "changed"}
	if req.CLI.DryRun {
		req.CLI.Out.Note("%s, every file in it and every key on it would be destroyed.", bucket.Name)
		return bucketSingle(bucket, false, columns...), nil
	}

	if err := checkBucketConfirmed(req, bucket); err != nil {
		return output.Table{}, err
	}

	if err := core.DeleteBucket(ctx, client, bucket.ID); err != nil {
		if e := clierr.AsError(err); e.Kind == clierr.KindNotFound {
			return output.Table{}, bucketGone(req, e)
		}
		return output.Table{}, err
	}

	req.CLI.Out.Note("Deleted %s. Its files and keys can keep answering for some minutes.", bucket.Name)
	return bucketSingle(bucket, true, columns...), nil
}

// checkBucketConfirmed is checkDeleteConfirmed, for a bucket. See there for why
// an agent harness is refused even with both flags.
func checkBucketConfirmed(req Request, bucket core.Bucket) error {
	if harness := req.CLI.Exec.AgentHarness; harness != "" {
		return bucketConfirmation(req, bucket,
			"This is running under %s, where the CLI cannot be the thing that approves "+
				"destroying data. Hand the command below to whoever is accountable for it.", harness)
	}

	if !req.Flags.Bool("yes") || req.Flags.String("confirm-name") == "" {
		return bucketConfirmation(req, bucket,
			"Deleting %s destroys every file and key in it, and nothing restores them. Both --yes "+
				"and --confirm-name are required.", bucket.Name)
	}

	if given := req.Flags.String("confirm-name"); given != bucket.Name {
		return clierr.New(clierr.KindUsage,
			"--confirm-name says %q and the bucket is called %q", given, bucket.Name).
			WithCode("bucket.confirm_name_mismatch").
			WithDetail("expected", bucket.Name).
			WithDetail("given", given)
	}
	return nil
}

func bucketConfirmation(req Request, bucket core.Bucket, hint string, args ...any) error {
	return clierr.New(clierr.KindConfirmation, "deleting %s needs confirmation", bucket.Name).
		WithCode("confirmation.required").
		WithHint(hint, args...).
		WithConfirmCommand(withTeam(req, "outplane", "bucket", "delete", bucket.Name,
			"--yes", "--confirm-name", bucket.Name)...)
}

// targetBucket resolves the bucket argument, translating the domain errors. The
// verb is the command's words after `bucket`, for the usage it suggests.
func targetBucket(ctx context.Context, req Request, verb ...string) (core.Bucket, error) {
	if len(req.Args) == 0 {
		usageLine := append(append([]string{"outplane", "bucket"}, verb...), "<BUCKET_NAME>")
		return core.Bucket{}, clierr.New(clierr.KindUsage, "no bucket given").
			WithCode("usage.missing_argument").
			WithStep("see the team's buckets", withTeam(req, "outplane", "bucket", "list")...).
			WithStep(strings.Join(verb, " ")+" one", withTeam(req, usageLine...)...)
	}
	if strings.TrimSpace(req.Args[0]) == "" {
		return core.Bucket{}, clierr.New(clierr.KindUsage, "the bucket argument is empty").
			WithCode("usage.empty_argument").
			WithHint("This is what an unset variable looks like.")
	}

	client, err := req.CLI.APIClient()
	if err != nil {
		return core.Bucket{}, err
	}

	bucket, err := core.FindBucket(ctx, client, req.Args[0])
	if err == nil {
		return bucket, nil
	}

	var notFound *core.BucketNotFoundError
	if errors.As(err, &notFound) {
		e := clierr.New(clierr.KindNotFound, "%v", notFound).
			WithCode("bucket.not_found").
			WithStep("see the team's buckets", withTeam(req, "outplane", "bucket", "list")...)
		if len(notFound.Available) > 0 {
			return core.Bucket{}, e.WithHint("This team has: %s.", strings.Join(notFound.Available, ", ")).
				WithDetail("availableBuckets", notFound.Available)
		}
		return core.Bucket{}, e.WithHint("This team has no buckets yet.")
	}
	return core.Bucket{}, err
}

// bucketGone labels a bucket deleted since it was resolved. The server's text
// for this is not written for a reader, so it is replaced.
func bucketGone(req Request, e *clierr.Error) error {
	e.Message = "the bucket no longer exists"
	return e.WithCode("bucket.not_found").
		WithHint("The bucket was found a moment ago and is gone now.").
		WithStep("see the team's buckets", withTeam(req, "outplane", "bucket", "list")...)
}

// bucketSubscriptionInactive replaces the generic advice for a 402, which is
// written for a Hobby team over a plan limit and points at the wrong command.
// The server's message is kept.
func bucketSubscriptionInactive(e *clierr.Error) error {
	e.NextSteps = nil
	return e.WithCode("bucket.subscription_inactive").
		WithHint("This needs an active subscription, on every plan. Billing is in the console; the " +
			"CLI has no billing commands.")
}

func visibility(public bool) string {
	if public {
		return "public"
	}
	return "private"
}

// plannedBucketTable is what a dry run reports. What only the server decides,
// the id, the name clients use and the addresses, is null rather than guessed.
func plannedBucketTable(name string, public bool) output.Table {
	return output.Table{
		Single:  true,
		Columns: []string{"name", "bucketName", "public", "url", "changed"},
		Total:   1,
		Rows: []map[string]any{{
			"id":         nil,
			"name":       name,
			"bucketName": nil,
			"public":     public,
			"endpoint":   nil,
			"region":     nil,
			"url":        nil,
			"createdAt":  nil,
			"changed":    false,
		}},
	}
}

func bucketSingle(b core.Bucket, changed bool, columns ...string) output.Table {
	row := bucketRow(b)
	row["changed"] = changed
	return output.Table{
		Single:  true,
		Columns: columns,
		Total:   1,
		Rows:    []map[string]any{row},
	}
}

func bucketRow(b core.Bucket) map[string]any {
	return map[string]any{
		"id":         b.ID,
		"name":       b.Name,
		"bucketName": b.BucketName,
		"public":     b.Public,
		"endpoint":   b.Endpoint,
		"region":     b.Region,
		"url":        b.URL,
		"createdAt":  nilIfEmpty(b.CreatedAt),
	}
}
