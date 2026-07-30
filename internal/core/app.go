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
		})
	}
	return apps, nil
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
