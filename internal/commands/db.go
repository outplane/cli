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
	register("db list", dbList)
	register("db get", dbGet)
	register("db url", dbURL)
	register("db create", dbCreate)
	register("db delete", dbDelete)
}

// dbList reports the team's managed databases.
func dbList(ctx context.Context, req Request) (output.Table, error) {
	client, err := req.CLI.APIClient()
	if err != nil {
		return output.Table{}, err
	}

	dbs, err := core.ListDatabases(ctx, client)
	if err != nil {
		return output.Table{}, err
	}

	table := output.Table{
		Columns: []string{"name", "status", "version", "region", "size"},
		Total:   len(dbs),
	}
	for _, d := range dbs {
		table.Rows = append(table.Rows, databaseRow(d))
	}
	return table, nil
}

// dbGet reports one database and what is inside it.
//
// The roles and the databases within the instance come from the provider and
// cost a second request, which is worth it: without them the reader has the
// instance and no way to name the two things a connection string needs.
func dbGet(ctx context.Context, req Request) (output.Table, error) {
	db, err := targetDatabase(ctx, req, "get")
	if err != nil {
		return output.Table{}, err
	}

	client, err := req.CLI.APIClient()
	if err != nil {
		return output.Table{}, err
	}

	roles, schemas, err := core.DatabaseContents(ctx, client, db.ID)
	if err != nil {
		// A database still provisioning has nothing to list yet, and failing
		// the whole command for that would hide the status that explains it.
		req.CLI.Out.Note("Could not read its roles and databases: %v", err)
	}

	row := databaseRow(db)
	row["roles"] = roleNames(roles)
	row["databases"] = schemaRows(schemas)

	table := output.Table{
		Single:  true,
		Columns: []string{"name", "status", "version", "region", "size", "createdAt"},
		Total:   1,
		Rows:    []map[string]any{row},
		Footer:  contentsFooter(db, roles, schemas),
	}
	return table, nil
}

// contentsFooter lists what is inside, since a single-object view has no room
// for two lists and the reader needs both to build a connection string.
func contentsFooter(db core.Database, roles []core.DBRole, schemas []core.DBSchema) string {
	if db.Status != "active" {
		return "This database is " + db.Status + ". It cannot be connected to until it is active."
	}
	if len(roles) == 0 && len(schemas) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("Roles: " + strings.Join(roleNames(roles), ", ") + "\n")
	b.WriteString("Databases: ")
	for i, s := range schemas {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(s.Name)
	}
	// The instruction depends on whether there is a choice to make: with one of
	// each the flags are optional, and telling somebody to pass them anyway is
	// advice they will follow forever.
	if len(roles) == 1 && len(schemas) == 1 {
		b.WriteString("\n\nConnection string: outplane db url " + db.Name)
	} else {
		b.WriteString("\n\nA connection string needs one of each: outplane db url " + db.Name +
			" --role <ROLE> --database <DATABASE>")
	}
	return b.String()
}

// dbURL prints a connection string and nothing else.
//
// The string carries the role's password, which is why text output is the bare
// value: DATABASE_URL=$(outplane db url mydb --role app --database main) has to
// work, and a table around a credential helps nobody.
func dbURL(ctx context.Context, req Request) (output.Table, error) {
	db, err := targetDatabase(ctx, req, "url")
	if err != nil {
		return output.Table{}, err
	}

	client, err := req.CLI.APIClient()
	if err != nil {
		return output.Table{}, err
	}

	role, database, err := connectionTarget(ctx, req, client, db)
	if err != nil {
		return output.Table{}, err
	}

	url, err := core.ConnectionURL(ctx, client, db.ID, role, database)
	if err != nil {
		return output.Table{}, err
	}

	if !req.CLI.Out.Ctx.Machine() {
		fmt.Fprintln(req.CLI.Out.Out, url)
		return streamed(), nil
	}

	return output.Table{
		Single:  true,
		Columns: []string{"url", "role", "database"},
		Total:   1,
		Rows: []map[string]any{{
			"url":      url,
			"role":     role,
			"database": database,
			"db":       db.Name,
		}},
	}, nil
}

// connectionTarget resolves --role and --database, filling in the only choice
// when there is one.
//
// A database usually has exactly one of each, and making somebody name both to
// get the string they always want is friction with no safety in it. Where there
// is a choice, the names are listed rather than one of them being picked.
func connectionTarget(ctx context.Context, req Request, client *api.Client, db core.Database) (string, string, error) {
	role := strings.TrimSpace(req.Flags.String("role"))
	database := strings.TrimSpace(req.Flags.String("database"))
	if role != "" && database != "" {
		return role, database, nil
	}

	roles, schemas, err := core.DatabaseContents(ctx, client, db.ID)
	if err != nil {
		return "", "", err
	}

	if role == "" {
		if len(roles) != 1 {
			return "", "", mustChoose("role", roleNames(roles), db.Name)
		}
		role = roles[0].Name
	}
	if database == "" {
		names := make([]string, 0, len(schemas))
		for _, s := range schemas {
			names = append(names, s.Name)
		}
		if len(schemas) != 1 {
			return "", "", mustChoose("database", names, db.Name)
		}
		database = schemas[0].Name
	}
	return role, database, nil
}

func mustChoose(kind string, names []string, db string) error {
	e := clierr.New(clierr.KindUsage, "%s has more than one %s, so it needs naming", db, kind).
		WithCode("db." + kind + "_required")
	if len(names) == 0 {
		return e.WithHint("It has none. A database that is still provisioning has neither yet.")
	}
	return e.
		WithHint("It has: %s.", strings.Join(names, ", ")).
		WithDetail("available", names).
		WithStep("name one", "outplane", "db", "url", db, "--"+kind, names[0])
}

// dbCreate provisions a database.
//
// It returns as soon as the platform has accepted the request, which is before
// the provider has finished. The status says which, and the message says so,
// because a caller that connects immediately will fail.
func dbCreate(ctx context.Context, req Request) (output.Table, error) {
	if len(req.Args) == 0 {
		return output.Table{}, clierr.New(clierr.KindUsage, "no name given").
			WithCode("usage.missing_argument").
			WithStep("create a database", "outplane", "db", "create", "<NAME>",
				"--region", "aws-eu-central-1")
	}

	name := req.Args[0]
	version := orDefault(req.Flags.String("version"), "17")
	region := strings.TrimSpace(req.Flags.String("region"))

	if err := core.CheckDatabaseName(name); err != nil {
		return output.Table{}, err
	}
	if err := core.CheckDatabaseVersion(version); err != nil {
		return output.Table{}, err
	}
	if region == "" {
		return output.Table{}, clierr.New(clierr.KindUsage, "no region given").
			WithCode("db.region_required").
			WithHint("A region cannot be changed afterwards, so there is no default: %s.",
				strings.Join(core.DatabaseRegions, ", ")).
			WithStep("create it in Frankfurt", "outplane", "db", "create", name,
				"--region", "aws-eu-central-1")
	}
	if err := core.CheckDatabaseRegion(region); err != nil {
		return output.Table{}, err
	}

	cli := req.CLI
	if cli.Config.TeamError != nil {
		return output.Table{}, cli.SignInError()
	}

	if cli.DryRun {
		cli.Out.Note("Would create %s: PostgreSQL %s in %s. Nothing was sent.", name, version, region)
		return output.Table{
			Single:  true,
			Columns: []string{"name", "status", "version", "region", "changed"},
			Total:   1,
			Rows: []map[string]any{{
				"name": name, "status": "not created", "version": version,
				"region": region, "changed": false,
			}},
		}, nil
	}

	client, err := cli.APIClient()
	if err != nil {
		return output.Table{}, err
	}

	db, err := core.CreateDatabase(ctx, client, cli.Config.TeamID.Value, name, version, region)
	if err != nil {
		return output.Table{}, err
	}

	cli.Out.Note("Created %s: PostgreSQL %s in %s.", db.Name, db.Version, db.Region)
	cli.Out.Note("It is %s. Wait for active before connecting: outplane db get %s", db.Status, db.Name)

	row := databaseRow(db)
	row["changed"] = true
	return output.Table{
		Single:  true,
		Columns: []string{"name", "status", "version", "region", "changed"},
		Total:   1,
		Rows:    []map[string]any{row},
	}, nil
}

// dbDelete destroys a database.
//
// The same protocol as every other destructive command, and the strongest case
// for it on the platform: an application is rebuilt from its source, a disk can
// be restored from a backup somebody took, and this is the data itself.
func dbDelete(ctx context.Context, req Request) (output.Table, error) {
	db, err := targetDatabase(ctx, req, "delete")
	if err != nil {
		return output.Table{}, err
	}

	client, err := req.CLI.APIClient()
	if err != nil {
		return output.Table{}, err
	}

	if req.CLI.DryRun {
		req.CLI.Out.Note("%s and everything in it would be destroyed.", db.Name)
		return dbSingle(db, false), nil
	}

	if err := checkDatabaseConfirmed(req, db); err != nil {
		return output.Table{}, err
	}

	if err := core.DeleteDatabase(ctx, client, db.ID); err != nil {
		return output.Table{}, err
	}

	req.CLI.Out.Note("Deleted %s.", db.Name)
	return dbSingle(db, true), nil
}

func checkDatabaseConfirmed(req Request, db core.Database) error {
	if harness := req.CLI.Exec.AgentHarness; harness != "" {
		return dbConfirmation(db,
			"This is running under %s, where the CLI cannot be the thing that approves "+
				"destroying a database. Hand the command below to whoever is accountable "+
				"for the data.", harness)
	}

	if !req.Flags.Bool("yes") || req.Flags.String("confirm-name") == "" {
		return dbConfirmation(db,
			"Deleting %s destroys every database and role in it, and nothing restores "+
				"them. Both --yes and --confirm-name are required.", db.Name)
	}

	if given := req.Flags.String("confirm-name"); given != db.Name {
		return clierr.New(clierr.KindUsage,
			"--confirm-name says %q and the database is called %q", given, db.Name).
			WithCode("db.confirm_name_mismatch").
			WithDetail("expected", db.Name).
			WithDetail("given", given)
	}
	return nil
}

func dbConfirmation(db core.Database, hint string, args ...any) error {
	return clierr.New(clierr.KindConfirmation, "deleting %s needs confirmation", db.Name).
		WithCode("confirmation.required").
		WithHint(hint, args...).
		WithConfirmCommand("outplane", "db", "delete", db.Name, "--yes", "--confirm-name", db.Name)
}

// targetDatabase resolves the database argument.
func targetDatabase(ctx context.Context, req Request, verb string) (core.Database, error) {
	if len(req.Args) == 0 {
		return core.Database{}, clierr.New(clierr.KindUsage, "no database given").
			WithCode("usage.missing_argument").
			WithStep("see the team's databases", "outplane", "db", "list").
			WithStep(verb+" one", "outplane", "db", verb, "<DATABASE_NAME>")
	}
	if strings.TrimSpace(req.Args[0]) == "" {
		return core.Database{}, clierr.New(clierr.KindUsage, "the database argument is empty").
			WithCode("usage.empty_argument").
			WithHint("This is what an unset variable looks like.")
	}

	client, err := req.CLI.APIClient()
	if err != nil {
		return core.Database{}, err
	}

	db, err := core.FindDatabase(ctx, client, req.Args[0])
	if err == nil {
		return db, nil
	}

	var notFound *core.DatabaseNotFoundError
	if errors.As(err, &notFound) {
		e := clierr.New(clierr.KindNotFound, "%v", notFound).
			WithCode("db.not_found").
			WithStep("see the team's databases", "outplane", "db", "list")
		if len(notFound.Available) > 0 {
			e = e.WithHint("This team has: %s.", strings.Join(notFound.Available, ", ")).
				WithDetail("availableDatabases", notFound.Available)
		} else {
			e = e.WithHint("This team has no managed databases yet.")
		}
		return core.Database{}, e
	}

	var ambiguous *core.AmbiguousDatabaseError
	if errors.As(err, &ambiguous) {
		return core.Database{}, clierr.New(clierr.KindUsage, "%v", ambiguous).
			WithCode("db.ambiguous").
			WithHint("Use the id, which `outplane db list --json` reports.")
	}

	return core.Database{}, err
}

func databaseRow(d core.Database) map[string]any {
	return map[string]any{
		"id":        d.ID,
		"name":      d.Name,
		"status":    d.Status,
		"version":   d.Version,
		"region":    d.Region,
		"size":      nilIfEmpty(d.Size),
		"createdAt": nilIfEmpty(d.CreatedAt),
	}
}

func dbSingle(d core.Database, changed bool) output.Table {
	row := databaseRow(d)
	row["changed"] = changed
	return output.Table{
		Single:  true,
		Columns: []string{"name", "status", "version", "region", "changed"},
		Total:   1,
		Rows:    []map[string]any{row},
	}
}

func roleNames(roles []core.DBRole) []string {
	names := make([]string, 0, len(roles))
	for _, r := range roles {
		names = append(names, r.Name)
	}
	return names
}

func schemaRows(schemas []core.DBSchema) []map[string]any {
	rows := make([]map[string]any, 0, len(schemas))
	for _, s := range schemas {
		rows = append(rows, map[string]any{"name": s.Name, "owner": nilIfEmpty(s.Owner)})
	}
	return rows
}
