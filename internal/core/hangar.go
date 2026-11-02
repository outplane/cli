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

	// DiskID is the home disk's volume id, which its disk usage is keyed by.
	// Empty when the server names none.
	DiskID string `json:"diskId"`

	// CPULimitMillicores and MemoryLimitMB are what its size allows, as on an
	// application. Zero when the server does not say.
	CPULimitMillicores int `json:"cpuLimitMillicores"`
	MemoryLimitMB      int `json:"memoryLimitMb"`

	// IdleTimeoutMinutes is nil for never. A pointer, because zero minutes and
	// never are different answers.
	IdleTimeoutMinutes *int `json:"idleTimeoutMinutes"`

	// Ports are what its dev servers are reached on, lowest first. Never nil.
	Ports []HangarPort `json:"ports"`

	// Repository is the owner/name cloned into it on its first start, and
	// Branch the branch cloned. Both empty for a Hangar created without one.
	// PublicSource says it was read without a GitHub installation.
	Repository   string `json:"repository"`
	Branch       string `json:"branch"`
	PublicSource bool   `json:"publicSource"`

	CreatedAt string `json:"createdAt"`
}

// HangarPort is one port a Hangar has open for its dev servers.
//
// It is an application's port with one address more. The team's applications
// reach a Hangar by its name, so every open port has an internal address,
// public or not, and a public one has an outside address besides.
type HangarPort struct {
	Port   int    `json:"port"`
	Scheme string `json:"scheme"`
	Public bool   `json:"public"`

	// URL is the outside address, present only while the port is public: an
	// https address for http and h2c, a host and a port number for tcp.
	URL string `json:"url"`

	// Internal is {name}:{port}, where the team's applications reach it.
	Internal string `json:"internal"`
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
	DiskID             string   `json:"diskId"`
	CPULimitMillicores int      `json:"cpuLimitMillicores"`
	MemoryLimitMB      int      `json:"memoryLimitMb"`
	IdleTimeoutMinutes *int     `json:"idleTimeoutMinutes"`
	CreatedDate        string   `json:"createdDate"`

	SourceRepository string `json:"sourceRepository"`
	Branch           string `json:"branch"`
	IsPublicSource   bool   `json:"isPublicSource"`

	Ports []hangarPortDTO `json:"ports"`
}

// hangarPortDTO is the part of an AppPortDto a Hangar's port needs. Scheme is
// an EndpointScheme, as an integer.
type hangarPortDTO struct {
	Port      int    `json:"port"`
	Scheme    int    `json:"scheme"`
	IsPublic  bool   `json:"isPublic"`
	PublicURL string `json:"publicUrl"`
}

// noDiskID is the empty GUID. The disk id is not optional on the wire, so a
// Hangar with no home disk on record arrives with this rather than without one.
const noDiskID = "00000000-0000-0000-0000-000000000000"

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

	diskID := strings.ToLower(d.DiskID)
	if diskID == noDiskID {
		diskID = ""
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
		DiskID:             diskID,
		CPULimitMillicores: d.CPULimitMillicores,
		MemoryLimitMB:      d.MemoryLimitMB,
		IdleTimeoutMinutes: d.IdleTimeoutMinutes,
		Ports:              decodeHangarPorts(d.Name, d.Ports),
		Repository:         d.SourceRepository,
		Branch:             d.Branch,
		PublicSource:       d.IsPublicSource,
		CreatedAt:          serverInstant(d.CreatedDate),
	}
}

// decodeHangarPorts sorts as well, so that every caller sees the set lowest
// first whatever order a response used.
func decodeHangarPorts(name string, dtos []hangarPortDTO) []HangarPort {
	ports := make([]HangarPort, 0, len(dtos))
	for _, d := range dtos {
		p := HangarPort{
			Port:     d.Port,
			Scheme:   schemeNames.name(d.Scheme),
			Public:   d.IsPublic,
			Internal: fmt.Sprintf("%s:%d", name, d.Port),
		}
		if d.IsPublic {
			p.URL = d.PublicURL
		}
		ports = append(ports, p)
	}
	sort.Slice(ports, func(i, j int) bool { return ports[i].Port < ports[j].Port })
	return ports
}

// Endpoints is the Hangar's ports in the shape MergePorts and WithoutPorts
// read, so that a Hangar's set changes by exactly the rules an application's
// does.
func (h Hangar) Endpoints() []Endpoint {
	out := make([]Endpoint, 0, len(h.Ports))
	for _, p := range h.Ports {
		out = append(out, Endpoint{Port: p.Port, Scheme: p.Scheme, Public: p.Public, URL: p.URL})
	}
	return out
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

// HangarIdleTimeouts are the idle timeouts a Hangar may have, in minutes, at
// creation and afterwards. Never is always allowed besides these.
var HangarIdleTimeouts = []int{15, 30, 60, 120, 360, 720, 1440}

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

	// Repository is owner/name on GitHub, cloned into the Hangar once, on its
	// first start, and never changed afterwards. Empty for none; given, it
	// needs a branch. PublicRepo reads it without a GitHub installation.
	Repository string
	Branch     string
	PublicRepo bool
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
// where null is the value that means never. The source fields are an
// application's, sent only with a repository, whose absence means none.
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

	body := map[string]any{
		"name":                 h.Name,
		"agents":               agents,
		"environmentVariables": env,
		"envVariableGroupIds":  groups,
		"idleTimeoutMinutes":   h.IdleTimeoutMinutes,
	}
	if h.Repository != "" {
		body["sourceProvider"] = sourceProviderValue(false)
		body["sourceRepository"] = h.Repository
		body["branch"] = h.Branch
		body["isPublicSource"] = h.PublicRepo
	}
	return body
}

// A Hangar's ports.
//
// The endpoint replaces the whole set, as an application's does, so callers
// send MergePorts' or WithoutPorts' result rather than the ports they named.
// What differs is the effect. The server rewrites the Hangar's routing alone
// and restarts nothing, so a save is live within seconds and the terminal
// session and the agents in it carry on. There is no deployment to start.

// HangarMaxPorts is how many ports a Hangar can have open at once.
const HangarMaxPorts = 3

// HangarOwnPort is the port the Hangar itself uses. It is never one a dev
// server can be reached on.
const HangarOwnPort = 7070

// SetHangarPorts replaces a Hangar's ports with exactly this set and returns
// what was saved, with the addresses the server assigned.
func SetHangarPorts(ctx context.Context, c *api.Client, h Hangar, ports []NewPort) ([]HangarPort, error) {
	var saved []hangarPortDTO
	body := map[string]any{"ports": portBodies(ports)}
	if err := c.Put(ctx, "/Hangar/UpdateHangarPorts/"+h.ID, body, &saved); err != nil {
		return nil, err
	}
	return decodeHangarPorts(h.Name, saved), nil
}

// CheckHangarPorts reports the first thing the server would refuse in a set
// about to be sent: an application's rules, then a Hangar's own two.
//
// It takes the whole set rather than the ports named, because the limit is on
// the whole set. A port carried over with a scheme this release does not know
// is refused too, rather than sent back as a guess.
func CheckHangarPorts(ports []NewPort) error {
	seen := make(map[int]bool, len(ports))
	for _, p := range ports {
		switch {
		case p.Port < 1 || p.Port > 65535:
			return usage(fmt.Sprintf("%d is not a port", p.Port), "hangar.port_invalid",
				"A port is a number from 1 to 65535.")
		case !contains(Schemes, p.Scheme):
			return usage(fmt.Sprintf("no scheme called %q", p.Scheme), "hangar.scheme_invalid",
				"Use one of: %s.", strings.Join(Schemes, ", "))
		case seen[p.Port]:
			return usage(fmt.Sprintf("port %d was given twice", p.Port), "hangar.port_duplicate", "")
		case p.Port == HangarOwnPort:
			return usage(fmt.Sprintf("port %d is the Hangar's own", p.Port), "hangar.port_reserved",
				"The Hangar itself uses it, so a dev server has to listen on another one.")
		}
		seen[p.Port] = true
	}
	if len(ports) > HangarMaxPorts {
		return usage(fmt.Sprintf("that would leave %d ports open, and a Hangar can have %d", len(ports), HangarMaxPorts),
			"hangar.port_limit", "Closing one makes room for another.")
	}
	return nil
}

// RestartHangar starts a Hangar again on a new machine with the same disk.
//
// The server starts it from what is saved: its variables, its groups and its
// ports, so a change saved since it last started takes effect. What stops is
// everything that runs, the session and its agents included. A Hangar with
// nothing running is refused with a conflict, because there is nothing to
// restart.
func RestartHangar(ctx context.Context, c *api.Client, id string) error {
	return c.Post(ctx, "/Hangar/RestartHangar/"+id, nil, nil)
}

// SetHangarPaused pauses a Hangar, or resumes it.
//
// Pausing stops its machine and keeps its disk, its ports and its variables;
// resuming starts it on a new machine with that disk, from the variables, groups
// and ports saved by then. The server returns early when the state already
// matches, and refuses a Hangar that never started with a conflict, because
// there is no machine to stop or start.
func SetHangarPaused(ctx context.Context, c *api.Client, id string, paused bool) error {
	body := map[string]any{"isPaused": paused}
	return c.Put(ctx, "/Hangar/UpdateHangarPauseState/"+id, body, nil)
}

// SetHangarIdleTimeout changes how long a Hangar may sit idle before it pauses.
// Nil is never, which the server reads from a null.
//
// Only the setting is written. Nothing restarts, and the next idle check reads
// the new value.
func SetHangarIdleTimeout(ctx context.Context, c *api.Client, id string, minutes *int) error {
	body := map[string]any{"idleTimeoutMinutes": minutes}
	return c.Put(ctx, "/Hangar/UpdateHangarIdleTimeout/"+id, body, nil)
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
	if h.Repository != "" {
		if err := checkRepository("hangar", h.Repository, h.Branch); err != nil {
			return err
		}
	}
	return CheckHangarIdleTimeout(h.IdleTimeoutMinutes)
}

// CheckHangarIdleTimeout refuses an idle timeout the server would refuse. Nil,
// for never, is always allowed.
func CheckHangarIdleTimeout(minutes *int) error {
	if minutes != nil && !containsInt(HangarIdleTimeouts, *minutes) {
		return usage(fmt.Sprintf("%d minutes is not an idle timeout a Hangar can have", *minutes),
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

// IdleTimeoutChoices are the idle timeouts as a person writes them: 15m, 1h,
// 24h, never.
func IdleTimeoutChoices() []string {
	out := make([]string, 0, len(HangarIdleTimeouts)+1)
	for _, m := range HangarIdleTimeouts {
		out = append(out, IdleTimeoutLabel(&m))
	}
	return append(out, "never")
}

// IdleTimeoutLabel writes an idle timeout the way --idle-timeout takes it:
// whole hours in hours, anything else in minutes, and nil as never.
func IdleTimeoutLabel(minutes *int) string {
	switch {
	case minutes == nil:
		return "never"
	case *minutes%60 == 0:
		return fmt.Sprintf("%dh", *minutes/60)
	default:
		return fmt.Sprintf("%dm", *minutes)
	}
}

func containsInt(values []int, v int) bool {
	for _, candidate := range values {
		if candidate == v {
			return true
		}
	}
	return false
}
