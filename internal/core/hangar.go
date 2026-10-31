package core

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/outplane/cli/internal/api"
)

// Hangars.
//
// A Hangar keeps one terminal session running for as long as it runs, and the
// CLI's part is to join it. The transport is the one app shell uses, so a
// Hangar session is a Shell like any other. What differs is who decides what is
// on the far end: nothing is asked for here, the server picks the session, and
// leaving it stops nothing.
//
// Around the session there is a record like an application's: a name from the
// same namespace, a state read the same way, and a size and a disk fixed when it
// is created. The list, the detail and creation all return that record in one
// shape.

// Hangar is one Hangar, as the CLI thinks about it. See App for why this is not
// the API's DTO.
type Hangar struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`

	// Status follows App.Status: pausing outranks the deployment state.
	Status string `json:"status"`
	Paused bool   `json:"paused"`

	// Agents are the coding agents installed in it, by catalog id. Empty for a
	// Hangar that is a terminal only.
	Agents []string `json:"agents"`

	Size   string `json:"size"`
	DiskGB int    `json:"diskGb"`

	// IdleTimeoutMinutes is nil for never. A pointer, because zero minutes and
	// never are different answers.
	IdleTimeoutMinutes *int `json:"idleTimeoutMinutes"`

	CreatedAt string `json:"createdAt"`
}

// hangarDTO is the wire shape of a Hangar, which the list, the detail and
// creation all return.
type hangarDTO struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`

	// Status is an AppDeploymentStatus, as an integer. See enum.go.
	Status   int  `json:"status"`
	IsPaused bool `json:"isPaused"`

	Agents             []string `json:"agents"`
	InstanceType       string   `json:"instanceType"`
	DiskSizeInGb       int      `json:"diskSizeInGb"`
	IdleTimeoutMinutes *int     `json:"idleTimeoutMinutes"`
	CreatedDate        string   `json:"createdDate"`
}

func decodeHangar(d hangarDTO) Hangar {
	status := deploymentStatusNames.name(d.Status)
	if d.IsPaused {
		status = "paused"
	}

	// Never null on the way out, so that --json reports [] for a terminal-only
	// Hangar rather than a null a reader has to special-case.
	agents := d.Agents
	if agents == nil {
		agents = []string{}
	}

	return Hangar{
		ID:                 d.ID,
		Name:               d.Name,
		DisplayName:        d.DisplayName,
		Status:             status,
		Paused:             d.IsPaused,
		Agents:             agents,
		Size:               d.InstanceType,
		DiskGB:             d.DiskSizeInGb,
		IdleTimeoutMinutes: d.IdleTimeoutMinutes,
		CreatedAt:          serverInstant(d.CreatedDate),
	}
}

// ListHangars returns every Hangar in the current team. Like the application
// list, it is the whole set in one response.
func ListHangars(ctx context.Context, c *api.Client) ([]Hangar, error) {
	var dtos []hangarDTO
	if err := c.Get(ctx, "/Hangar/GetHangarsByTeamId", &dtos); err != nil {
		return nil, err
	}

	hangars := make([]Hangar, 0, len(dtos))
	for _, d := range dtos {
		hangars = append(hangars, decodeHangar(d))
	}
	return hangars, nil
}

// FindHangar resolves a reference typed by a person into exactly one Hangar.
//
// The rules are FindApp's, for the same reasons: the id first, then the name,
// then a display name only when exactly one Hangar carries it.
func FindHangar(ctx context.Context, c *api.Client, ref string) (Hangar, error) {
	hangars, err := ListHangars(ctx, c)
	if err != nil {
		return Hangar{}, err
	}

	for _, h := range hangars {
		if h.ID == ref {
			return h, nil
		}
	}

	for _, h := range hangars {
		if h.Name == ref {
			return h, nil
		}
	}

	var byDisplay []Hangar
	for _, h := range hangars {
		if h.DisplayName == ref {
			byDisplay = append(byDisplay, h)
		}
	}
	switch len(byDisplay) {
	case 1:
		return byDisplay[0], nil
	case 0:
		return Hangar{}, &HangarNotFoundError{Ref: ref, Available: hangarNames(hangars)}
	default:
		return Hangar{}, &AmbiguousHangarError{Ref: ref, Matches: byDisplay}
	}
}

// HangarNotFoundError carries what does exist.
type HangarNotFoundError struct {
	Ref       string
	Available []string
}

func (e *HangarNotFoundError) Error() string {
	return fmt.Sprintf("no Hangar called %q in this team", e.Ref)
}

// AmbiguousHangarError means a display name is shared.
type AmbiguousHangarError struct {
	Ref     string
	Matches []Hangar
}

func (e *AmbiguousHangarError) Error() string {
	return fmt.Sprintf("%d Hangars share the display name %q", len(e.Matches), e.Ref)
}

// Names lists the unique names of the tied Hangars, which are what the caller
// should use instead.
func (e *AmbiguousHangarError) Names() []string { return hangarNames(e.Matches) }

func hangarNames(hangars []Hangar) []string {
	out := make([]string, 0, len(hangars))
	for _, h := range hangars {
		out = append(out, h.Name)
	}
	sort.Strings(out)
	return out
}

// ConnectHangar joins the session a Hangar keeps running.
//
// Nothing is asked for beyond which Hangar. The server decides what the session
// is, and a Hangar that is not running is refused with a conflict before
// anything opens.
func ConnectHangar(ctx context.Context, c *api.Client, id string) (*Shell, error) {
	return dialShell(ctx, c, id, url.Values{},
		"The Hangar may have restarted or stopped. Connect again to rejoin its session.")
}

// Creating a Hangar.
//
// One request makes the record, its disk and its variables, and starts it. The
// size and the disk are not part of the request: the platform decides them, so
// the CLI reports what came back rather than what it expected.
//
// As with an application, the rules below are the server's, restated so that a
// mistake names its field before anything is sent and --dry-run means something.
// The exceptions are the name being taken and the team's limit, which only the
// server knows.

// HangarAgents is the catalog of coding agents a Hangar can install, by id.
//
// The list is the server's and is duplicated here for the reason InstanceTypes
// is: a wrong id is refused with the alternatives in hand rather than after a
// round trip. An agent the catalog gained after this release is refused by it,
// and the message says where the list came from.
var HangarAgents = []string{"codex", "claude-code", "pi", "omp", "devin", "opencode", "cursor"}

// DefaultHangarAgents are installed when none are named, the same preselection
// the console's form makes.
var DefaultHangarAgents = []string{"claude-code", "codex"}

// HangarIdleTimeouts are the idle timeouts a new Hangar may choose, in minutes.
// Never is always allowed besides these.
var HangarIdleTimeouts = []int{30}

// NewHangar is everything needed to create a Hangar.
type NewHangar struct {
	Name string

	// Agents are catalog ids. Empty means a terminal only.
	Agents []string

	Env map[string]string

	// EnvGroupIDs are ids, already resolved, in the order they were given. The
	// order matters: when two groups set the same key, it decides which wins.
	EnvGroupIDs []string

	// IdleTimeoutMinutes is nil for never.
	IdleTimeoutMinutes *int
}

// CreateHangar creates a Hangar and starts it.
//
// A Hangar that is created but cannot start is still returned, with status
// failed: the record exists and holds its name, which is the server's rule for
// an application whose first deployment fails too.
func CreateHangar(ctx context.Context, c *api.Client, h NewHangar) (Hangar, error) {
	var dto hangarDTO
	if err := c.Post(ctx, "/Hangar/CreateHangar", h.body(), &dto); err != nil {
		return Hangar{}, err
	}
	return decodeHangar(dto), nil
}

// body builds the request.
//
// Every collection is sent even when empty, and never as null: the server
// requires all three and reads an empty one as "none", so a nil slice encoded
// as null would be refused as missing. The idle timeout is the opposite case,
// where null is the value that means never.
func (h NewHangar) body() map[string]any {
	agents := h.Agents
	if agents == nil {
		agents = []string{}
	}
	env := h.Env
	if env == nil {
		env = map[string]string{}
	}
	groups := h.EnvGroupIDs
	if groups == nil {
		groups = []string{}
	}

	return map[string]any{
		"name":                 h.Name,
		"agents":               agents,
		"environmentVariables": env,
		"envVariableGroupIds":  groups,
		"idleTimeoutMinutes":   h.IdleTimeoutMinutes,
	}
}

// DeleteHangar removes a Hangar and its disk.
func DeleteHangar(ctx context.Context, c *api.Client, id string) error {
	return c.Delete(ctx, "/Hangar/DeleteHangar/"+id, nil)
}

// Check reports the first thing the server would refuse, one at a time as
// NewApp.Check does.
func (h NewHangar) Check() error {
	if err := checkHangarName(h.Name); err != nil {
		return err
	}
	if err := checkHangarAgents(h.Agents); err != nil {
		return err
	}
	if h.IdleTimeoutMinutes != nil && !containsInt(HangarIdleTimeouts, *h.IdleTimeoutMinutes) {
		return usage(fmt.Sprintf("%d minutes is not an idle timeout a Hangar can have", *h.IdleTimeoutMinutes),
			"hangar.idle_timeout_invalid", "Use one of: %s.", strings.Join(IdleTimeoutChoices(), ", "))
	}
	return nil
}

// checkHangarName is the application name rule, because a Hangar takes its name
// from the same namespace and the server holds both to one rule. See
// NewApp.checkName for why each part of it exists.
func checkHangarName(name string) error {
	name = strings.TrimSpace(name)

	switch {
	case len(name) < 5:
		return usage(fmt.Sprintf("%q is shorter than five characters", name), "hangar.name_invalid",
			"A Hangar's name follows the rule for application names: letters and numbers, 5 to 45 characters.")
	case len(name) > 45:
		return usage(fmt.Sprintf("%q is longer than 45 characters", short(name)), "hangar.name_invalid", "")
	case !isAlphanumeric(name):
		return usage(fmt.Sprintf("%q has something other than letters and numbers in it", short(name)),
			"hangar.name_invalid",
			"A Hangar's name follows the rule for application names, so it takes no dashes, dots "+
				"or underscores.")
	}

	for _, reserved := range reservedNames {
		if strings.EqualFold(name, reserved) {
			return usage(fmt.Sprintf("%q is reserved", name), "hangar.name_reserved",
				"Names that read as infrastructure are refused, for Hangars as for applications.")
		}
	}
	return nil
}

func checkHangarAgents(agents []string) error {
	for i, a := range agents {
		if !contains(HangarAgents, a) {
			return usage(fmt.Sprintf("no agent called %q", a), "hangar.agent_invalid",
				"This release knows: %s. A newer release may know more.", strings.Join(HangarAgents, ", "))
		}
		if contains(agents[:i], a) {
			return usage(fmt.Sprintf("%s was given twice", a), "hangar.agent_duplicate",
				"Each agent is installed once.")
		}
	}
	return nil
}

// IdleTimeoutChoices are the idle timeouts as a person writes them: 30m, never.
func IdleTimeoutChoices() []string {
	out := make([]string, 0, len(HangarIdleTimeouts)+1)
	for _, m := range HangarIdleTimeouts {
		out = append(out, fmt.Sprintf("%dm", m))
	}
	return append(out, "never")
}

func containsInt(values []int, v int) bool {
	for _, candidate := range values {
		if candidate == v {
			return true
		}
	}
	return false
}
