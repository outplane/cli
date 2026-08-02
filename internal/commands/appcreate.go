package commands

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/outplane/cli/internal/clierr"
	"github.com/outplane/cli/internal/core"
	"github.com/outplane/cli/internal/output"
)

func init() {
	register("app create", appCreate)
}

// appCreate makes an application and, unavoidably, deploys it.
//
// There is no dormant application on this platform: the server starts the first
// deployment as part of creation, so the command reports the deployment id
// alongside the app and says plainly that something is already building.
//
// Every rule the server would enforce is checked before anything is sent. That
// is a copy of somebody else's rules, which this codebase otherwise avoids, and
// it earns its place here for one reason: creation has a dozen ways to be
// wrong, and the server answers all of them with one sentence and no field
// name. Checking first is also what makes --dry-run mean something.
func appCreate(ctx context.Context, req Request) (output.Table, error) {
	spec, err := buildNewApp(req)
	if err != nil {
		return output.Table{}, err
	}
	if err := spec.Check(); err != nil {
		return output.Table{}, err
	}

	cli := req.CLI
	if cli.Config.TeamError != nil {
		return output.Table{}, cli.SignInError()
	}

	if cli.DryRun {
		describeNewApp(req, spec)
		return createTable(spec, core.CreatedApp{}, false), nil
	}

	client, err := cli.APIClient()
	if err != nil {
		return output.Table{}, err
	}

	created, err := core.CreateApp(ctx, client, cli.Config.TeamID.Value, spec)
	if err != nil {
		return output.Table{}, explainRepositoryAccess(err, spec)
	}

	cli.Out.Note("Created %s.", spec.Name)
	cli.Out.Note("Deployment %d started. It is not finished.", created.DeploymentID)
	cli.Out.Note("Watch it with: outplane deploy logs %d", created.DeploymentID)

	return createTable(spec, created, true), nil
}

// explainRepositoryAccess adds a way forward when the platform cannot see the
// repository.
//
// One server message covers three different situations: the repository does not
// exist, it exists but the GitHub App was never installed, and it is installed
// somewhere that does not include this repository. A reader cannot tell which,
// and the fix for all three is the same page.
//
// The match is on the server's own sentence, which is a coupling worth naming.
// It fails safe: a message this does not recognise is passed through exactly as
// it arrived, so the worst outcome of the server rewording its error is that
// two next steps stop appearing.
func explainRepositoryAccess(err error, spec core.NewApp) error {
	e := clierr.AsError(err)
	if e == nil || spec.Repository == "" || !strings.Contains(e.Message, "installations") {
		return err
	}

	return e.
		WithCode("app.repository_unavailable").
		WithHint("The platform cannot see %s. Either it does not exist, or the Out Plane "+
			"GitHub App has not been given access to it. A public repository needs no "+
			"access at all: pass --public-repo.", spec.Repository).
		WithStep("see which repositories are connected", "outplane", "repos").
		WithStep("grant access to more of them", core.ConnectURL)
}

// buildNewApp reads the flags into a specification.
func buildNewApp(req Request) (core.NewApp, error) {
	if len(req.Args) == 0 {
		return core.NewApp{}, clierr.New(clierr.KindUsage, "no name given").
			WithCode("usage.missing_argument").
			WithHint("The name appears in the application's public address and cannot be changed. "+
				"The display name can.").
			WithStep("create from a repository", "outplane", "app", "create", "<NAME>",
				"--repo", "<OWNER>/<REPO>", "--branch", "main", "--port", "3000").
			WithStep("create from an image", "outplane", "app", "create", "<NAME>",
				"--image", "nginx:latest", "--port", "80")
	}
	if strings.TrimSpace(req.Args[0]) == "" {
		return core.NewApp{}, emptyAppArgument(
			"A new application has to be named; there is nothing to fall back to.")
	}

	spec := core.NewApp{
		Name:         req.Args[0],
		Repository:   strings.TrimSpace(req.Flags.String("repo")),
		Branch:       strings.TrimSpace(req.Flags.String("branch")),
		PublicRepo:   req.Flags.Bool("public-repo"),
		Image:        strings.TrimSpace(req.Flags.String("image")),
		BuildMethod:  orDefault(req.Flags.String("build"), "dockerfile"),
		Directory:    req.Flags.String("dir"),
		StartCommand: req.Flags.String("start-command"),
		InstanceType: orDefault(req.Flags.String("size"), "op-20"),
	}

	instances, err := strconv.Atoi(orDefault(req.Flags.String("instances"), "1"))
	if err != nil {
		return spec, clierr.New(clierr.KindUsage, "--instances is not a number: %q",
			req.Flags.String("instances")).WithCode("usage.bad_instances")
	}
	spec.Instances = instances

	if spec.Ports, err = parsePorts(req.Flags.Strings("port")); err != nil {
		return spec, err
	}
	if spec.Env, err = parseAssignments(req.Flags.Strings("env")); err != nil {
		return spec, err
	}
	if spec.Volumes, err = parseMounts(req.Flags.Strings("volume")); err != nil {
		return spec, err
	}
	spec.EnvGroups = req.Flags.Strings("env-group")
	return spec, nil
}

// parseMounts reads VOLUME_ID:/path.
//
// Split on the first colon only: the id never contains one and the path might,
// and getting that backwards would silently mount somewhere else.
func parseMounts(values []string) ([]core.Mount, error) {
	mounts := make([]core.Mount, 0, len(values))
	for _, raw := range values {
		id, path, ok := strings.Cut(raw, ":")
		if !ok {
			return nil, clierr.New(clierr.KindUsage, "%q is not a volume mount", raw).
				WithCode("usage.bad_mount").
				WithHint("Write it as VOLUME_ID:/path, for example %s:/data.",
					"3f2b1c4e-0000-0000-0000-000000000000")
		}
		mounts = append(mounts, core.Mount{
			VolumeID: strings.TrimSpace(id),
			Path:     strings.TrimSpace(path),
		})
	}
	return mounts, nil
}

// parsePorts reads PORT[:SCHEME[:public|private]] for a new application, where
// an unwritten part takes its default: a private HTTP port, which is what an
// application behind another one wants.
//
// The syntax itself lives in parsePortSpecs, because `port set` reads the same
// argument and cannot use these defaults: there, an unwritten part means the
// port keeps what it already had.
func parsePorts(values []string) ([]core.NewPort, error) {
	specs, err := parsePortSpecs(values)
	if err != nil {
		return nil, err
	}

	ports := make([]core.NewPort, 0, len(specs))
	for _, s := range specs {
		ports = append(ports, s.withDefaults())
	}
	return ports, nil
}

// portSpec is one PORT[:SCHEME[:public|private]] argument with the parts that
// were not written left unset.
//
// The pointers carry the distinction the parts cannot: `3000` and
// `3000:http:private` look the same after defaults are applied, and they are
// different requests when the port already exists. Collapsing them is how a
// public port becomes private because somebody typed its number.
type portSpec struct {
	Port   int
	Scheme *string
	Public *bool
}

// withDefaults is the port to create when there is nothing to keep.
func (s portSpec) withDefaults() core.NewPort {
	port := core.NewPort{Port: s.Port, Scheme: "http"}
	if s.Scheme != nil {
		port.Scheme = *s.Scheme
	}
	if s.Public != nil {
		port.Public = *s.Public
	}
	return port
}

// appliedTo is the port to send for one that already exists: what was written
// wins, and what was not is carried over unchanged.
func (s portSpec) appliedTo(existing core.Endpoint) core.NewPort {
	port := core.NewPort{Port: s.Port, Scheme: existing.Scheme, Public: existing.Public}
	if s.Scheme != nil {
		port.Scheme = *s.Scheme
	}
	if s.Public != nil {
		port.Public = *s.Public
	}
	return port
}

// parsePortSpecs reads the argument's syntax and nothing else.
//
// The shape is positional because the parts have an obvious order and somebody
// writing 3000 means the common case. Whether an omitted part becomes a default
// or is left alone is the caller's decision, not this function's.
func parsePortSpecs(values []string) ([]portSpec, error) {
	specs := make([]portSpec, 0, len(values))
	seen := make(map[int]bool, len(values))

	for _, raw := range values {
		parts := strings.Split(raw, ":")
		if len(parts) > 3 {
			return nil, badPort(raw, "There are at most three parts: the number, the scheme and public or private.")
		}

		number, err := strconv.Atoi(strings.TrimSpace(parts[0]))
		if err != nil {
			return nil, badPort(raw, "%q is not a number.", parts[0])
		}
		if seen[number] {
			// Not badPort: the number is a perfectly good port, and saying it is
			// not one would send the reader looking at the wrong thing.
			return nil, clierr.New(clierr.KindUsage, "port %d was given twice", number).
				WithCode("app.port_duplicate").
				WithHint("The two would contradict each other and there is no rule saying " +
					"which of them wins.")
		}
		seen[number] = true

		spec := portSpec{Port: number}
		if len(parts) > 1 && strings.TrimSpace(parts[1]) != "" {
			scheme := strings.ToLower(strings.TrimSpace(parts[1]))
			spec.Scheme = &scheme
		}
		if len(parts) > 2 && strings.TrimSpace(parts[2]) != "" {
			switch strings.ToLower(strings.TrimSpace(parts[2])) {
			case "public":
				public := true
				spec.Public = &public
			case "private":
				private := false
				spec.Public = &private
			default:
				return nil, badPort(raw, "The last part is public or private.")
			}
		}
		specs = append(specs, spec)
	}
	return specs, nil
}

func badPort(raw, hint string, args ...any) error {
	return clierr.New(clierr.KindUsage, "%q is not a port", raw).
		WithCode("usage.bad_port").
		WithHint(hint, args...).
		WithStep("a private HTTP port", "outplane", "app", "create", "<NAME>", "--port", "3000").
		WithStep("a public one", "outplane", "app", "create", "<NAME>", "--port", "3000:http:public")
}

// describeNewApp prints what would be created, so a dry run says more than
// "nothing was sent".
func describeNewApp(req Request, spec core.NewApp) {
	source := spec.Image
	if source == "" {
		source = fmt.Sprintf("%s on %s", spec.Repository, spec.Branch)
	}

	req.CLI.Out.Note("Would create %s from %s.", spec.Name, source)
	req.CLI.Out.Note("  %d instance(s) of %s", spec.Instances, spec.InstanceType)
	for _, p := range spec.Ports {
		visibility := "private"
		if p.Public {
			visibility = "public"
		}
		req.CLI.Out.Note("  port %d, %s, %s", p.Port, p.Scheme, visibility)
	}
	if len(spec.Env) > 0 {
		req.CLI.Out.Note("  %d environment variable(s), values not shown", len(spec.Env))
	}
	for _, m := range spec.Volumes {
		req.CLI.Out.Note("  volume %s at %s", m.VolumeID, m.Path)
	}
	for _, g := range spec.EnvGroups {
		req.CLI.Out.Note("  variable group %s", g)
	}
	req.CLI.Out.Note("Nothing was sent. Creating also starts a deployment.")
}

func createTable(spec core.NewApp, created core.CreatedApp, changed bool) output.Table {
	ports := make([]map[string]any, 0, len(spec.Ports))
	for _, p := range spec.Ports {
		ports = append(ports, map[string]any{"port": p.Port, "scheme": p.Scheme, "public": p.Public})
	}

	return output.Table{
		Single:  true,
		Columns: []string{"name", "appId", "deploymentId", "source", "size", "instances", "changed"},
		Total:   1,
		Rows: []map[string]any{{
			"name":         spec.Name,
			"appId":        nilIfEmpty(created.AppID),
			"deploymentId": nilIfZero(created.DeploymentID),
			"source":       sourceDescription(spec),
			"repository":   nilIfEmpty(spec.Repository),
			"branch":       nilIfEmpty(spec.Branch),
			"imageRef":     nilIfEmpty(spec.Image),
			"buildMethod":  buildMethodOf(spec),
			"size":         spec.InstanceType,
			"instances":    spec.Instances,
			"ports":        ports,
			"envCount":     len(spec.Env),
			"changed":      changed,
		}},
	}
}

func sourceDescription(spec core.NewApp) string {
	if spec.Image != "" {
		return core.SourceContainerRegistry
	}
	return "github"
}

// buildMethodOf reports what will actually build the app, which for an image is
// nothing at all: the server overrides whatever --build said.
func buildMethodOf(spec core.NewApp) string {
	if spec.Image != "" {
		return "prebuilt-image"
	}
	return spec.BuildMethod
}
