package core

import (
	"context"

	"github.com/outplane/cli/internal/api"
)

// ConnectURL is where a person grants or widens access to their repositories.
//
// Printed by `outplane repos` every time, not only when the list is empty. The
// common case is not "nothing is connected" but "the repository I wanted is not
// among these", and that is fixed at the same address.
const ConnectURL = "https://github.com/apps/out-plane-connect-run/installations/select_target"

// Repo is a repository the signed-in user can deploy from.
type Repo struct {
	FullName string `json:"fullName"`
	Name     string `json:"name"`
	Private  bool   `json:"private"`
	Language string `json:"language"`
	Branch   string `json:"defaultBranch"`
	Archived bool   `json:"archived"`
	URL      string `json:"url"`

	// Provider names where the repository lives. Only one exists today, and it
	// is reported anyway so that a caller written now keeps working when a
	// second one appears rather than having to learn a new field.
	Provider string `json:"provider"`
}

type repoDTO struct {
	Name          string `json:"name"`
	FullName      string `json:"fullName"`
	Private       bool   `json:"private"`
	Language      string `json:"language"`
	Archived      bool   `json:"archived"`
	Disabled      bool   `json:"disabled"`
	HTMLURL       string `json:"htmlUrl"`
	DefaultBranch string `json:"defaultBranch"`
}

// ListRepos returns every repository the token's user can deploy from.
//
// Every one: the endpoint accepts page and per-page arguments and applies them
// to each connected installation separately, then concatenates the results, so
// asking for "page 2" returns an arbitrary slice of each rather than the second
// page of anything. The CLI does not offer pagination it cannot honour, and
// says so in the command's automation notes.
//
// This is the one call in the CLI that depends on the token knowing which user
// created it. A token without that claim reaches the server as nobody and is
// told it has no installations.
func ListRepos(ctx context.Context, c *api.Client) ([]Repo, error) {
	var dtos []repoDTO
	if err := c.Get(ctx, "/GitProvider/Github/Repositories", &dtos); err != nil {
		return nil, err
	}

	repos := make([]Repo, 0, len(dtos))
	for _, d := range dtos {
		// A disabled repository cannot be cloned, so offering it as a deploy
		// source would be offering a build that always fails. Archived ones are
		// kept: they are read-only on the host but still perfectly deployable,
		// and they are marked so the reader can tell.
		if d.Disabled {
			continue
		}
		repos = append(repos, Repo{
			FullName: d.FullName,
			Name:     d.Name,
			Private:  d.Private,
			Language: d.Language,
			Branch:   d.DefaultBranch,
			Archived: d.Archived,
			URL:      d.HTMLURL,
			Provider: "github",
		})
	}
	return repos, nil
}
