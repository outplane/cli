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

	// LastDeployedAt is when the last deployment started, and is the field that
	// answers "when did anything last happen here".
	//
	// UpdatedAt is not that field, which is why both exist: it is the app
	// record's own modification time, so editing a variable moves it and
	// deploying does not. The console draws its "Last deploy" column from the
	// same value this reads.
	LastDeployedAt string `json:"lastDeployedAt"`
	UpdatedAt      string `json:"updatedAt"`

	// CPULimitMillicores and MemoryLimitMB are what one instance may use. They
	// come from the instance type and are per pod rather than per app, which is
	// what makes a usage percentage mean the same thing at any replica count.
	CPULimitMillicores int `json:"cpuLimitMillicores"`
	MemoryLimitMB      int `json:"memoryLimitMb"`

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

	MinScale           int    `json:"minScale"`
	InstanceType       string `json:"instanceType"`
	CPULimitMillicores int    `json:"cpuLimitMillicores"`
	MemoryLimitMB      int    `json:"memoryLimitMb"`

	// LastModifiedDate stays null until an app is first changed, so CreatedDate
	// is read as the fallback. The console pairs these two the same way.
	LastModifiedDate string `json:"lastModifiedDate"`
	CreatedDate      string `json:"createdDate"`

	// LastDeployment is the whole last deployment record, of which one field is
	// wanted: when it started. It rides along in the list response, so the date
	// costs nothing extra to report.
	LastDeployment struct {
		CreatedDate string `json:"createdDate"`
	} `json:"lastDeployment"`
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

		// An app always has a deployment, made when it was created, so the
		// fallback is for a record the server could not attach one to rather
		// than for a state a user can reach. The console falls back the same
		// way.
		deployed := d.LastDeployment.CreatedDate
		if deployed == "" {
			deployed = d.CreatedDate
		}

		apps = append(apps, App{
			ID:                 d.ID,
			Name:               d.Name,
			DisplayName:        d.DisplayName,
			Status:             status,
			DeploymentStatus:   deployment,
			Paused:             d.IsPaused,
			Instances:          d.MinScale,
			Size:               d.InstanceType,
			CPULimitMillicores: d.CPULimitMillicores,
			MemoryLimitMB:      d.MemoryLimitMB,
			LastDeployedAt:     serverInstant(deployed),
			UpdatedAt:          serverInstant(updated),
			Source:             sourceProviderNames.name(d.SourceProvider),
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

// AppDetail is one application, with what the list cannot afford to fetch.
//
// The list endpoint returns every application in one response and so cannot
// carry per-application detail; this is a second request for one of them. The
// difference that matters is Endpoints, and through them the public address,
// which is the question `app list` deliberately leaves unanswered.
type AppDetail struct {
	App

	// Repository and ImageRef are the two forms a source takes, and exactly one
	// of them is ever set. The API keeps both in a single column, so an app
	// built from a container image reports "nginx:latest" as its repository;
	// splitting them here means a reader never has to know that, and matches
	// the branch/imageRef pair a deployment already reports.
	Repository string `json:"repository"`
	Branch     string `json:"branch"`
	ImageRef   string `json:"imageRef"`
	SourceURL  string `json:"sourceUrl"`

	// PublicSource says the repository needs no credential to read. It is
	// always true for a container-registry app, which has no repository.
	PublicSource bool `json:"publicSource"`

	BuildMethod  string `json:"buildMethod"`
	Directory    string `json:"directory"`
	StartCommand string `json:"startCommand"`

	// URL is where the app can be reached, which is what somebody means by "the
	// app's URL". Endpoints has the rest.
	URL       string     `json:"url"`
	Endpoints []Endpoint `json:"endpoints"`

	CommitMessage string `json:"commitMessage"`
	CreatedAt     string `json:"createdAt"`
}

// Endpoint is one port an application serves.
type Endpoint struct {
	Port   int    `json:"port"`
	Scheme string `json:"scheme"`
	Public bool   `json:"public"`

	// URL is the platform address for this port, present only while the port is
	// public. A private port with a custom domain is still reachable, at that
	// domain and nowhere else.
	URL string `json:"url"`

	// CustomDomains are full addresses rather than bare host names, because the
	// scheme and the path are both configurable per domain and a host name on
	// its own is not something a reader can open or curl.
	CustomDomains []string `json:"customDomains"`
}

type appDetailDTO struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`

	SourceProvider   int    `json:"sourceProvider"`
	SourceRepository string `json:"sourceRepository"`
	SourceURL        string `json:"sourceUrl"`
	IsPublicSource   bool   `json:"isPublicSource"`
	DefaultBranch    string `json:"defaultBranch"`

	BuildMethod  int    `json:"buildMethod"`
	Directory    string `json:"directory"`
	StartCommand string `json:"startCommand"`

	MinScale     int    `json:"minScale"`
	InstanceType string `json:"instanceType"`
	IsPaused     bool   `json:"isPaused"`

	LastDeploymentStatus int    `json:"lastDeploymentStatus"`
	LastDeploymentDate   string `json:"lastDeploymentDate"`
	CommitMessage        string `json:"commitMessage"`

	CreatedDate      string `json:"createdDate"`
	LastModifiedDate string `json:"lastModifiedDate"`

	AppPorts []struct {
		Port          int    `json:"port"`
		Scheme        int    `json:"scheme"`
		IsPublic      bool   `json:"isPublic"`
		PublicURL     string `json:"publicUrl"`
		CustomDomains []struct {
			Domain string `json:"domain"`
			Path   string `json:"path"`
			SSL    bool   `json:"ssl"`
		} `json:"customDomains"`
	} `json:"appPorts"`
}

// GetApp fetches one application by id.
func GetApp(ctx context.Context, c *api.Client, appID string) (AppDetail, error) {
	var d appDetailDTO
	if err := c.Get(ctx, "/App/GetAppById/"+appID, &d); err != nil {
		return AppDetail{}, err
	}

	deployment := deploymentStatusNames.name(d.LastDeploymentStatus)
	status := deployment
	if d.IsPaused {
		status = "paused"
	}

	updated := d.LastModifiedDate
	if updated == "" {
		updated = d.CreatedDate
	}

	detail := AppDetail{
		App: App{
			ID:               d.ID,
			Name:             d.Name,
			DisplayName:      d.DisplayName,
			Status:           status,
			DeploymentStatus: deployment,
			Paused:           d.IsPaused,
			Instances:        d.MinScale,
			Size:             d.InstanceType,
			LastDeployedAt:   serverInstant(d.LastDeploymentDate),
			UpdatedAt:        serverInstant(updated),
			Source:           sourceProviderNames.name(d.SourceProvider),
		},
		Branch:        d.DefaultBranch,
		SourceURL:     d.SourceURL,
		PublicSource:  d.IsPublicSource,
		BuildMethod:   buildMethodNames.name(d.BuildMethod),
		Directory:     d.Directory,
		StartCommand:  d.StartCommand,
		CommitMessage: d.CommitMessage,
		CreatedAt:     serverInstant(d.CreatedDate),
		Endpoints:     make([]Endpoint, 0, len(d.AppPorts)),
	}

	if detail.Source == SourceContainerRegistry {
		detail.ImageRef = d.SourceRepository
	} else {
		detail.Repository = d.SourceRepository
	}

	for _, p := range d.AppPorts {
		domains := make([]string, 0, len(p.CustomDomains))
		for _, cd := range p.CustomDomains {
			domains = append(domains, domainURL(cd.Domain, cd.Path, cd.SSL))
		}
		e := Endpoint{
			Port:          p.Port,
			Scheme:        schemeNames.name(p.Scheme),
			Public:        p.IsPublic,
			CustomDomains: domains,
		}
		if p.IsPublic {
			e.URL = p.PublicURL
		}
		detail.Endpoints = append(detail.Endpoints, e)

		// The first address the app answers on wins. The platform one is
		// preferred because it always exists once a port is public; a custom
		// domain is the fallback rather than the second choice, since a private
		// port with a domain attached is reachable at that domain only, and
		// reporting no URL for it would be wrong rather than cautious.
		if detail.URL == "" {
			if e.URL != "" {
				detail.URL = e.URL
			} else if len(domains) > 0 {
				detail.URL = domains[0]
			}
		}
	}

	return detail, nil
}

// domainURL turns a custom domain record into an address that can be opened.
//
// The path is stored as "/" when there is none, and a URL ending in a bare
// slash is noise, so that one case is trimmed and any real path is kept.
func domainURL(domain, path string, ssl bool) string {
	scheme := "http"
	if ssl {
		scheme = "https"
	}
	if path == "/" {
		path = ""
	}
	return scheme + "://" + domain + path
}

// DeleteApp removes an application.
//
// The platform refuses while a custom domain, an attached volume or a
// deployment in flight still exists, and answers all three with the same shape
// of error. Naming which one is the caller's job; see the delete command.
func DeleteApp(ctx context.Context, c *api.Client, appID string) error {
	return c.Delete(ctx, "/App/DeleteApplication/"+appID, nil)
}
