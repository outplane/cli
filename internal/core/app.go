// Package core holds the domain operations the CLI performs.
//
// Nothing here knows about terminals, flags, colour or cobra. Functions take
// plain arguments and return plain data, which means they can be tested
// without a network and reused unchanged by anything that needs them later,
// including an MCP tool server.
//
// The dependency direction is one way and enforced by review: core may import
// api and clierr; nothing in core may import cmd, output or help.
package core

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/outplane/cli/internal/api"
)

// App is one application, as the CLI thinks about it.
//
// This is not the API's DTO. It is the shape commands want, which lets the
// wire format change without every command changing with it. The API has no
// OpenAPI response schemas today, so these types are written by hand against
// observed responses; keeping them separate is what makes that survivable.
type App struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`

	// Status is the app's effective state and is the field to branch on.
	//
	// Pausing outranks the deployment status here, which mirrors the console's
	// rule: a paused app goes on reporting whatever its last deployment ended
	// as, so a caller reading DeploymentStatus alone would call a stopped app
	// "ready" and act on it.
	Status string `json:"status"`

	// DeploymentStatus is the raw state of the last deployment, kept so that
	// pausing hides nothing. For a paused app this is what it will return to.
	DeploymentStatus string `json:"deploymentStatus"`
	Paused           bool   `json:"paused"`

	// Instances is the configured replica count, which does not change when an
	// app is paused. Whether it is currently running is what Status says.
	Instances int    `json:"instances"`
	Size      string `json:"size"`
	UpdatedAt string `json:"updatedAt"`

	// Source is where the app's image comes from: "github" or
	// "container-registry". It decides whether an explicit image reference
	// means anything, so `deploy --image` can refuse a Git-sourced app here
	// rather than after a round trip.
	Source string `json:"source"`
}

// appOverviewDTO is the wire shape of GET /App/GetAppsByTeamId.
//
// Field names follow the API's camelCase output. Unknown fields are ignored by
// encoding/json, which is what makes an older CLI survive a server that has
// started returning more: a new field is additive and invisible here.
//
// There is no URL in this response, and none can be derived from it. A public
// address is {name}-{port}-{teamSlug}.outplane.app, the port comes from the
// app's endpoints, and endpoints are a separate request per app. Listing
// therefore reports no URL rather than guessing one that would 404, or turning
// one list call into one call per application.
type appOverviewDTO struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`

	// Status is an AppDeploymentStatus, and it arrives as an integer: the API
	// serialises enums by value. Decoding lives in enum.go.
	Status   int  `json:"status"`
	IsPaused bool `json:"isPaused"`

	// SourceProvider is a SourceProvider enum, as an integer. See enum.go.
	SourceProvider int `json:"sourceProvider"`

	MinScale     int    `json:"minScale"`
	InstanceType string `json:"instanceType"`

	// LastModifiedDate stays null until an app is first changed, so CreatedDate
	// is read as the fallback. The console pairs these two the same way.
	LastModifiedDate string `json:"lastModifiedDate"`
	CreatedDate      string `json:"createdDate"`
}

// ListApps returns every application in the current team.
//
// The API returns the whole set in one response: there is no pagination, no
// filtering and no server-side search. Callers therefore always receive a
// complete list, and any narrowing happens here rather than being requested
// from the server.
func ListApps(ctx context.Context, c *api.Client, search string) ([]App, error) {
	var dtos []appOverviewDTO
	if err := c.Get(ctx, "/App/GetAppsByTeamId", &dtos); err != nil {
		return nil, err
	}

	apps := make([]App, 0, len(dtos))
	for _, d := range dtos {
		if !matches(d, search) {
			continue
		}
		deployment := deploymentStatusNames.name(d.Status)

		// See App.Status: pausing outranks the deployment state.
		status := deployment
		if d.IsPaused {
			status = "paused"
		}

		updated := d.LastModifiedDate
		if updated == "" {
			updated = d.CreatedDate
		}

		apps = append(apps, App{
			ID:               d.ID,
			Name:             d.Name,
			DisplayName:      d.DisplayName,
			Status:           status,
			DeploymentStatus: deployment,
			Paused:           d.IsPaused,
			Instances:        d.MinScale,
			Size:             d.InstanceType,
			UpdatedAt:        serverInstant(updated),
			Source:           sourceProviderNames.name(d.SourceProvider),
		})
	}
	return apps, nil
}

// FindApp resolves a reference typed by a person into exactly one application.
//
// This is the reference implementation for every argument declared with
// Resolves. The API has no lookup-by-name endpoint anywhere: every path
// parameter is a GUID, so turning "checkout" into an id is entirely the
// client's job, and the only way to do it is to list and match.
//
// Matching is exact and never fuzzy, in any of the three fields. Resolving
// "check" to "checkout" would work until the day somebody creates "checkers",
// and the command being resolved for might be `app delete`. A near miss is
// reported as a miss, with the available names listed so the correction is one
// read away rather than another command away.
func FindApp(ctx context.Context, c *api.Client, ref string) (App, error) {
	apps, err := ListApps(ctx, c, "")
	if err != nil {
		return App{}, err
	}

	// An id is unambiguous by construction, so it settles the question before
	// any name is considered.
	for _, a := range apps {
		if a.ID == ref {
			return a, nil
		}
	}

	// The name is unique within a team and is what appears in URLs.
	for _, a := range apps {
		if a.Name == ref {
			return a, nil
		}
	}

	// The display name is editable and NOT unique, which is why it is tried
	// last and why a tie has to be refused rather than resolved. Picking the
	// first of two would be a coin flip the caller never sees.
	var byDisplay []App
	for _, a := range apps {
		if a.DisplayName == ref {
			byDisplay = append(byDisplay, a)
		}
	}
	switch len(byDisplay) {
	case 1:
		return byDisplay[0], nil
	case 0:
		return App{}, &AppNotFoundError{Ref: ref, Available: appNames(apps)}
	default:
		return App{}, &AmbiguousAppError{Ref: ref, Matches: byDisplay}
	}
}

// AppNotFoundError carries the names that do exist, because "no such app" on
// its own sends the reader off to run another command to find out what is
// there.
type AppNotFoundError struct {
	Ref       string
	Available []string
}

func (e *AppNotFoundError) Error() string {
	return fmt.Sprintf("no application called %q in this team", e.Ref)
}

// AmbiguousAppError means a display name is shared. It carries the matches so
// the caller can be told to use a name or an id instead.
type AmbiguousAppError struct {
	Ref     string
	Matches []App
}

func (e *AmbiguousAppError) Error() string {
	return fmt.Sprintf("%d applications share the display name %q", len(e.Matches), e.Ref)
}

// Names lists the unique names of the tied applications, which are what the
// caller should use instead.
func (e *AmbiguousAppError) Names() []string { return appNames(e.Matches) }

func appNames(apps []App) []string {
	out := make([]string, 0, len(apps))
	for _, a := range apps {
		out = append(out, a.Name)
	}
	sort.Strings(out)
	return out
}

// matches implements --search: a case-insensitive substring test against both
// the immutable name and the editable display name, because a user searching
// for "checkout" should find it whichever one they remember.
func matches(d appOverviewDTO, search string) bool {
	if search == "" {
		return true
	}
	needle := strings.ToLower(search)
	return strings.Contains(strings.ToLower(d.Name), needle) ||
		strings.Contains(strings.ToLower(d.DisplayName), needle)
}
