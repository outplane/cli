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
	register("port list", portList)
	register("port get", portGet)
	register("port set", portSet)
	register("port unset", portUnset)
}

// The ports an application serves.
//
// This group is deliberately the env group with different nouns: the same four
// verbs, the same --app flag rather than a positional, the same --deploy and
// the same sentence about the running application keeping what it has. A reader
// who has used one should not have to learn the other.
//
// One thing behind the surface differs, and it is the reason core/port.go
// exists. `env set` never sends a variable it was not told about, because the
// API merges. The port endpoint replaces: whatever is missing from the request
// is deleted. So these commands read the current set, apply the change and send
// the result, which produces the same experience and one risk env does not
// have. Two callers changing different ports at the same time can lose one of
// the changes. The read is therefore taken as late as possible, --dry-run
// prints the entire set that would be sent rather than the part that changed,
// and the automation notes say so plainly.

func portList(ctx context.Context, req Request) (output.Table, error) {
	_, ports, err := appPorts(ctx, req, "port", "list")
	if err != nil {
		return output.Table{}, err
	}

	table := output.Table{
		Columns: []string{"port", "scheme", "public", "url", "domains"},
		Total:   len(ports),
	}
	for _, p := range ports {
		table.Rows = append(table.Rows, map[string]any{
			"port":          p.Port,
			"portId":        p.PortID,
			"scheme":        p.Scheme,
			"public":        p.Public,
			"url":           nilIfEmpty(p.URL),
			"domains":       len(p.CustomDomains),
			"customDomains": p.CustomDomains,
		})
	}

	if len(ports) == 0 {
		table.Footer = "This application serves no ports, so nothing can reach it."
	}
	return table, nil
}

// portGet prints one port's address and nothing else.
//
// The same reason `env get` exists: `curl $(outplane port get 3000)` has to
// work, so text mode writes the bare address with no table around it.
//
// A port with no address is an error rather than an empty line, in every
// format, because an empty line in a command substitution becomes a request to
// nowhere and the failure surfaces somewhere else entirely.
func portGet(ctx context.Context, req Request) (output.Table, error) {
	number, err := portArg(req, 0)
	if err != nil {
		return output.Table{}, err
	}

	app, ports, err := appPorts(ctx, req, "port", "get")
	if err != nil {
		return output.Table{}, err
	}

	found, ok := core.FindPort(ports, number)
	if !ok {
		return output.Table{}, unknownPort(app, number, ports)
	}
	if found.URL == "" {
		return output.Table{}, clierr.New(clierr.KindNotFound,
			"port %d on %s has no address", number, app.Name).
			WithCode("port.no_address").
			WithHint("It is private, so it is reachable from inside the platform and "+
				"nowhere else. Making it public gives it one.").
			WithStep("make it public", "outplane", "port", "set",
				fmt.Sprintf("%d::public", number), "--app", app.Name).
			WithStep("see every port", "outplane", "port", "list", "--app", app.Name)
	}

	if !req.CLI.Out.Ctx.Machine() {
		fmt.Fprintln(req.CLI.Out.Out, found.URL)
		return streamed(), nil
	}

	return output.Table{
		Single:  true,
		Columns: []string{"port", "scheme", "public", "url"},
		Total:   1,
		Rows: []map[string]any{{
			"port":          found.Port,
			"portId":        found.PortID,
			"scheme":        found.Scheme,
			"public":        found.Public,
			"url":           found.URL,
			"customDomains": found.CustomDomains,
		}},
	}, nil
}

// portSet adds ports, or changes the ones already there.
//
// A part left out of the argument keeps whatever the port already had, so
// `port set 3000` on a public port does not quietly make it private. On a port
// that does not exist yet there is nothing to keep, and the same defaults as
// `app create` apply: private HTTP.
func portSet(ctx context.Context, req Request) (output.Table, error) {
	specs, err := parsePortSpecs(req.Args)
	if err != nil {
		return output.Table{}, err
	}
	if len(specs) == 0 {
		return output.Table{}, clierr.New(clierr.KindUsage, "no port given").
			WithCode("usage.missing_argument").
			WithStep("open a port", "outplane", "port", "set", "3000:http:public").
			WithStep("see what is open", "outplane", "port", "list")
	}

	app, ports, err := appPorts(ctx, req, "port", "set")
	if err != nil {
		return output.Table{}, err
	}

	// Resolved against what is there now, so an omitted part can be carried
	// over, and checked before anything is sent so that a bad third argument
	// does not leave the first two applied.
	incoming := make([]core.NewPort, 0, len(specs))
	for _, spec := range specs {
		port := spec.withDefaults()
		if existing, ok := core.FindPort(ports, spec.Port); ok {
			port = spec.appliedTo(existing)
		}
		if err := core.CheckPort(port); err != nil {
			return output.Table{}, err
		}
		incoming = append(incoming, port)
	}

	return applyPorts(ctx, req, app, "set", core.MergePorts(ports, incoming), portNumbers(specs))
}

// portUnset stops an application serving a port.
//
// Every number is resolved before anything is sent, which is `env unset`'s rule
// and exists for the same reason: a typo in the third number should not leave
// the first two removed.
func portUnset(ctx context.Context, req Request) (output.Table, error) {
	if len(req.Args) == 0 {
		return output.Table{}, clierr.New(clierr.KindUsage, "no port given").
			WithCode("usage.missing_argument").
			WithStep("see what is open", "outplane", "port", "list")
	}

	app, ports, err := appPorts(ctx, req, "port", "unset")
	if err != nil {
		return output.Table{}, err
	}

	numbers := make([]int, 0, len(req.Args))
	for i := range req.Args {
		number, err := portArg(req, i)
		if err != nil {
			return output.Table{}, err
		}
		if _, ok := core.FindPort(ports, number); !ok {
			return output.Table{}, unknownPort(app, number, ports)
		}
		numbers = append(numbers, number)
	}

	return applyPorts(ctx, req, app, "unset", core.WithoutPorts(ports, numbers), numbers)
}

// applyPorts sends a whole set and reports what it did, which is the last step
// of both mutations and therefore lives in one place.
//
// The set it prints is the set it sends. That is the honest report for an
// endpoint that replaces rather than merges: "3000 added" would be true and
// would hide the fact that four other ports were in the same request.
func applyPorts(ctx context.Context, req Request, app core.App, action string,
	final []core.NewPort, named []int) (output.Table, error) {

	client, err := req.CLI.APIClient()
	if err != nil {
		return output.Table{}, err
	}

	if req.CLI.DryRun {
		req.CLI.Out.Note("Would %s %s on %s. Nothing was sent.",
			action, portsPhrase(named), app.Name)
		req.CLI.Out.Note("The request replaces every port, and would carry: %s.", describePorts(final))
		return portChangeTable(app, action, named, final, false, 0), nil
	}

	if err := core.SetPorts(ctx, client, app.ID, final); err != nil {
		return output.Table{}, err
	}
	req.CLI.Out.Note("%s now serves: %s.", app.Name, describePorts(final))

	id, err := applyChange(ctx, req, client, app, "ports")
	if err != nil {
		return output.Table{}, err
	}
	return portChangeTable(app, action, named, final, true, id), nil
}

// appPorts resolves the application and reads its ports.
//
// Every command here starts this way, and the mutations start this way as late
// as they can: the set that comes back is the set that will be sent again, so
// the shorter the gap the smaller the chance of overwriting somebody else's
// change.
func appPorts(ctx context.Context, req Request, command ...string) (core.App, []core.Endpoint, error) {
	app, err := flagApp(ctx, req, command...)
	if err != nil {
		return core.App{}, nil, err
	}

	client, err := req.CLI.APIClient()
	if err != nil {
		return core.App{}, nil, err
	}

	ports, err := core.ListPorts(ctx, client, app.ID)
	if err != nil {
		return core.App{}, nil, err
	}
	return app, ports, nil
}

// portArg reads one positional port number.
func portArg(req Request, i int) (int, error) {
	if i >= len(req.Args) || strings.TrimSpace(req.Args[i]) == "" {
		return 0, clierr.New(clierr.KindUsage, "no port given").
			WithCode("usage.missing_argument").
			WithStep("see what is open", "outplane", "port", "list")
	}

	number, err := strconv.Atoi(strings.TrimSpace(req.Args[i]))
	if err != nil {
		return 0, clierr.New(clierr.KindUsage, "%q is not a port number", req.Args[i]).
			WithCode("usage.bad_port").
			WithHint("A port is a number between 1 and 65535.")
	}
	return number, nil
}

func unknownPort(app core.App, number int, ports []core.Endpoint) error {
	e := clierr.New(clierr.KindNotFound, "%s does not serve port %d", app.Name, number).
		WithCode("port.not_found").
		WithStep("see what is open", "outplane", "port", "list", "--app", app.Name)
	if names := core.PortNumbers(ports); len(names) > 0 {
		return e.WithHint("It serves: %s.", strings.Join(names, ", ")).
			WithDetail("availablePorts", names)
	}
	return e.WithHint("It serves none at all.")
}

// portChangeTable reports both halves: what was asked for, and what was sent.
func portChangeTable(app core.App, action string, named []int, final []core.NewPort,
	changed bool, deploymentID int) output.Table {

	ports := make([]map[string]any, 0, len(final))
	for _, p := range final {
		ports = append(ports, map[string]any{
			"port": p.Port, "scheme": p.Scheme, "public": p.Public,
		})
	}

	return output.Table{
		Single:  true,
		Columns: []string{"action", "numbers", "app", "serving", "changed", "deploymentId"},
		Total:   1,
		Rows: []map[string]any{{
			"action":       action,
			"numbers":      named,
			"app":          app.Name,
			"appId":        app.ID,
			"serving":      len(final),
			"ports":        ports,
			"changed":      changed,
			"deploymentId": nilIfZero(deploymentID),
		}},
	}
}

// describePorts renders a set the way somebody would say it out loud.
func describePorts(ports []core.NewPort) string {
	if len(ports) == 0 {
		return "nothing"
	}
	parts := make([]string, 0, len(ports))
	for _, p := range ports {
		visibility := "private"
		if p.Public {
			visibility = "public"
		}
		parts = append(parts, fmt.Sprintf("%d/%s %s", p.Port, p.Scheme, visibility))
	}
	return strings.Join(parts, ", ")
}

func portsPhrase(numbers []int) string {
	parts := make([]string, 0, len(numbers))
	for _, n := range numbers {
		parts = append(parts, strconv.Itoa(n))
	}
	return "port " + strings.Join(parts, ", ")
}

func portNumbers(specs []portSpec) []int {
	out := make([]int, 0, len(specs))
	for _, s := range specs {
		out = append(out, s.Port)
	}
	return out
}
