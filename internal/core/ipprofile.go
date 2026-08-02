package core

import (
	"context"
	"fmt"
	"net/netip"
	"sort"
	"strings"

	"github.com/outplane/cli/internal/api"
)

// Who is allowed to reach an application, by address.
//
// A profile is a list of networks and a set of applications it is attached to.
// Every rule is an allow rule: the platform declares a Deny mode and reserves
// it, unimplemented, so a profile is an allowlist and attaching one to an
// application means everything not listed stops reaching it.
//
// The shape mirrors environment variable groups exactly, because the thing is
// the same shape: a team-level object, assigned to applications, removed by the
// assignment's own id rather than by the pair. The commands mirror them too, so
// somebody who has used one has used both.
//
// Two platform rules matter and both are the server's:
//
//   - Updating replaces the whole rule list, so changing one rule means sending
//     them all. That is another read-modify-write, like ports and build.
//   - A profile with assignments cannot be deleted. The server refuses and says
//     so, and the CLI relays that rather than predicting it.

// IPProfile is one profile.
type IPProfile struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`

	Rules       []IPRule `json:"rules"`
	Assignments int      `json:"assignments"`

	// AssignedApps are the applications using it, which only the detail
	// endpoint carries. They matter more than the count: attaching a profile
	// changes who can reach an application, so knowing which ones are affected
	// is the difference between a safe change and an outage.
	AssignedApps []IPAssignment `json:"assignedApps"`

	CreatedAt string `json:"createdAt"`
}

// IPRule is one network the profile lets through.
type IPRule struct {
	CIDR        string `json:"cidr"`
	Description string `json:"description"`
}

// IPAssignment is one application a profile is attached to.
type IPAssignment struct {
	// ID is the assignment's own identifier, which removing one needs.
	ID      string `json:"id"`
	AppID   string `json:"appId"`
	AppName string `json:"app"`
}

type ipProfileDTO struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Description     string `json:"description"`
	RuleCount       int    `json:"ruleCount"`
	AssignmentCount int    `json:"assignmentCount"`
	CreatedDate     string `json:"createdDate"`

	Rules []struct {
		CIDR        string `json:"cidr"`
		Description string `json:"description"`
		Mode        int    `json:"mode"`
	} `json:"rules"`

	AssignedResources []struct {
		AssignmentID string `json:"assignmentId"`
		AppID        string `json:"appId"`
		AppName      string `json:"appName"`
	} `json:"assignedResources"`
}

// ipAccessModeAllow is the only mode the platform implements. Deny is declared
// on the server and commented out as reserved, so every rule sent is an allow
// and nothing here offers a choice that would be refused.
const ipAccessModeAllow = 10

// Limits the server enforces, duplicated so that a violation names the field
// rather than arriving as one sentence about the whole request.
const (
	MaxIPProfileName        = 100
	MaxIPProfileDescription = 255
	MaxCIDRLength           = 45
)

// ListIPProfiles returns the team's profiles, sorted by name.
func ListIPProfiles(ctx context.Context, c *api.Client) ([]IPProfile, error) {
	var dtos []ipProfileDTO
	if err := c.Get(ctx, "/IPAccessProfile/GetByTeamId", &dtos); err != nil {
		return nil, err
	}

	out := make([]IPProfile, 0, len(dtos))
	for _, d := range dtos {
		out = append(out, decodeIPProfile(d))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// GetIPProfile reads one in full, which is the only way to see its rules and
// the applications it is attached to.
func GetIPProfile(ctx context.Context, c *api.Client, id string) (IPProfile, error) {
	var dto ipProfileDTO
	if err := c.Get(ctx, "/IPAccessProfile/GetById/"+id, &dto); err != nil {
		return IPProfile{}, err
	}
	return decodeIPProfile(dto), nil
}

func decodeIPProfile(d ipProfileDTO) IPProfile {
	rules := make([]IPRule, 0, len(d.Rules))
	for _, r := range d.Rules {
		rules = append(rules, IPRule{CIDR: r.CIDR, Description: r.Description})
	}
	sort.Slice(rules, func(i, j int) bool { return rules[i].CIDR < rules[j].CIDR })

	assigned := make([]IPAssignment, 0, len(d.AssignedResources))
	for _, a := range d.AssignedResources {
		assigned = append(assigned, IPAssignment{ID: a.AssignmentID, AppID: a.AppID, AppName: a.AppName})
	}
	sort.Slice(assigned, func(i, j int) bool { return assigned[i].AppName < assigned[j].AppName })

	count := d.AssignmentCount
	if count == 0 {
		count = len(assigned)
	}

	return IPProfile{
		ID:           d.ID,
		Name:         d.Name,
		Description:  d.Description,
		Rules:        rules,
		Assignments:  count,
		AssignedApps: assigned,
		CreatedAt:    serverInstant(d.CreatedDate),
	}
}

// SaveIPProfile creates a profile, or replaces one whole.
//
// The same body serves both, which is the API's shape: an update replaces the
// name, the description and every rule together, so a caller changing one rule
// has to send the rest and the command that does it reads them first.
func SaveIPProfile(ctx context.Context, c *api.Client, id string, p IPProfile) (IPProfile, error) {
	rules := make([]map[string]any, 0, len(p.Rules))
	for _, r := range p.Rules {
		rules = append(rules, map[string]any{
			"cidr":        r.CIDR,
			"description": nilIfBlank(r.Description),
			"mode":        ipAccessModeAllow,
		})
	}

	body := map[string]any{
		"name":        p.Name,
		"description": nilIfBlank(p.Description),
		"rules":       rules,
	}

	var dto ipProfileDTO
	path := "/IPAccessProfile/Create"
	if id != "" {
		path = "/IPAccessProfile/Update/" + id
		if err := c.Put(ctx, path, body, &dto); err != nil {
			return IPProfile{}, err
		}
		return decodeIPProfile(dto), nil
	}

	if err := c.Post(ctx, path, body, &dto); err != nil {
		return IPProfile{}, err
	}
	return decodeIPProfile(dto), nil
}

// DeleteIPProfile removes a profile. The server refuses while it is attached to
// anything, and that refusal is relayed rather than predicted.
func DeleteIPProfile(ctx context.Context, c *api.Client, id string) error {
	return c.Delete(ctx, "/IPAccessProfile/Delete/"+id, nil)
}

// AssignIPProfile attaches a profile to an application.
func AssignIPProfile(ctx context.Context, c *api.Client, profileID, appID string) error {
	body := map[string]any{"ipAccessProfileId": profileID, "appId": appID}
	return c.Post(ctx, "/IPAccessProfile/Assign", body, nil)
}

// UnassignIPProfile removes an attachment by its own id, which is why the
// caller has to look it up first: the pair of profile and application does not
// identify one.
func UnassignIPProfile(ctx context.Context, c *api.Client, assignmentID string) error {
	return c.Delete(ctx, "/IPAccessProfile/Unassign/"+assignmentID, nil)
}

// AppIPProfiles lists the profiles attached to an application.
func AppIPProfiles(ctx context.Context, c *api.Client, appID string) ([]IPProfile, error) {
	var dtos []ipProfileDTO
	if err := c.Get(ctx, "/IPAccessProfile/GetByAppId/"+appID, &dtos); err != nil {
		return nil, err
	}

	out := make([]IPProfile, 0, len(dtos))
	for _, d := range dtos {
		out = append(out, decodeIPProfile(d))
	}
	return out, nil
}

// FindIPProfile resolves a reference into exactly one profile, matching the id
// and then the name, as every other resource does.
func FindIPProfile(ctx context.Context, c *api.Client, ref string) (IPProfile, error) {
	profiles, err := ListIPProfiles(ctx, c)
	if err != nil {
		return IPProfile{}, err
	}

	for _, p := range profiles {
		if p.ID == ref || strings.EqualFold(p.Name, ref) {
			// The list carries no rules and no assignments, and both are what a
			// caller almost always came for.
			return GetIPProfile(ctx, c, p.ID)
		}
	}
	return IPProfile{}, &IPProfileNotFoundError{Ref: ref, Available: ipProfileNames(profiles)}
}

// IPProfileNotFoundError carries what does exist.
type IPProfileNotFoundError struct {
	Ref       string
	Available []string
}

func (e *IPProfileNotFoundError) Error() string {
	return fmt.Sprintf("no IP profile called %q in this team", e.Ref)
}

func ipProfileNames(profiles []IPProfile) []string {
	names := make([]string, 0, len(profiles))
	for _, p := range profiles {
		names = append(names, p.Name)
	}
	return names
}

// CheckIPProfile rejects what the server would, naming the field.
func CheckIPProfile(p IPProfile) error {
	name := strings.TrimSpace(p.Name)
	switch {
	case name == "":
		return usage("a name is needed", "ipprofile.name_required", "")
	case len(name) > MaxIPProfileName:
		return usage(fmt.Sprintf("that name is %d characters, and the limit is %d",
			len(name), MaxIPProfileName), "ipprofile.name_invalid", "")
	case len(p.Description) > MaxIPProfileDescription:
		return usage(fmt.Sprintf("that description is %d characters, and the limit is %d",
			len(p.Description), MaxIPProfileDescription), "ipprofile.description_invalid", "")
	}

	seen := make(map[string]bool, len(p.Rules))
	for _, r := range p.Rules {
		if err := CheckCIDR(r.CIDR); err != nil {
			return err
		}
		if len(r.Description) > MaxIPProfileDescription {
			return usage(fmt.Sprintf("the description for %s is longer than %d characters",
				r.CIDR, MaxIPProfileDescription), "ipprofile.description_invalid", "")
		}
		key := strings.ToLower(strings.TrimSpace(r.CIDR))
		if seen[key] {
			return usage(fmt.Sprintf("%s appears twice", r.CIDR), "ipprofile.rule_duplicate",
				"The server keeps one of them and there is no rule saying which.")
		}
		seen[key] = true
	}
	return nil
}

// CheckCIDR rejects an address the server would.
//
// A bare address is the common mistake and it deserves its own sentence: the
// server's message names the format and not what was wrong with this one, and
// somebody who wrote 10.0.0.1 meant 10.0.0.1/32.
func CheckCIDR(cidr string) error {
	c := strings.TrimSpace(cidr)
	switch {
	case c == "":
		return usage("a network is needed", "ipprofile.cidr_required",
			"Write it in CIDR notation, such as 10.0.0.0/8 or 203.0.113.4/32.")
	case len(c) > MaxCIDRLength:
		return usage(fmt.Sprintf("%q is longer than %d characters", short(c), MaxCIDRLength),
			"ipprofile.cidr_invalid", "")
	case !strings.Contains(c, "/"):
		if _, err := netip.ParseAddr(c); err == nil {
			return usage(fmt.Sprintf("%s is an address, not a network", c),
				"ipprofile.cidr_invalid",
				"Add the prefix length: %s/32 for one IPv4 address, /128 for one IPv6 address.", c)
		}
		return usage(fmt.Sprintf("%q is not a network", c), "ipprofile.cidr_invalid",
			"Write it in CIDR notation, such as 10.0.0.0/8.")
	}

	if _, err := netip.ParsePrefix(c); err != nil {
		return usage(fmt.Sprintf("%q is not a network", c), "ipprofile.cidr_invalid",
			"Write it in CIDR notation, such as 10.0.0.0/8 or 2001:db8::/32.")
	}
	return nil
}

// FindAssignment locates the attachment to one application, which unassigning
// needs and which the pair alone does not identify.
func FindAssignment(p IPProfile, appName, appID string) (IPAssignment, bool) {
	for _, a := range p.AssignedApps {
		if a.AppID == appID || strings.EqualFold(a.AppName, appName) {
			return a, true
		}
	}
	return IPAssignment{}, false
}

func nilIfBlank(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}
