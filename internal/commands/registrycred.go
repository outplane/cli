package commands

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"

	"github.com/outplane/cli/internal/clierr"
	"github.com/outplane/cli/internal/core"
	"github.com/outplane/cli/internal/output"
)

func init() {
	register("registry list", registryList)
	register("registry create", registryCreate)
	register("registry delete", registryDelete)
}

// Credentials for private container registries.
//
// Three commands because there are three endpoints: the platform offers no
// update, so a rotated password is a delete and a create. That is said in the
// help rather than hidden behind a command that would do both and leave the
// application without a credential in between if the second half failed.
//
// The password is the whole reason this group needs care. It is write-only on
// the server and it is never printed here, and --password-stdin exists so that
// it does not have to appear in a process list or a shell's history either.

func registryList(ctx context.Context, req Request) (output.Table, error) {
	client, err := req.CLI.APIClient()
	if err != nil {
		return output.Table{}, err
	}

	creds, err := core.ListRegistryCredentials(ctx, client)
	if err != nil {
		return output.Table{}, err
	}

	table := output.Table{
		Columns: []string{"name", "server", "username", "createdAt"},
		Total:   len(creds),
	}
	for _, c := range creds {
		table.Rows = append(table.Rows, map[string]any{
			"id":        c.ID,
			"name":      c.Name,
			"server":    c.Server,
			"username":  c.Username,
			"createdAt": nilIfEmpty(c.CreatedAt),
		})
	}

	if len(creds) == 0 {
		table.Footer = "This team has none. An application pulling from a private registry needs one."
	}
	return table, nil
}

// registryCreate stores a login.
func registryCreate(ctx context.Context, req Request) (output.Table, error) {
	if len(req.Args) == 0 || strings.TrimSpace(req.Args[0]) == "" {
		return output.Table{}, clierr.New(clierr.KindUsage, "no name given").
			WithCode("usage.missing_argument").
			WithHint("The name is what this credential is called here. The registry's host "+
				"is --server.").
			WithStep("store one", "outplane", "registry", "create", "<NAME>",
				"--server", "ghcr.io", "--username", "<USERNAME>", "--password-stdin")
	}

	password, err := registryPassword(req)
	if err != nil {
		return output.Table{}, err
	}

	cred := core.NewRegistryCredential{
		Name:     strings.TrimSpace(req.Args[0]),
		Server:   strings.TrimSpace(req.Flags.String("server")),
		Username: strings.TrimSpace(req.Flags.String("username")),
		Password: password,
	}
	if err := core.CheckRegistryCredential(cred); err != nil {
		return output.Table{}, err
	}

	client, err := req.CLI.APIClient()
	if err != nil {
		return output.Table{}, err
	}

	if req.CLI.DryRun {
		req.CLI.Out.Note("Would store %s for %s as %s. Nothing was sent.",
			cred.Name, cred.Server, cred.Username)
		return registryTable(core.RegistryCredential{
			Name: cred.Name, Server: cred.Server, Username: cred.Username,
		}, false), nil
	}

	created, err := core.CreateRegistryCredential(ctx, client, cred)
	if err != nil {
		return output.Table{}, err
	}

	req.CLI.Out.Note("Stored %s for %s.", created.Name, created.Server)
	req.CLI.Out.Note("An application already deployed keeps pulling with what it had. " +
		"The credential is used by the next deployment.")
	return registryTable(created, true), nil
}

// registryPassword reads the password from wherever it was given.
//
// Standard input is the form to reach for and the one CI should use: an
// argument is visible in the process list of every other user on the machine
// and in whatever recorded the command. --password exists because a person
// experimenting should not have to learn a pipe first, and it is marked
// discouraged in the help for the same reason.
func registryPassword(req Request) (string, error) {
	fromStdin := req.Flags.Bool("password-stdin")
	inline := req.Flags.String("password")

	switch {
	case fromStdin && inline != "":
		return "", clierr.New(clierr.KindUsage,
			"--password and --password-stdin are both set").
			WithCode("usage.conflicting_flags").
			WithHint("Only one of them can be the password, and there is no rule saying which.")
	case fromStdin:
		raw, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", clierr.New(clierr.KindUsage, "could not read the password: %v", err).
				WithCode("registry.password_unreadable")
		}
		// A trailing newline is what `echo` adds and what nobody means to
		// include. Nothing else is trimmed: a password may legitimately start
		// or end with a space.
		return strings.TrimRight(string(raw), "\r\n"), nil
	default:
		return inline, nil
	}
}

// registryDelete removes a credential.
func registryDelete(ctx context.Context, req Request) (output.Table, error) {
	if len(req.Args) == 0 || strings.TrimSpace(req.Args[0]) == "" {
		return output.Table{}, clierr.New(clierr.KindUsage, "no credential given").
			WithCode("usage.missing_argument").
			WithStep("see what this team has", "outplane", "registry", "list")
	}

	client, err := req.CLI.APIClient()
	if err != nil {
		return output.Table{}, err
	}

	cred, err := core.FindRegistryCredential(ctx, client, req.Args[0])
	if err != nil {
		return output.Table{}, explainRegistryNotFound(err)
	}

	if req.CLI.DryRun {
		req.CLI.Out.Note("The credential %s for %s would be removed.", cred.Name, cred.Server)
		return registryTable(cred, false), nil
	}

	if err := checkRegistryConfirmed(req, cred); err != nil {
		return output.Table{}, err
	}

	if err := core.DeleteRegistryCredential(ctx, client, cred.ID); err != nil {
		return output.Table{}, err
	}

	req.CLI.Out.Note("Removed %s.", cred.Name)
	req.CLI.Out.Note("An application that pulls from %s will fail at its next deployment "+
		"unless another credential covers it.", cred.Server)
	return registryTable(cred, true), nil
}

func checkRegistryConfirmed(req Request, cred core.RegistryCredential) error {
	confirm := func(hint string, args ...any) error {
		return clierr.New(clierr.KindConfirmation,
			"removing %s needs confirmation", cred.Name).
			WithCode("confirmation.required").
			WithHint(hint, args...).
			WithConfirmCommand("outplane", "registry", "delete", cred.Name,
				"--yes", "--confirm-name", cred.Name)
	}

	if harness := req.CLI.Exec.AgentHarness; harness != "" {
		return confirm("This is running under %s, where the CLI cannot be the thing that "+
			"approves breaking a deployment. Hand the command below to whoever is "+
			"accountable for it.", harness)
	}

	if !req.Flags.Bool("yes") || req.Flags.String("confirm-name") == "" {
		return confirm("The password cannot be read back, so this is not reversible without " +
			"the original. Both --yes and --confirm-name are required.")
	}

	if given := req.Flags.String("confirm-name"); given != cred.Name {
		return clierr.New(clierr.KindUsage,
			"--confirm-name says %q and the credential is called %q", given, cred.Name).
			WithCode("registry.confirm_name_mismatch").
			WithDetail("expected", cred.Name).
			WithDetail("given", given)
	}
	return nil
}

func explainRegistryNotFound(err error) error {
	var notFound *core.RegistryCredentialNotFoundError
	if !errors.As(err, &notFound) {
		return err
	}

	e := clierr.New(clierr.KindNotFound, "%v", notFound).
		WithCode("registry.not_found").
		WithStep("see what this team has", "outplane", "registry", "list")
	if len(notFound.Available) > 0 {
		return e.WithHint("It has: %s.", strings.Join(notFound.Available, ", ")).
			WithDetail("availableCredentials", notFound.Available)
	}
	return e.WithHint("It has none at all.")
}

func registryTable(cred core.RegistryCredential, changed bool) output.Table {
	return output.Table{
		Single:  true,
		Columns: []string{"name", "server", "username", "changed"},
		Total:   1,
		Rows: []map[string]any{{
			"id":        nilIfEmpty(cred.ID),
			"name":      cred.Name,
			"server":    cred.Server,
			"username":  cred.Username,
			"createdAt": nilIfEmpty(cred.CreatedAt),
			"changed":   changed,
		}},
	}
}
