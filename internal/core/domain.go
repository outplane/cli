package core

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/outplane/cli/internal/api"
)

// Custom domains.
//
// A domain is a route rather than a name: the same host can carry several
// paths, each going to a different application, so what the API stores and what
// these commands act on is the pair. That is why every command takes --path and
// why a host with more than one route cannot be addressed by host alone.
//
// Two rules come from the platform and shape the commands:
//
//   - A route is bound to a port record, not to an application. Attaching needs
//     the port's id, which `app get` reports.
//   - The port has to be HTTP and its application has to have deployed
//     successfully at least once. A domain pointed at something that has never
//     run would resolve to nothing.

// Domain is one route: a host, a path, and where it goes.
type Domain struct {
	ID   string `json:"id"`
	Host string `json:"host"`
	Path string `json:"path"`

	// App and PortID are empty while the route exists but points nowhere, which
	// is the state a domain is created in.
	App    string `json:"app"`
	AppID  string `json:"appId"`
	PortID string `json:"portId"`

	// SSL is always true: the platform issues a certificate for every custom
	// domain. It is reported because a reader asks, not because it varies.
	SSL bool `json:"ssl"`

	// URL is the address the route answers on, which is the host, the path and
	// the scheme put together rather than something the API stores.
	URL string `json:"url"`
}

type domainDTO struct {
	ID        string `json:"id"`
	Domain    string `json:"domain"`
	Path      string `json:"path"`
	AppID     string `json:"appId"`
	AppName   string `json:"appName"`
	AppPortID string `json:"appPortId"`
	SSL       bool   `json:"ssl"`
}

// DNS targets a reader has to put in their zone.
//
// Both are the platform's published addresses, and a domain command without
// them is a command that tells somebody to go and look elsewhere. A host with a
// subdomain gets a CNAME; an apex domain cannot have one, so it gets an A
// record.
const (
	DNSCNAMETarget = "domains-management.outplane.app"
	DNSATarget     = "162.55.159.8"
)

// DNSRecord is what to create in a zone for a host.
type DNSRecord struct {
	Type  string `json:"type"`
	Name  string `json:"name"`
	Value string `json:"value"`
}

// RecordFor works out which record a host needs. Purely local: there is nothing
// to ask the server, and a reader setting up DNS wants the answer before they
// have created anything.
func RecordFor(host string) DNSRecord {
	labels := strings.Split(strings.TrimSpace(host), ".")
	if len(labels) > 2 {
		return DNSRecord{Type: "CNAME", Name: labels[0], Value: DNSCNAMETarget}
	}
	return DNSRecord{Type: "A", Name: "@", Value: DNSATarget}
}

// ListDomains returns the team's routes, sorted by host and then by path with
// the root first, which is the order somebody reads them in.
func ListDomains(ctx context.Context, c *api.Client) ([]Domain, error) {
	var dtos []domainDTO
	if err := c.Get(ctx, "/CustomDomain/GetAll", &dtos); err != nil {
		return nil, err
	}

	domains := make([]Domain, 0, len(dtos))
	for _, d := range dtos {
		domains = append(domains, Domain{
			ID:     d.ID,
			Host:   d.Domain,
			Path:   NormalizeDomainPath(d.Path),
			App:    d.AppName,
			AppID:  d.AppID,
			PortID: d.AppPortID,
			SSL:    d.SSL,
			URL:    domainURL(d.Domain, NormalizeDomainPath(d.Path), d.SSL),
		})
	}

	sort.Slice(domains, func(i, j int) bool {
		if domains[i].Host != domains[j].Host {
			return domains[i].Host < domains[j].Host
		}
		if domains[i].Path == "/" {
			return true
		}
		if domains[j].Path == "/" {
			return false
		}
		return domains[i].Path < domains[j].Path
	})
	return domains, nil
}

// AddDomain registers a route, optionally pointing it at a port.
func AddDomain(ctx context.Context, c *api.Client, host, path, portID string) (Domain, error) {
	body := map[string]any{"domain": host, "path": path}
	if portID != "" {
		body["appPortId"] = portID
	}

	var dto domainDTO
	if err := c.Post(ctx, "/CustomDomain/Create", body, &dto); err != nil {
		return Domain{}, err
	}
	return Domain{
		ID: dto.ID, Host: dto.Domain, Path: NormalizeDomainPath(dto.Path),
		App: dto.AppName, AppID: dto.AppID, PortID: dto.AppPortID, SSL: dto.SSL,
		URL: domainURL(dto.Domain, NormalizeDomainPath(dto.Path), dto.SSL),
	}, nil
}

// PointDomain sends a route to a port, or to nowhere when portID is empty.
//
// The same endpoint does both, and the path has to be sent either way: it is a
// full replacement, so omitting the path would move the route to the root.
func PointDomain(ctx context.Context, c *api.Client, domainID, portID, path string) (Domain, error) {
	body := map[string]any{"path": path}
	if portID != "" {
		body["appPortId"] = portID
	} else {
		body["appPortId"] = nil
	}

	var dto domainDTO
	if err := c.Put(ctx, "/CustomDomain/Update/"+domainID, body, &dto); err != nil {
		return Domain{}, err
	}
	return Domain{
		ID: dto.ID, Host: dto.Domain, Path: NormalizeDomainPath(dto.Path),
		App: dto.AppName, AppID: dto.AppID, PortID: dto.AppPortID, SSL: dto.SSL,
		URL: domainURL(dto.Domain, NormalizeDomainPath(dto.Path), dto.SSL),
	}, nil
}

// RemoveDomain deletes a route.
func RemoveDomain(ctx context.Context, c *api.Client, domainID string) error {
	return c.Delete(ctx, "/CustomDomain/Delete/"+domainID, nil)
}

// FindDomain resolves a host, and a path when the host has more than one route.
//
// A host with one route needs no path, which is the common case. A host with
// several is ambiguous by design rather than by accident, so the caller is told
// which paths exist instead of one being chosen.
func FindDomain(ctx context.Context, c *api.Client, host, path string) (Domain, error) {
	domains, err := ListDomains(ctx, c)
	if err != nil {
		return Domain{}, err
	}

	var matches []Domain
	for _, d := range domains {
		if strings.EqualFold(d.Host, host) || d.ID == host {
			matches = append(matches, d)
		}
	}

	if path != "" {
		wanted := NormalizeDomainPath(path)
		for _, d := range matches {
			if d.Path == wanted {
				return d, nil
			}
		}
		return Domain{}, &DomainNotFoundError{Host: host, Path: wanted, Available: domainHosts(domains)}
	}

	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return Domain{}, &DomainNotFoundError{Host: host, Available: domainHosts(domains)}
	default:
		return Domain{}, &AmbiguousDomainError{Host: host, Paths: domainPaths(matches)}
	}
}

// DomainNotFoundError carries what does exist.
type DomainNotFoundError struct {
	Host      string
	Path      string
	Available []string
}

func (e *DomainNotFoundError) Error() string {
	if e.Path != "" {
		return fmt.Sprintf("no route for %s at %s", e.Host, e.Path)
	}
	return fmt.Sprintf("no domain called %q in this team", e.Host)
}

// AmbiguousDomainError means the host carries several routes and the path says
// which one.
type AmbiguousDomainError struct {
	Host  string
	Paths []string
}

func (e *AmbiguousDomainError) Error() string {
	return fmt.Sprintf("%s has %d routes", e.Host, len(e.Paths))
}

func domainHosts(domains []Domain) []string {
	seen := map[string]bool{}
	var hosts []string
	for _, d := range domains {
		if !seen[d.Host] {
			seen[d.Host] = true
			hosts = append(hosts, d.Host)
		}
	}
	return hosts
}

func domainPaths(domains []Domain) []string {
	paths := make([]string, 0, len(domains))
	for _, d := range domains {
		paths = append(paths, d.Path)
	}
	return paths
}

// acmePath is reserved for certificate renewal. A route on it would intercept
// the challenge and the certificate would stop renewing, silently, weeks later.
const acmePath = "/.well-known/acme-challenge"

// NormalizeDomainPath matches the server's rule: trailing slashes go, empty
// becomes the root.
func NormalizeDomainPath(path string) string {
	p := strings.TrimSpace(path)
	if p == "" {
		return "/"
	}
	p = strings.TrimRight(p, "/")
	if p == "" {
		return "/"
	}
	return p
}

// CheckHost rejects what the server or DNS would.
func CheckHost(host string) error {
	h := strings.TrimSpace(host)

	switch {
	case h == "":
		return usage("a domain is needed", "domain.host_required", "")
	case strings.HasPrefix(strings.ToLower(h), "http://"), strings.HasPrefix(strings.ToLower(h), "https://"):
		return usage("give the host only, without a scheme", "domain.host_invalid",
			"For example app.example.com rather than https://app.example.com.")
	case len(h) > 253:
		return usage("that host is longer than 253 characters", "domain.host_invalid", "")
	case strings.Count(h, ".") < 1:
		return usage(fmt.Sprintf("%q is not a full domain", h), "domain.host_invalid",
			"A full domain has at least two labels, such as example.com.")
	case !isHostChars(h):
		return usage(fmt.Sprintf("%q has characters a host cannot", h), "domain.host_invalid",
			"Letters, numbers, dots and hyphens only.")
	}
	return nil
}

// CheckDomainPath rejects a path the server would, and the one that would break
// certificate renewal.
func CheckDomainPath(path string) error {
	p := strings.TrimSpace(path)
	switch {
	case p == "":
		return nil
	case !strings.HasPrefix(p, "/"):
		return usage(fmt.Sprintf("%q does not start with /", p), "domain.path_invalid", "")
	case len(p) > 500:
		return usage("that path is longer than 500 characters", "domain.path_invalid", "")
	case NormalizeDomainPath(p) == acmePath:
		return usage("that path is reserved", "domain.path_reserved",
			"The platform answers certificate challenges there. A route on it would stop "+
				"the certificate renewing.")
	}
	return nil
}

func isHostChars(h string) bool {
	for _, r := range h {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-':
		default:
			return false
		}
	}
	return true
}
