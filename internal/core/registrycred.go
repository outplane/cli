package core

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/outplane/cli/internal/api"
)

// Credentials for pulling from a private container registry.
//
// The platform stores one per team and uses it when an application's image
// comes from somewhere that needs a login. There are three endpoints and no
// update: changing a password means deleting the credential and creating it
// again, which is the server's shape and not a simplification made here.
//
// The password is write-only. It goes in on creation and no endpoint returns
// it, which is the right design and the reason nothing in this file has a field
// for reading one back.

// RegistryCredential is one stored login.
type RegistryCredential struct {
	ID   string `json:"id"`
	Name string `json:"name"`

	// Server is the registry's host, as the runtime needs it: ghcr.io,
	// registry.gitlab.com, an account-specific ECR host.
	Server string `json:"server"`

	Username  string `json:"username"`
	CreatedAt string `json:"createdAt"`
}

type registryCredentialDTO struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Server      string `json:"server"`
	Username    string `json:"username"`
	CreatedDate string `json:"createdDate"`
}

// ListRegistryCredentials returns the team's credentials, sorted by name.
func ListRegistryCredentials(ctx context.Context, c *api.Client) ([]RegistryCredential, error) {
	var dtos []registryCredentialDTO
	if err := c.Get(ctx, "/RegistryCredential/GetByTeamId", &dtos); err != nil {
		return nil, err
	}

	out := make([]RegistryCredential, 0, len(dtos))
	for _, d := range dtos {
		out = append(out, RegistryCredential{
			ID: d.ID, Name: d.Name, Server: d.Server, Username: d.Username,
			CreatedAt: serverInstant(d.CreatedDate),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// NewRegistryCredential is what creating one needs.
type NewRegistryCredential struct {
	Name     string
	Server   string
	Username string
	Password string
}

// CreateRegistryCredential stores a login.
func CreateRegistryCredential(ctx context.Context, c *api.Client, cred NewRegistryCredential) (RegistryCredential, error) {
	body := map[string]any{
		"name":     cred.Name,
		"server":   cred.Server,
		"username": cred.Username,
		"password": cred.Password,
	}

	var dto registryCredentialDTO
	if err := c.Post(ctx, "/RegistryCredential/Create", body, &dto); err != nil {
		return RegistryCredential{}, err
	}
	return RegistryCredential{
		ID: dto.ID, Name: dto.Name, Server: dto.Server, Username: dto.Username,
		CreatedAt: serverInstant(dto.CreatedDate),
	}, nil
}

// DeleteRegistryCredential removes one.
func DeleteRegistryCredential(ctx context.Context, c *api.Client, id string) error {
	return c.Delete(ctx, "/RegistryCredential/Delete/"+id, nil)
}

// FindRegistryCredential resolves a reference into exactly one credential,
// matching the id first and then the name, as every other resource does.
func FindRegistryCredential(ctx context.Context, c *api.Client, ref string) (RegistryCredential, error) {
	creds, err := ListRegistryCredentials(ctx, c)
	if err != nil {
		return RegistryCredential{}, err
	}

	for _, cred := range creds {
		if cred.ID == ref || strings.EqualFold(cred.Name, ref) {
			return cred, nil
		}
	}
	return RegistryCredential{}, &RegistryCredentialNotFoundError{
		Ref: ref, Available: registryCredentialNames(creds),
	}
}

// RegistryCredentialNotFoundError carries what does exist.
type RegistryCredentialNotFoundError struct {
	Ref       string
	Available []string
}

func (e *RegistryCredentialNotFoundError) Error() string {
	return fmt.Sprintf("no registry credential called %q in this team", e.Ref)
}

func registryCredentialNames(creds []RegistryCredential) []string {
	names := make([]string, 0, len(creds))
	for _, c := range creds {
		names = append(names, c.Name)
	}
	return names
}

// CheckRegistryCredential rejects what the server would, before a password
// travels anywhere.
func CheckRegistryCredential(cred NewRegistryCredential) error {
	switch {
	case strings.TrimSpace(cred.Name) == "":
		return usage("a name is needed", "registry.name_required",
			"It is what the CLI and the console call this credential; it is not the registry's host.")
	case strings.TrimSpace(cred.Server) == "":
		return usage("a server is needed", "registry.server_required",
			"The registry's host, such as ghcr.io. Not a full URL and not an image reference.")
	case strings.Contains(cred.Server, "://"):
		return usage(fmt.Sprintf("%q is a URL, not a host", cred.Server),
			"registry.server_invalid", "Write it as ghcr.io rather than https://ghcr.io.")
	case strings.TrimSpace(cred.Username) == "":
		return usage("a username is needed", "registry.username_required", "")
	case cred.Password == "":
		return usage("a password is needed", "registry.password_required",
			"Pass it on standard input with --password-stdin, so it stays out of the "+
				"process list and the shell's history.")
	}
	return nil
}
