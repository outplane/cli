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
	register("repos", repoList)
}

// repoList shows what this account can deploy from.
//
// The connect address is printed on every run, not only on an empty one. The
// question people actually arrive with is "why is my repository not in this
// list", and the answer is the same address as "nothing is here at all".
func repoList(ctx context.Context, req Request) (output.Table, error) {
	client, err := req.CLI.APIClient()
	if err != nil {
		return output.Table{}, err
	}

	repos, err := core.ListRepos(ctx, client)
	if err != nil {
		return output.Table{}, explainNotConnected(err)
	}

	search := strings.ToLower(strings.TrimSpace(req.Flags.String("search")))

	table := output.Table{Columns: []string{"fullName", "private", "defaultBranch", "language"}}
	for _, r := range repos {
		if search != "" && !strings.Contains(strings.ToLower(r.FullName), search) {
			continue
		}
		table.Rows = append(table.Rows, map[string]any{
			"fullName":      r.FullName,
			"name":          r.Name,
			"provider":      r.Provider,
			"private":       r.Private,
			"defaultBranch": r.Branch,
			"language":      nilIfEmpty(r.Language),
			"archived":      r.Archived,
			"url":           r.URL,
		})
	}
	table.Total = len(table.Rows)

	if len(table.Rows) == 0 && search != "" {
		req.CLI.Out.Note("No repository matching %q. %d are connected.", search, len(repos))
	}
	table.Footer = "Missing one? Grant access at " + core.ConnectURL

	return table, nil
}

// explainNotConnected turns both of the server's empty answers into one that
// says what to do.
//
// There are two, and neither is usable as it arrives. No connection at all is a
// 400 reading "User has no installations", which describes the database rather
// than the person. A connection that covers no repositories is a 404 with an
// empty message, which says nothing whatsoever. Both mean the same thing to
// whoever ran the command, and both are fixed at the same address.
func explainNotConnected(err error) error {
	var e *clierr.Error
	if !errors.As(err, &e) || (e.Kind != clierr.KindUsage && e.Kind != clierr.KindNotFound) {
		return err
	}

	return clierr.New(clierr.KindUsage, "no repositories are connected to this account").
		WithCode("repos.not_connected").
		WithHint("Connecting happens in a browser and cannot be done from a terminal. "+
			"Open %s, choose the repositories to share, then run this again.", core.ConnectURL).
		WithDetail("connectUrl", core.ConnectURL).
		WithDetail("reason", e.Message)
}
