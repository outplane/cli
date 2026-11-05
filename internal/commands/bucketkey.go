package commands

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/outplane/cli/internal/api"
	"github.com/outplane/cli/internal/clierr"
	"github.com/outplane/cli/internal/core"
	"github.com/outplane/cli/internal/output"
)

func init() {
	register("bucket key list", bucketKeyList)
	register("bucket key create", bucketKeyCreate)
	register("bucket key delete", bucketKeyDelete)
}

// Access keys on a bucket.
//
// The secret is the whole reason this group needs care. It exists in one
// response, so it goes in the result, which is written in every format and under
// --quiet, and never in a note or an error, which are not.

func bucketKeyList(ctx context.Context, req Request) (output.Table, error) {
	bucket, err := targetBucket(ctx, req, "key", "list")
	if err != nil {
		return output.Table{}, err
	}

	client, err := req.CLI.APIClient()
	if err != nil {
		return output.Table{}, err
	}

	keys, err := readBucketKeys(ctx, req, client, bucket)
	if err != nil {
		return output.Table{}, err
	}

	table := output.Table{
		Columns: []string{"name", "accessKeyId", "permission"},
		Total:   len(keys),
	}
	for _, k := range keys {
		table.Rows = append(table.Rows, bucketKeyRow(k, bucket))
	}
	if len(keys) == 0 {
		table.Footer = fmt.Sprintf("%s has no keys. Clients need one: %s", bucket.Name,
			strings.Join(withTeam(req, "outplane", "bucket", "key", "create", bucket.Name,
				"<KEY_NAME>", "--permission", "read-write"), " "))
	}
	return table, nil
}

// bucketKeyCreate adds a key and reports the only copy of its secret.
//
// The name and the permission are checked before the bucket is resolved, so a
// mistake in them costs no request. The result repeats what the handler already
// knows about the bucket, so that the one response holding the secret is enough
// to connect.
func bucketKeyCreate(ctx context.Context, req Request) (output.Table, error) {
	if len(req.Args) < 2 {
		return output.Table{}, clierr.New(clierr.KindUsage, "a key needs a bucket and a name").
			WithCode("usage.missing_argument").
			WithStep("see the team's buckets", withTeam(req, "outplane", "bucket", "list")...).
			WithStep("create a key", bucketKeyCreateUsage(req)...)
	}

	name := req.Args[1]
	if err := core.CheckBucketKeyName(name); err != nil {
		return output.Table{}, err
	}

	permission := req.Flags.String("permission")
	if strings.TrimSpace(permission) == "" {
		return output.Table{}, clierr.New(clierr.KindUsage, "no permission given").
			WithCode("usage.missing_argument").
			WithHint("A key is one of: %s. There is no default.", strings.Join(core.BucketPermissions, ", ")).
			WithStep("create a key", bucketKeyCreateUsage(req)...)
	}
	if err := core.CheckBucketPermission(permission); err != nil {
		return output.Table{}, err
	}

	bucket, err := targetBucket(ctx, req, "key", "create")
	if err != nil {
		return output.Table{}, err
	}

	client, err := req.CLI.APIClient()
	if err != nil {
		return output.Table{}, err
	}

	if req.CLI.DryRun {
		req.CLI.Out.Note("Would add the %s key %s to %s. Nothing was sent.", permission, name, bucket.Name)
		return bucketKeyCreateTable(core.BucketKey{Name: name, Permission: permission}, bucket, false), nil
	}

	key, err := core.CreateBucketKey(ctx, client, bucket.ID, name, permission)
	if err != nil {
		return output.Table{}, explainBucketKeyCreateRefusal(req, err, name, bucket)
	}
	// A success with nothing to connect with is not one: the key may exist, and
	// this was the only response that could have carried its secret.
	if key.AccessKeyID == "" || key.SecretAccessKey == "" {
		return output.Table{}, bucketKeyMayExist(req, clierr.New(clierr.KindUpstream,
			"the server reported a new key without its id or its secret"), bucket)
	}

	req.CLI.Out.Note("The secret is shown here once and cannot be read again.")
	req.CLI.Out.Note("Clients need virtual-hosted addressing; path-style requests fail. Use bucketName, " +
		"not the short name.")
	return bucketKeyCreateTable(key, bucket, true), nil
}

// bucketKeyCreateUsage is the command with what the caller gave filled in. The
// permission stays a placeholder until one is given, because none is the default.
func bucketKeyCreateUsage(req Request) []string {
	bucket, name := "<BUCKET_NAME>", "<KEY_NAME>"
	if len(req.Args) > 0 && strings.TrimSpace(req.Args[0]) != "" {
		bucket = req.Args[0]
	}
	if len(req.Args) > 1 && strings.TrimSpace(req.Args[1]) != "" {
		name = req.Args[1]
	}
	permission := req.Flags.String("permission")
	if strings.TrimSpace(permission) == "" {
		permission = "<PERMISSION>"
	}
	return withTeam(req, "outplane", "bucket", "key", "create", bucket, name, "--permission", permission)
}

// explainBucketKeyCreateRefusal names what only the server can refuse.
//
// The text of a conflict here is written about a bucket name, which is the wrong
// thing to send a reader looking for, so it is replaced. A request that got no
// answer from the API is not retryable, although the kinds it arrives as are: it
// may have made a key whose secret nobody received, and a retry makes another
// one. A 5xx the API wrote itself means the server failed and stays retryable.
func explainBucketKeyCreateRefusal(req Request, err error, name string, bucket core.Bucket) error {
	e := clierr.AsError(err)
	if e == nil {
		return err
	}

	switch {
	case e.Kind == clierr.KindConflict:
		e.Message = fmt.Sprintf("the server refused the key name %q", name)
		return e.WithCode("bucket.key_conflict").
			WithHint("Pick another name for the key.").
			WithStep("see the bucket's keys", withTeam(req, "outplane", "bucket", "key", "list", bucket.Name)...)
	case e.Kind == clierr.KindNotFound:
		return bucketGone(req, e)
	case e.Code == "quota.upgrade_required":
		return bucketSubscriptionInactive(e)
	case answerLost(e):
		return bucketKeyMayExist(req, e, bucket)
	}
	return err
}

// answerLost reports a request that went out with nothing from the API coming
// back, so nothing says whether the server acted on it: no answer, one cut off,
// a proxy's reply, or a success that could not be read.
//
// It is decided by what the API client attaches rather than by its wording
// wherever that is possible, and a change in that wording fails towards not
// retrying. Two things stay retryable: a host that could not be reached never
// got the request, and only a 5xx the API wrote itself carries its message.
func answerLost(e *clierr.Error) bool {
	switch e.Kind {
	case clierr.KindTimeout:
		return true
	case clierr.KindUpstream:
		_, fromAPI := e.Details["apiMessage"]
		return !fromAPI && !strings.HasPrefix(e.Message, "could not reach")
	case clierr.KindInternal:
		return strings.HasPrefix(e.Message, "could not decode the response")
	}
	return false
}

// bucketKeyMayExist reports a create that may have left a key behind, whose
// secret nobody has.
func bucketKeyMayExist(req Request, e *clierr.Error, bucket core.Bucket) error {
	e.Retryable = false
	return e.WithHint("A key may exist that nobody has the secret of, and that secret cannot be "+
		"read again. Running this again makes another key.").
		WithStep("see the bucket's keys", withTeam(req, "outplane", "bucket", "key", "list", bucket.Name)...)
}

// bucketKeyDelete removes a key, under the usual protocol. A credential that
// cannot be read back is treated as destructive, as a database role and a
// registry login are.
func bucketKeyDelete(ctx context.Context, req Request) (output.Table, error) {
	bucket, key, err := targetBucketKey(ctx, req)
	if err != nil {
		return output.Table{}, err
	}

	client, err := req.CLI.APIClient()
	if err != nil {
		return output.Table{}, err
	}

	if req.CLI.DryRun {
		req.CLI.Out.Note("The key %s (%s) on %s would be deleted.", key.Name, key.AccessKeyID, bucket.Name)
		return bucketKeyDeleteTable(key, bucket, false), nil
	}

	if err := checkBucketKeyConfirmed(req, bucket, key); err != nil {
		return output.Table{}, err
	}

	if err := core.DeleteBucketKey(ctx, client, bucket.ID, key.AccessKeyID); err != nil {
		if e := clierr.AsError(err); e.Kind == clierr.KindNotFound {
			// This answers for a bucket that went as well as for a key, and the
			// server's text for the first is not written for a reader.
			e.Message = fmt.Sprintf("the key %s or its bucket no longer exists", key.Name)
			return output.Table{}, e.WithCode("bucket.key_not_found").
				WithHint("Both were found a moment ago. `outplane bucket key list` says which one went.").
				WithStep("see the bucket's keys", withTeam(req, "outplane", "bucket", "key", "list", bucket.Name)...).
				WithStep("see the team's buckets", withTeam(req, "outplane", "bucket", "list")...)
		}
		return output.Table{}, err
	}

	req.CLI.Out.Note("Deleted the key %s from %s. It can keep working for up to 15 minutes.", key.Name, bucket.Name)
	return bucketKeyDeleteTable(key, bucket, true), nil
}

// checkBucketKeyConfirmed is checkDeleteConfirmed, for a key. --confirm-name is
// the key's name even when the key was named by its id, because the name is what
// a person recognises.
func checkBucketKeyConfirmed(req Request, bucket core.Bucket, key core.BucketKey) error {
	if harness := req.CLI.Exec.AgentHarness; harness != "" {
		return bucketKeyConfirmation(req, bucket, key,
			"This is running under %s, where the CLI cannot be the thing that approves removing a "+
				"credential. Hand the command below to whoever is accountable for it.", harness)
	}

	if !req.Flags.Bool("yes") || req.Flags.String("confirm-name") == "" {
		return bucketKeyConfirmation(req, bucket, key,
			"Deleting the key %s breaks every client still using it, and its secret cannot be read "+
				"again. Both --yes and --confirm-name are required.", key.Name)
	}

	if given := req.Flags.String("confirm-name"); given != key.Name {
		return clierr.New(clierr.KindUsage,
			"--confirm-name says %q and the key is called %q", given, key.Name).
			WithCode("bucket.confirm_name_mismatch").
			WithDetail("expected", key.Name).
			WithDetail("given", given)
	}
	return nil
}

// bucketKeyConfirmation names the key by its id in the command it hands over,
// because a name can be shared and the replay has to reach this key.
func bucketKeyConfirmation(req Request, bucket core.Bucket, key core.BucketKey, hint string, args ...any) error {
	return clierr.New(clierr.KindConfirmation, "deleting the key %s needs confirmation", key.Name).
		WithCode("confirmation.required").
		WithHint(hint, args...).
		WithConfirmCommand(withTeam(req, "outplane", "bucket", "key", "delete", bucket.Name, key.AccessKeyID,
			"--yes", "--confirm-name", key.Name)...)
}

// targetBucketKey reads the two positionals `bucket key delete` takes and
// resolves them, translating the domain errors.
func targetBucketKey(ctx context.Context, req Request) (core.Bucket, core.BucketKey, error) {
	if len(req.Args) == 1 {
		return core.Bucket{}, core.BucketKey{}, clierr.New(clierr.KindUsage, "no key given").
			WithCode("usage.missing_argument").
			WithStep("see the bucket's keys", withTeam(req, "outplane", "bucket", "key", "list", req.Args[0])...)
	}
	if len(req.Args) > 1 && strings.TrimSpace(req.Args[1]) == "" {
		return core.Bucket{}, core.BucketKey{}, clierr.New(clierr.KindUsage, "the key argument is empty").
			WithCode("usage.empty_argument").
			WithHint("This is what an unset variable looks like.")
	}

	bucket, err := targetBucket(ctx, req, "key", "delete")
	if err != nil {
		return core.Bucket{}, core.BucketKey{}, err
	}

	client, err := req.CLI.APIClient()
	if err != nil {
		return core.Bucket{}, core.BucketKey{}, err
	}

	keys, err := readBucketKeys(ctx, req, client, bucket)
	if err != nil {
		return core.Bucket{}, core.BucketKey{}, err
	}

	key, err := core.FindBucketKey(keys, req.Args[1])
	if err == nil {
		return bucket, key, nil
	}

	var notFound *core.BucketKeyNotFoundError
	if errors.As(err, &notFound) {
		e := clierr.New(clierr.KindNotFound, "%v", notFound).
			WithCode("bucket.key_not_found").
			WithStep("see the bucket's keys", withTeam(req, "outplane", "bucket", "key", "list", bucket.Name)...)
		if len(notFound.Available) > 0 {
			return core.Bucket{}, core.BucketKey{}, e.WithHint("This bucket has: %s.", strings.Join(notFound.Available, ", ")).
				WithDetail("availableKeys", notFound.Available)
		}
		return core.Bucket{}, core.BucketKey{}, e.WithHint("This bucket has no keys.")
	}

	var ambiguous *core.AmbiguousBucketKeyError
	if errors.As(err, &ambiguous) {
		return core.Bucket{}, core.BucketKey{}, clierr.New(clierr.KindUsage, "%v", ambiguous).
			WithCode("bucket.key_ambiguous").
			WithHint("Names are not unique. Use one of these ids instead: %s.", strings.Join(ambiguous.IDs, ", ")).
			WithDetail("matchingKeys", ambiguous.IDs)
	}

	return core.Bucket{}, core.BucketKey{}, err
}

// readBucketKeys reads a bucket's keys, labelling a bucket deleted since it was
// resolved.
func readBucketKeys(ctx context.Context, req Request, client *api.Client, bucket core.Bucket) ([]core.BucketKey, error) {
	keys, err := core.ListBucketKeys(ctx, client, bucket.ID)
	if err != nil {
		if e := clierr.AsError(err); e.Kind == clierr.KindNotFound {
			return nil, bucketGone(req, e)
		}
		return nil, err
	}
	return keys, nil
}

func bucketKeyCreateTable(key core.BucketKey, bucket core.Bucket, changed bool) output.Table {
	row := bucketKeyRow(key, bucket)
	row["accessKeyId"] = nilIfEmpty(key.AccessKeyID)
	row["secretAccessKey"] = nilIfEmpty(key.SecretAccessKey)
	row["bucketName"] = bucket.BucketName
	row["endpoint"] = bucket.Endpoint
	row["region"] = bucket.Region
	row["changed"] = changed
	return output.Table{
		Single: true,
		// The secret has to be a column as well as a value, because text mode
		// prints columns and nothing else, and this is the only copy.
		Columns: []string{"name", "accessKeyId", "secretAccessKey", "permission", "bucketName", "endpoint", "region", "changed"},
		Total:   1,
		Rows:    []map[string]any{row},
	}
}

func bucketKeyDeleteTable(key core.BucketKey, bucket core.Bucket, changed bool) output.Table {
	row := bucketKeyRow(key, bucket)
	row["changed"] = changed
	return output.Table{
		Single:  true,
		Columns: []string{"name", "accessKeyId", "bucket", "changed"},
		Total:   1,
		Rows:    []map[string]any{row},
	}
}

func bucketKeyRow(key core.BucketKey, bucket core.Bucket) map[string]any {
	return map[string]any{
		"accessKeyId": key.AccessKeyID,
		"name":        key.Name,
		"permission":  key.Permission,
		"bucket":      bucket.Name,
		"bucketId":    bucket.ID,
	}
}
