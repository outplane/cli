package core

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/outplane/cli/internal/api"
	"github.com/outplane/cli/internal/clierr"
)

// Creating an application.
//
// One request creates the record, its ports, its variables and its first
// deployment: the server starts that deployment itself, so there is no separate
// deploy step and no way to create something dormant.
//
// The rules below are the server's, restated here for one reason. Creation is
// the request with the most ways to be wrong, and the server answers all of
// them with one 400 carrying a single sentence. Checking first means the reader
// is told which field, before anything is sent, and it is what makes --dry-run
// worth running.

// NewApp is everything needed to create an application.
type NewApp struct {
	Name string

	// Repository and Image are the two sources, and exactly one is given.
	// Repository is "owner/name" and needs a branch; Image is a container
	// reference and forbids one, because there is nothing to build.
	Repository string
	Branch     string
	PublicRepo bool
	Image      string

	BuildMethod  string
	Directory    string
	StartCommand string

	Instances    int
	InstanceType string

	Ports []NewPort
	Env   map[string]string

	// Volumes and EnvGroups reference things that already exist. The server
	// attaches each one inside a try/catch and carries on when it cannot, so a
	// wrong id produces an application without the attachment and no error at
	// all. Whoever asks for one has to read back what they got; see
	// AppVolumes and AppEnvGroups.
	Volumes   []Mount
	EnvGroups []string
}

// Mount is an existing volume and where it should appear in the container.
type Mount struct {
	VolumeID string
	Path     string
}

// NewPort is one port the application will serve.
type NewPort struct {
	Port   int
	Scheme string
	Public bool
}

// CreatedApp is what the server returns, which is two identifiers.
type CreatedApp struct {
	AppID        string `json:"appId"`
	DeploymentID int    `json:"firstAppDeploymentId"`
}

// InstanceTypes are the sizes an application can run at, smallest first.
//
// The list is the server's and is duplicated here so that a wrong one is
// refused with the alternatives in hand rather than after a round trip. A size
// the server has learned since this release is refused by a CLI that has not,
// which is the cost of listing them; the message says where the list came from.
var InstanceTypes = []string{"op-20", "op-22", "op-34", "op-46", "op-58", "op-70", "op-82", "op-94"}

// BuildMethods are how a repository becomes an image.
var BuildMethods = []string{"dockerfile", "buildpack"}

// Schemes are how a port is served.
var Schemes = []string{"http", "h2c", "tcp"}

// reservedNames are names the server refuses.
//
// They are refused because they read as infrastructure rather than as somebody's
// application, and the platform puts the name in a public address.
var reservedNames = []string{
	"admin", "api", "auth", "authentication", "backend", "dashboard", "database",
	"demo", "frontend", "login", "mobile", "outplane", "root", "service", "system",
	"test", "web",
}

// CreateApp creates an application and starts its first deployment.
func CreateApp(ctx context.Context, c *api.Client, teamID string, app NewApp) (CreatedApp, error) {
	var out CreatedApp
	if err := c.Post(ctx, "/App/CreateApp", app.body(teamID), &out); err != nil {
		return CreatedApp{}, err
	}
	return out, nil
}

// body builds the request, whose field names are the server's own mixture of
// cases. They are copied exactly; the API does not accept either convention
// consistently, and this is not the place to find out which.
func (a NewApp) body(teamID string) map[string]any {
	body := map[string]any{
		"Name":                a.Name,
		"TeamId":              teamID,
		"MinScale":            a.Instances,
		"InstanceType":        a.InstanceType,
		"Directory":           a.Directory,
		"StartCommand":        a.StartCommand,
		"AppEnvironments":     envPairs(a.Env),
		"Ports":               portBodies(a.Ports),
		"VolumeMounts":        mountBody(a.Volumes),
		"EnvVariableGroupIds": a.EnvGroups,
		"isPublicSource":      a.PublicRepo,
		"sourceProvider":      sourceProviderValue(a.Image != ""),
		"SourceRepository":    a.Repository,
		"Branch":              a.Branch,
		"BuildMethod":         buildMethodValue(a.BuildMethod),
	}

	if a.Image != "" {
		// A container-registry app has no branch and no build: the server sets
		// both itself, and sending a branch it will discard invites a reader to
		// believe it meant something.
		body["SourceRepository"] = a.Image
		body["Branch"] = ""
		body["isPublicSource"] = true
	}
	return body
}

func sourceProviderValue(isImage bool) int {
	if isImage {
		return 200
	}
	return 10
}

// buildMethodValue turns the name back into the wire's integer. An image app
// gets PreBuiltImage, which the server would set anyway.
func buildMethodValue(name string) int {
	switch name {
	case "buildpack":
		return 20
	case "prebuilt-image":
		return 30
	default:
		return 10
	}
}

func schemeValue(name string) int {
	switch name {
	case "h2c":
		return 20
	case "tcp":
		return 30
	default:
		return 10
	}
}

func envPairs(env map[string]string) []map[string]string {
	pairs := make([]map[string]string, 0, len(env))
	for _, k := range sortedEnvKeys(env) {
		pairs = append(pairs, map[string]string{"Key": k, "Value": env[k]})
	}
	return pairs
}

func sortedEnvKeys(env map[string]string) []string {
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// mountBody builds the volume dictionary, keyed by id.
//
// A map means the same volume cannot be mounted twice, which is also the
// server's rule: a volume belongs to one application at one path.
func mountBody(mounts []Mount) map[string]string {
	body := make(map[string]string, len(mounts))
	for _, m := range mounts {
		body[m.VolumeID] = m.Path
	}
	return body
}

func portBodies(ports []NewPort) []map[string]any {
	out := make([]map[string]any, 0, len(ports))
	for _, p := range ports {
		out = append(out, map[string]any{
			"Port":     p.Port,
			"Scheme":   schemeValue(p.Scheme),
			"IsPublic": p.Public,
		})
	}
	return out
}

// Check reports the first thing the server would refuse.
//
// One at a time rather than all at once, because the server reports one too and
// a caller fixing them one by one gets the same experience either way.
func (a NewApp) Check() error {
	if err := a.checkName(); err != nil {
		return err
	}
	if err := a.checkSource(); err != nil {
		return err
	}
	if err := a.checkSize(); err != nil {
		return err
	}
	if err := a.checkPorts(); err != nil {
		return err
	}
	return a.checkMounts()
}

func (a NewApp) checkMounts() error {
	seen := make(map[string]bool, len(a.Volumes))
	for _, m := range a.Volumes {
		switch {
		case m.VolumeID == "":
			return usage("a volume mount needs a volume id", "app.mount_invalid", "")
		case !strings.HasPrefix(m.Path, "/"):
			return usage(fmt.Sprintf("%q is not an absolute path", m.Path), "app.mount_invalid",
				"A mount path is where the disk appears inside the container, such as /data.")
		case seen[m.VolumeID]:
			return usage(fmt.Sprintf("volume %s was given twice", m.VolumeID), "app.mount_duplicate",
				"A volume belongs to one application at one path.")
		}
		seen[m.VolumeID] = true
	}
	return nil
}

func (a NewApp) checkName() error {
	name := strings.TrimSpace(a.Name)

	switch {
	case name == "":
		return usage("an application needs a name", "app.name_required", "")
	case len(name) < 5:
		return usage(fmt.Sprintf("%q is shorter than five characters", name), "app.name_invalid",
			"The platform puts this name in a public address, so it refuses very short ones.")
	case len(name) > 45:
		return usage(fmt.Sprintf("%q is longer than 45 characters", short(name)), "app.name_invalid", "")
	case !isAlphanumeric(name):
		return usage(fmt.Sprintf("%q has something other than letters and numbers in it", short(name)),
			"app.name_invalid",
			"The name becomes part of a hostname, so it takes no dashes, dots or underscores. "+
				"The editable display name has no such rule.")
	}

	for _, reserved := range reservedNames {
		if strings.EqualFold(name, reserved) {
			return usage(fmt.Sprintf("%q is reserved", name), "app.name_reserved",
				"Names that read as infrastructure are refused, because this one appears in a "+
					"public address.")
		}
	}
	return nil
}

func (a NewApp) checkSource() error {
	hasRepo, hasImage := a.Repository != "", a.Image != ""

	switch {
	case hasRepo && hasImage:
		return usage("an application is built from a repository or run from an image, not both",
			"app.source_conflict", "Pass either --repo or --image.")
	case !hasRepo && !hasImage:
		return usage("no source given", "app.source_required",
			"Pass --repo owner/name to build from a repository, or --image to run a "+
				"container image that is already built.")
	}

	if hasImage {
		if strings.ContainsAny(a.Image, " \t") {
			return usage("an image reference cannot contain spaces", "app.image_invalid", "")
		}
		return nil
	}

	if strings.Count(a.Repository, "/") != 1 {
		return usage(fmt.Sprintf("%q is not owner/name", a.Repository), "app.repository_invalid",
			"A repository is given as its owner and its name, such as acme/checkout.")
	}
	if strings.TrimSpace(a.Branch) == "" {
		return usage("a repository needs a branch", "app.branch_required",
			"Pass --branch, which is usually main or master.")
	}
	if !contains(BuildMethods, a.BuildMethod) {
		return usage(fmt.Sprintf("no build method called %q", a.BuildMethod), "app.build_method_invalid",
			"Use one of: %s.", strings.Join(BuildMethods, ", "))
	}
	return nil
}

func (a NewApp) checkSize() error {
	if a.Instances < 1 || a.Instances > 5 {
		return usage(fmt.Sprintf("%d instances, and the range is 1 to 5", a.Instances),
			"app.instances_invalid", "")
	}
	if !contains(InstanceTypes, a.InstanceType) {
		return usage(fmt.Sprintf("no instance size called %q", a.InstanceType), "app.size_invalid",
			"Use one of: %s.", strings.Join(InstanceTypes, ", "))
	}
	return nil
}

func (a NewApp) checkPorts() error {
	seen := make(map[int]bool, len(a.Ports))
	for _, p := range a.Ports {
		if p.Port < 1 || p.Port > 65535 {
			return usage(fmt.Sprintf("%d is not a port", p.Port), "app.port_invalid", "")
		}
		if !contains(Schemes, p.Scheme) {
			return usage(fmt.Sprintf("no scheme called %q", p.Scheme), "app.scheme_invalid",
				"Use one of: %s.", strings.Join(Schemes, ", "))
		}
		if seen[p.Port] {
			return usage(fmt.Sprintf("port %d was given twice", p.Port), "app.port_duplicate", "")
		}
		seen[p.Port] = true
	}
	return nil
}

func usage(message, code, hint string, args ...any) error {
	e := clierr.New(clierr.KindUsage, "%s", message).WithCode(code)
	if hint != "" {
		e = e.WithHint(hint, args...)
	}
	return e
}

func isAlphanumeric(s string) bool {
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		default:
			return false
		}
	}
	return true
}

func contains(values []string, v string) bool {
	for _, candidate := range values {
		if candidate == v {
			return true
		}
	}
	return false
}
