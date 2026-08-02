package core

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/outplane/cli/internal/api"
)

// Managed PostgreSQL.
//
// The platform provisions the database itself; nothing here runs in the team's
// own applications. That has two consequences the commands have to carry:
//
//   - Provisioning is not instant. A database is created in a Provisioning
//     state and becomes Active when the provider has it ready, so a caller that
//     creates one and immediately connects will fail.
//   - Roles and databases inside it are the provider's objects, addressed by
//     name rather than by id, and a connection string is assembled from a role
//     and a database rather than stored.

// Database is one managed PostgreSQL instance.
type Database struct {
	ID   string `json:"id"`
	Name string `json:"name"`

	// Status is the lifecycle state, and the only one worth waiting for is
	// active. Provisioning means the provider has not finished.
	Status  string `json:"status"`
	Version string `json:"version"`
	Region  string `json:"region"`

	// Size is the provider's own compute unit, such as "0.25-1". It is not one
	// of the platform's instance codes and there is nothing to compare it with,
	// so it is passed through rather than interpreted.
	Size string `json:"size"`

	CreatedAt string `json:"createdAt"`
}

type databaseDTO struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Status       int    `json:"status"`
	Version      string `json:"version"`
	Region       string `json:"region"`
	InstanceType string `json:"instanceType"`
	CreatedDate  string `json:"createdDate"`
}

// Lifecycle states, as integers on the wire.
var dbStatusNames = enumNames{
	10: "provisioning",
	20: "active",
	30: "failed",
	40: "deleting",
}

// PostgresVersions and DatabaseRegions are what the server accepts. They are
// restated so that a wrong one is refused with the alternatives in hand, and
// because a region cannot be changed after creation.
var (
	PostgresVersions = []string{"14", "15", "16", "17", "18"}
	DatabaseRegions  = []string{
		"aws-us-east-1", "aws-us-east-2", "aws-us-west-2",
		"aws-eu-central-1", "aws-eu-west-2",
		"aws-ap-southeast-1", "aws-ap-southeast-2", "aws-sa-east-1",
	}
)

// ListDatabases returns the team's managed databases, sorted by name.
func ListDatabases(ctx context.Context, c *api.Client) ([]Database, error) {
	var dtos []databaseDTO
	if err := c.Get(ctx, "/DataStorage/GetAllDataSourcesByTeamId", &dtos); err != nil {
		return nil, err
	}

	dbs := make([]Database, 0, len(dtos))
	for _, d := range dtos {
		dbs = append(dbs, decodeDatabase(d))
	}
	sort.Slice(dbs, func(i, j int) bool { return dbs[i].Name < dbs[j].Name })
	return dbs, nil
}

// GetDatabase reads one database's current state.
//
// The detail endpoint also returns the provider's own project object, which is
// deliberately not decoded: it is somebody else's shape, it changes without
// notice, and nothing in the CLI needs it.
func GetDatabase(ctx context.Context, c *api.Client, id string) (Database, error) {
	var dto databaseDTO
	if err := c.Get(ctx, "/DataStorage/GetDataStorageById/"+id+"/false", &dto); err != nil {
		return Database{}, err
	}
	return decodeDatabase(dto), nil
}

func decodeDatabase(d databaseDTO) Database {
	return Database{
		ID:        d.ID,
		Name:      d.Name,
		Status:    dbStatusNames.name(d.Status),
		Version:   d.Version,
		Region:    d.Region,
		Size:      d.InstanceType,
		CreatedAt: serverInstant(d.CreatedDate),
	}
}

// CreateDatabase provisions a managed PostgreSQL instance.
func CreateDatabase(ctx context.Context, c *api.Client, teamID, name, version, region string) (Database, error) {
	body := map[string]any{
		"name":    name,
		"version": version,
		"region":  region,
		"teamId":  teamID,
	}

	var dto databaseDTO
	if err := c.Post(ctx, "/DataStorage/CreatePostgresDataStorage", body, &dto); err != nil {
		return Database{}, err
	}
	return decodeDatabase(dto), nil
}

// DeleteDatabase destroys a database and everything in it.
func DeleteDatabase(ctx context.Context, c *api.Client, id string) error {
	return c.Delete(ctx, "/DataStorage/DeleteDataStorage/"+id, nil)
}

// DBRole is a login the database accepts. DBSchema is a database inside the
// instance, which PostgreSQL also calls a database; the CLI keeps the
// platform's word for the instance and the provider's word for what is in it.
type DBRole struct {
	Name string `json:"name"`
}

type DBSchema struct {
	Name  string `json:"name"`
	Owner string `json:"owner"`
}

type rolesAndDatabasesDTO struct {
	Roles []struct {
		Name string `json:"name"`
	} `json:"roles"`
	Databases []struct {
		Name      string `json:"name"`
		OwnerName string `json:"owner_name"`
		Owner     string `json:"ownerName"`
	} `json:"databases"`
}

// DatabaseContents lists the roles and the databases inside an instance.
func DatabaseContents(ctx context.Context, c *api.Client, id string) ([]DBRole, []DBSchema, error) {
	var dto rolesAndDatabasesDTO
	if err := c.Get(ctx, "/DataStorage/GetDataStorageRolesAndDatabases/"+id, &dto); err != nil {
		return nil, nil, err
	}

	roles := make([]DBRole, 0, len(dto.Roles))
	for _, r := range dto.Roles {
		roles = append(roles, DBRole{Name: r.Name})
	}

	schemas := make([]DBSchema, 0, len(dto.Databases))
	for _, d := range dto.Databases {
		schemas = append(schemas, DBSchema{Name: d.Name, Owner: firstNonEmpty(d.OwnerName, d.Owner)})
	}

	sort.Slice(roles, func(i, j int) bool { return roles[i].Name < roles[j].Name })
	sort.Slice(schemas, func(i, j int) bool { return schemas[i].Name < schemas[j].Name })
	return roles, schemas, nil
}

// ConnectionURL assembles the string a client connects with.
//
// It carries the role's password, so it is a credential and every command that
// handles it treats it as one.
func ConnectionURL(ctx context.Context, c *api.Client, id, role, database string) (string, error) {
	var url string
	path := fmt.Sprintf("/DataStorage/GetConnectionUrl/%s/%s/%s", id, role, database)
	if err := c.Get(ctx, path, &url); err != nil {
		return "", err
	}
	return url, nil
}

// FindDatabase resolves a reference into exactly one database.
func FindDatabase(ctx context.Context, c *api.Client, ref string) (Database, error) {
	dbs, err := ListDatabases(ctx, c)
	if err != nil {
		return Database{}, err
	}

	for _, d := range dbs {
		if d.ID == ref {
			return d, nil
		}
	}

	var byName []Database
	for _, d := range dbs {
		if d.Name == ref {
			byName = append(byName, d)
		}
	}

	switch len(byName) {
	case 1:
		return byName[0], nil
	case 0:
		return Database{}, &DatabaseNotFoundError{Ref: ref, Available: databaseNames(dbs)}
	default:
		return Database{}, &AmbiguousDatabaseError{Ref: ref, Count: len(byName)}
	}
}

// DatabaseNotFoundError carries what does exist.
type DatabaseNotFoundError struct {
	Ref       string
	Available []string
}

func (e *DatabaseNotFoundError) Error() string {
	return fmt.Sprintf("no database called %q in this team", e.Ref)
}

// AmbiguousDatabaseError means two databases share a name.
type AmbiguousDatabaseError struct {
	Ref   string
	Count int
}

func (e *AmbiguousDatabaseError) Error() string {
	return fmt.Sprintf("%d databases are called %q", e.Count, e.Ref)
}

func databaseNames(dbs []Database) []string {
	names := make([]string, 0, len(dbs))
	for _, d := range dbs {
		names = append(names, d.Name)
	}
	return names
}

// CheckDatabaseName rejects a name the server would refuse.
func CheckDatabaseName(name string) error {
	switch {
	case strings.TrimSpace(name) == "":
		return usage("a database needs a name", "db.name_required", "")
	case len(name) > 128:
		return usage(fmt.Sprintf("%q is longer than 128 characters", short(name)),
			"db.name_invalid", "")
	}
	return nil
}

// CheckDatabaseVersion and CheckDatabaseRegion refuse what the provider will
// not accept, before a request that would fail after a provisioning attempt.
func CheckDatabaseVersion(version string) error {
	if !contains(PostgresVersions, version) {
		return usage(fmt.Sprintf("no PostgreSQL version %q", version), "db.version_invalid",
			"Use one of: %s.", strings.Join(PostgresVersions, ", "))
	}
	return nil
}

func CheckDatabaseRegion(region string) error {
	if !contains(DatabaseRegions, region) {
		return usage(fmt.Sprintf("no region called %q", region), "db.region_invalid",
			"Use one of: %s.", strings.Join(DatabaseRegions, ", "))
	}
	return nil
}
