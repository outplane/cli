package core

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/outplane/cli/internal/api"
)

// S3 compatible buckets, and the access keys that reach them.
//
// Three platform facts shape every command over them:
//
//   - A bucket has two names. The short one is unique in its team and is what
//     these commands take. Clients use the other, which carries a suffix the
//     platform adds and is part of the bucket's address.
//   - A key's secret exists in one response, the one that creates the key. No
//     endpoint returns it again, which is why only CreateBucketKey fills it in.
//   - Nothing enforces a key's name to be unique, and keys are listed in no
//     particular order, so a tie on a name is reported and the list is sorted
//     here.

// Bucket is one bucket, with what a client needs to reach it.
type Bucket struct {
	ID   string `json:"id"`
	Name string `json:"name"`

	// BucketName is the name a client uses. Name is only what the platform calls
	// the bucket.
	BucketName string `json:"bucketName"`

	Public    bool   `json:"public"`
	Endpoint  string `json:"endpoint"`
	Region    string `json:"region"`
	URL       string `json:"url"`
	CreatedAt string `json:"createdAt"`
}

type bucketDTO struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	BucketName  string `json:"providerBucketName"`
	IsPublic    bool   `json:"isPublic"`
	Endpoint    string `json:"endpoint"`
	Region      string `json:"region"`
	URL         string `json:"url"`
	CreatedDate string `json:"createdDate"`
}

// BucketKey is one access key on a bucket.
type BucketKey struct {
	AccessKeyID string `json:"accessKeyId"`
	Name        string `json:"name"`

	// Permission is decoded, so a value this release does not know reads as
	// unknown:N rather than as one of the two it does.
	Permission string `json:"permission"`

	// SecretAccessKey is set only on the key CreateBucketKey returns.
	SecretAccessKey string `json:"secretAccessKey,omitempty"`
}

type bucketKeyDTO struct {
	AccessKeyID     string `json:"accessKeyId"`
	Name            string `json:"name"`
	Permission      int    `json:"permission"`
	SecretAccessKey string `json:"secretAccessKey"`
}

// What an access key may do, as integers on the wire. See enum.go.
var bucketPermissionNames = enumNames{10: "read-write", 20: "read-only"}

// BucketPermissions is what --permission takes, in the order the help lists
// them. The registry cannot import this package, so a test keeps its enum in
// step.
var BucketPermissions = []string{"read-write", "read-only"}

// bucketNamePattern is the server's rule, apart from its ban on a double hyphen:
// that part is a lookahead there, which this regexp engine does not have, and
// CheckBucketName tests it separately.
var bucketNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,36}[a-z0-9]$`)

// ListBuckets returns every bucket in the team, in the order the server sorts
// them, which is by name.
func ListBuckets(ctx context.Context, c *api.Client) ([]Bucket, error) {
	var dtos []bucketDTO
	if err := c.Get(ctx, "/Bucket/GetBucketsByTeamId", &dtos); err != nil {
		return nil, err
	}

	buckets := make([]Bucket, 0, len(dtos))
	for _, d := range dtos {
		buckets = append(buckets, decodeBucket(d))
	}
	return buckets, nil
}

func decodeBucket(d bucketDTO) Bucket {
	return Bucket{
		ID:         d.ID,
		Name:       d.Name,
		BucketName: d.BucketName,
		Public:     d.IsPublic,
		Endpoint:   d.Endpoint,
		Region:     d.Region,
		URL:        d.URL,
		CreatedAt:  serverInstant(d.CreatedDate),
	}
}

// CreateBucket makes a bucket.
func CreateBucket(ctx context.Context, c *api.Client, name string, public bool) (Bucket, error) {
	body := map[string]any{"name": name, "isPublic": public}

	var dto bucketDTO
	if err := c.Post(ctx, "/Bucket/CreateBucket", body, &dto); err != nil {
		return Bucket{}, err
	}
	return decodeBucket(dto), nil
}

// SetBucketPublic changes who can read a bucket's files.
//
// The field always goes out, whichever way it is being set: the server reads an
// omitted one as false, so a request without it makes the bucket private.
func SetBucketPublic(ctx context.Context, c *api.Client, bucketID string, public bool) (Bucket, error) {
	var dto bucketDTO
	if err := c.Put(ctx, "/Bucket/UpdateBucket/"+bucketID, map[string]any{"isPublic": public}, &dto); err != nil {
		return Bucket{}, err
	}
	return decodeBucket(dto), nil
}

// DeleteBucket destroys a bucket, its files and its keys.
func DeleteBucket(ctx context.Context, c *api.Client, bucketID string) error {
	return c.Delete(ctx, "/Bucket/DeleteBucket/"+bucketID, nil)
}

// ListBucketKeys returns a bucket's keys, sorted by name and then by id so that
// two keys sharing a name still come out in the same order every time.
func ListBucketKeys(ctx context.Context, c *api.Client, bucketID string) ([]BucketKey, error) {
	var dtos []bucketKeyDTO
	if err := c.Get(ctx, "/Bucket/GetAccessKeys/"+bucketID, &dtos); err != nil {
		return nil, err
	}

	keys := make([]BucketKey, 0, len(dtos))
	for _, d := range dtos {
		keys = append(keys, decodeBucketKey(d))
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Name != keys[j].Name {
			return keys[i].Name < keys[j].Name
		}
		return keys[i].AccessKeyID < keys[j].AccessKeyID
	})
	return keys, nil
}

func decodeBucketKey(d bucketKeyDTO) BucketKey {
	return BucketKey{
		AccessKeyID:     d.AccessKeyID,
		Name:            d.Name,
		Permission:      bucketPermissionNames.name(d.Permission),
		SecretAccessKey: d.SecretAccessKey,
	}
}

// CreateBucketKey adds a key to a bucket. The result holds the only copy of the
// secret there will ever be.
func CreateBucketKey(ctx context.Context, c *api.Client, bucketID, name, permission string) (BucketKey, error) {
	value, err := bucketPermissionValue(permission)
	if err != nil {
		return BucketKey{}, err
	}

	var dto bucketKeyDTO
	body := map[string]any{"name": name, "permission": value}
	if err := c.Post(ctx, "/Bucket/CreateAccessKey/"+bucketID, body, &dto); err != nil {
		return BucketKey{}, err
	}
	return decodeBucketKey(dto), nil
}

// DeleteBucketKey removes a key. Unlike every other id here an access key id is
// a free string rather than a GUID, so it is escaped.
func DeleteBucketKey(ctx context.Context, c *api.Client, bucketID, accessKeyID string) error {
	return c.Delete(ctx, "/Bucket/DeleteAccessKey/"+bucketID+"/"+url.PathEscape(accessKeyID), nil)
}

// FindBucket resolves a reference into exactly one bucket, on the id first and
// then the exact name.
//
// There is no by-name endpoint, and a by-id request for a bucket that is gone
// answers with text meant for nobody, so this lists and matches. A name is
// unique in a team, which leaves no tie to report.
func FindBucket(ctx context.Context, c *api.Client, ref string) (Bucket, error) {
	buckets, err := ListBuckets(ctx, c)
	if err != nil {
		return Bucket{}, err
	}

	for _, b := range buckets {
		if b.ID == ref {
			return b, nil
		}
	}
	for _, b := range buckets {
		if b.Name == ref {
			return b, nil
		}
	}

	names := make([]string, 0, len(buckets))
	for _, b := range buckets {
		names = append(names, b.Name)
	}
	return Bucket{}, &BucketNotFoundError{Ref: ref, Available: names}
}

// FindBucketKey resolves a reference into exactly one of a bucket's keys, on the
// id first and then the exact name. A tie is reported rather than guessed,
// because the command being resolved for is a deletion.
func FindBucketKey(keys []BucketKey, ref string) (BucketKey, error) {
	for _, k := range keys {
		if k.AccessKeyID == ref {
			return k, nil
		}
	}

	var byName []BucketKey
	for _, k := range keys {
		if k.Name == ref {
			byName = append(byName, k)
		}
	}

	switch len(byName) {
	case 1:
		return byName[0], nil
	case 0:
		names := make([]string, 0, len(keys))
		for _, k := range keys {
			names = append(names, k.Name)
		}
		return BucketKey{}, &BucketKeyNotFoundError{Ref: ref, Available: names}
	default:
		ids := make([]string, 0, len(byName))
		for _, k := range byName {
			ids = append(ids, k.AccessKeyID)
		}
		return BucketKey{}, &AmbiguousBucketKeyError{Ref: ref, IDs: ids}
	}
}

// BucketNotFoundError carries what does exist, so the correction is one read
// away rather than another command away.
type BucketNotFoundError struct {
	Ref       string
	Available []string
}

func (e *BucketNotFoundError) Error() string {
	return fmt.Sprintf("no bucket called %q in this team", e.Ref)
}

// BucketKeyNotFoundError carries the names the bucket's keys do have.
type BucketKeyNotFoundError struct {
	Ref       string
	Available []string
}

func (e *BucketKeyNotFoundError) Error() string {
	return fmt.Sprintf("no access key called %q on this bucket", e.Ref)
}

// AmbiguousBucketKeyError means two keys share a name and only an id can say
// which was meant.
type AmbiguousBucketKeyError struct {
	Ref string
	IDs []string
}

func (e *AmbiguousBucketKeyError) Error() string {
	return fmt.Sprintf("%d access keys are called %q", len(e.IDs), e.Ref)
}

// CheckBucketName rejects a name the server would refuse.
func CheckBucketName(name string) error {
	switch {
	case strings.TrimSpace(name) == "":
		return usage("the bucket name is empty", "usage.empty_argument",
			"This is what an unset variable looks like.")
	case !bucketNamePattern.MatchString(name) || strings.Contains(name, "--"):
		return usage(fmt.Sprintf("%q is not a valid bucket name", short(name)),
			"bucket.name_invalid",
			"It has to be 3 to 38 characters of lowercase letters, numbers and single hyphens, "+
				"and start and end with a letter or a number.")
	}
	return nil
}

// CheckBucketKeyName rejects a key name the server would refuse. The empty case
// comes first because isVolumeName, whose character rule this shares, accepts
// an empty string.
func CheckBucketKeyName(name string) error {
	switch {
	case strings.TrimSpace(name) == "":
		return usage("the key name is empty", "usage.empty_argument",
			"This is what an unset variable looks like.")
	case len(name) > 100:
		return usage(fmt.Sprintf("%q is longer than 100 characters", short(name)),
			"bucket.key_name_invalid", "")
	case !isVolumeName(name):
		return usage(fmt.Sprintf("%q is not a valid key name", short(name)),
			"bucket.key_name_invalid",
			"It has to start with a letter or a number, and may then contain letters, "+
				"numbers, hyphens and underscores.")
	}
	return nil
}

// CheckBucketPermission rejects a permission that has no wire value.
func CheckBucketPermission(permission string) error {
	_, err := bucketPermissionValue(permission)
	return err
}

// bucketPermissionValue is a permission's wire value. An unknown word is an
// error and never a default: a typo must not grant write access.
func bucketPermissionValue(permission string) (int, error) {
	for value, name := range bucketPermissionNames {
		if name == permission {
			return value, nil
		}
	}
	return 0, usage(fmt.Sprintf("%q is not a permission", short(permission)),
		"bucket.permission_invalid", "A key is one of: %s.", strings.Join(BucketPermissions, ", "))
}
