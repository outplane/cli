package commands

import (
	"context"
	"strconv"
	"strings"

	"github.com/outplane/cli/internal/clierr"
	"github.com/outplane/cli/internal/core"
	"github.com/outplane/cli/internal/output"
)

func init() {
	register("hangar port list", hangarPortList)
	register("hangar port set", hangarPortSet)
	register("hangar port unset", hangarPortUnset)
}

// The ports a Hangar has open for its dev servers.
//
// These are `port list`, `port set` and `port unset` for a Hangar: the same
// argument syntax, and the same read, merge and send behind it, because the
// endpoint replaces the whole set as an application's does. Two things differ.
//
// The Hangar is the first argument rather than --app. Nothing defaults to a
// Hangar the way a linked application does, so it is always given, and a
// required first argument cannot be mistaken for one of the ports after it.
//
// Nothing deploys. The server applies a save to the running Hangar within
// seconds without restarting it, so there is no --deploy, and the output says
// so because the opposite is true of an application.

func hangarPortList(ctx context.Context, req Request) (output.Table, error) {
	hangar, err := portHangar(ctx, req, "list")
	if err != nil {
		return output.Table{}, err
	}

	table := output.Table{
		Columns: []string{"port", "scheme", "public", "url", "internal"},
		Total:   len(hangar.Ports),
		Rows:    hangarPortRows(hangar.Ports),
	}
	if len(hangar.Ports) == 0 {
		table.Footer = "This Hangar has no ports open, so nothing outside it reaches its dev servers."
	}
	return table, nil
}

// hangarPortSet opens ports, or changes the ones already open.
//
// A part left out of the argument keeps what the port already had, exactly as
// in `port set`, and a port that is not open yet takes the same defaults:
// private HTTP. The limits are checked on the set that would be sent, since
// the limit on how many is a limit on the whole set.
func hangarPortSet(ctx context.Context, req Request) (output.Table, error) {
	if len(req.Args) < 2 {
		return output.Table{}, noHangarPort(req, "set", "3000:http:public")
	}

	specs, err := parsePortSpecs(req.Args[1:])
	if err != nil {
		return output.Table{}, hangarSpecError(req, err)
	}

	hangar, err := portHangar(ctx, req, "set")
	if err != nil {
		return output.Table{}, err
	}
	existing := hangar.Endpoints()

	incoming := make([]core.NewPort, 0, len(specs))
	for _, spec := range specs {
		port := spec.withDefaults()
		if open, ok := core.FindPort(existing, spec.Port); ok {
			port = spec.appliedTo(open)
		}
		incoming = append(incoming, port)
	}

	final := core.MergePorts(existing, incoming)
	if err := core.CheckHangarPorts(final); err != nil {
		if e := clierr.AsError(err); e != nil && e.Code == "hangar.port_limit" {
			return output.Table{}, e.
				WithStep("close one", withTeam(req, "outplane", "hangar", "port", "unset", hangar.Name, "<PORT>")...).
				WithStep("see what is open", withTeam(req, "outplane", "hangar", "port", "list", hangar.Name)...)
		}
		return output.Table{}, err
	}

	return applyHangarPorts(ctx, req, hangar, "set", final, portNumbers(specs))
}

// hangarPortUnset closes ports on a Hangar.
//
// Every number is read and found before anything is sent, which is `port
// unset`'s rule: a typo in the third number does not leave the first two
// closed.
func hangarPortUnset(ctx context.Context, req Request) (output.Table, error) {
	if len(req.Args) < 2 {
		return output.Table{}, noHangarPort(req, "unset", "3000")
	}

	numbers := make([]int, 0, len(req.Args)-1)
	for _, raw := range req.Args[1:] {
		number, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil {
			return output.Table{}, clierr.New(clierr.KindUsage, "%q is not a port number", raw).
				WithCode("usage.bad_port").
				WithHint("A port is a number from 1 to 65535.")
		}
		numbers = append(numbers, number)
	}

	hangar, err := portHangar(ctx, req, "unset")
	if err != nil {
		return output.Table{}, err
	}
	existing := hangar.Endpoints()

	for _, number := range numbers {
		if _, ok := core.FindPort(existing, number); !ok {
			return output.Table{}, unknownHangarPort(req, hangar, number)
		}
	}

	final := core.WithoutPorts(existing, numbers)
	if err := core.CheckHangarPorts(final); err != nil {
		return output.Table{}, err
	}
	return applyHangarPorts(ctx, req, hangar, "unset", final, numbers)
}

// applyHangarPorts sends a whole set and reports it, as applyPorts does for an
// application, and ends where that one would start a deployment: here the save
// is the whole change.
func applyHangarPorts(ctx context.Context, req Request, hangar core.Hangar, action string,
	final []core.NewPort, named []int) (output.Table, error) {

	if req.CLI.DryRun {
		req.CLI.Out.Note("Would %s %s on %s. Nothing was sent.", action, portsPhrase(named), hangar.Name)
		req.CLI.Out.Note("The request replaces every port, and would carry: %s.", describePorts(final))
		return hangarPortChangeTable(hangar, action, named, plannedHangarPortRows(hangar, final), false), nil
	}

	client, err := req.CLI.APIClient()
	if err != nil {
		return output.Table{}, err
	}

	saved, err := core.SetHangarPorts(ctx, client, hangar, final)
	if err != nil {
		return output.Table{}, explainHangarPortRefusal(req, err, hangar, action, final)
	}

	if len(final) == 0 {
		req.CLI.Out.Note("%s now has no ports open.", hangar.Name)
	} else {
		req.CLI.Out.Note("%s now has open: %s.", hangar.Name, describePorts(final))
	}
	for _, p := range saved {
		if p.URL != "" {
			req.CLI.Out.Note("  %d is public at %s", p.Port, p.URL)
		}
	}
	req.CLI.Out.Note("Live within seconds. The Hangar was not restarted, so its terminal and agents keep running.")
	if action == "set" {
		req.CLI.Out.Note("A dev server is reached only if it listens on 0.0.0.0, not localhost.")
	}

	return hangarPortChangeTable(hangar, action, named, hangarPortRows(saved), true), nil
}

// portHangar resolves the Hangar, which is the first argument of all three
// commands.
//
// The list it is resolved from carries the ports, so for set and unset this is
// also the read that is changed and sent back, taken after the arguments have
// been checked and as close to the write as it can be.
func portHangar(ctx context.Context, req Request, verb string) (core.Hangar, error) {
	if len(req.Args) == 0 {
		return core.Hangar{}, noHangar(verb)
	}
	return targetHangar(ctx, req, verb)
}

// noHangar is targetHangar's own error, with a step that names this group.
func noHangar(verb string) error {
	return clierr.New(clierr.KindUsage, "no Hangar given").
		WithCode("usage.missing_argument").
		WithStep("see the team's Hangars", "outplane", "hangar", "list").
		WithStep(verb+" a Hangar's ports", "outplane", "hangar", "port", verb, "<HANGAR_NAME>")
}

// noHangarPort is set or unset given a Hangar and nothing to do to it, or given
// nothing at all.
func noHangarPort(req Request, verb, example string) error {
	if len(req.Args) == 0 {
		return noHangar(verb)
	}
	return clierr.New(clierr.KindUsage, "no port given").
		WithCode("usage.missing_argument").
		WithStep(verb+" one", withTeam(req, "outplane", "hangar", "port", verb, req.Args[0], example)...).
		WithStep("see what is open", withTeam(req, "outplane", "hangar", "port", "list", req.Args[0])...)
}

// hangarSpecError renames what parsePortSpecs reports for an application. The
// syntax is the same, but its duplicate code belongs to the app family and its
// steps create an application.
func hangarSpecError(req Request, err error) error {
	e := clierr.AsError(err)
	if e == nil {
		return err
	}
	switch e.Code {
	case "app.port_duplicate":
		return e.WithCode("hangar.port_duplicate")
	case "usage.bad_port":
		e.NextSteps = nil
		return e.
			WithStep("a private HTTP port", withTeam(req, "outplane", "hangar", "port", "set", req.Args[0], "3000")...).
			WithStep("a public one", withTeam(req, "outplane", "hangar", "port", "set", req.Args[0], "3000:http:public")...)
	}
	return err
}

// explainHangarPortRefusal names the two refusals only the server can make.
//
// A Hangar that never started has nothing to route its ports to, and the
// server's message already says to delete it and create it again; the steps
// make that runnable. A save whose routing failed arrives as a 500, whose
// message is held back from the terminal like any other 500's. The ports are
// saved by then, and sending the same set again is what applies them. For set
// that is the same command. For unset it is not: the ports it named are gone
// from the saved set, so the same command would find nothing to close, and
// saving a port that is still open is what sends the set again.
func explainHangarPortRefusal(req Request, err error, hangar core.Hangar, action string,
	final []core.NewPort) error {
	e := clierr.AsError(err)
	if e == nil {
		return err
	}

	switch e.Kind {
	case clierr.KindNotFound:
		if strings.Contains(e.Message, "never started") {
			return e.WithCode("hangar.never_started").
				WithStep("delete it", withTeam(req, "outplane", "hangar", "delete", hangar.Name)...).
				WithStep("create it again", withTeam(req, "outplane", "hangar", "create", hangar.Name)...)
		}
		return e.WithCode("hangar.not_found").
			WithStep("see the team's Hangars", withTeam(req, "outplane", "hangar", "list")...)
	case clierr.KindUpstream:
		if message, _ := e.Details["apiMessage"].(string); strings.Contains(message, "routing") {
			e = e.WithCode("hangar.ports_not_applied")
			list := withTeam(req, "outplane", "hangar", "port", "list", hangar.Name)
			switch {
			case action == "set":
				e.Retryable = true
				return e.WithHint("The ports were saved, but have not reached the Hangar yet. Running the "+
					"same command again applies them, and restarts nothing.").
					WithStep("see what is saved", list...)
			case len(final) > 0:
				e.Retryable = false
				return e.WithHint("The change was saved, but has not reached the Hangar yet. Saving a "+
					"port that is still open sends the whole set again and applies it.").
					WithStep("save the set again", withTeam(req, "outplane", "hangar", "port", "set",
						hangar.Name, strconv.Itoa(final[0].Port))...).
					WithStep("see what is saved", list...)
			default:
				e.Retryable = false
				return e.WithHint("The change was saved, but has not reached the Hangar yet. With no "+
					"port left to save, opening one and closing it again applies it.").
					WithStep("see what is saved", list...)
			}
		}
	}
	return err
}

func unknownHangarPort(req Request, hangar core.Hangar, number int) error {
	e := clierr.New(clierr.KindNotFound, "%s has no port %d open", hangar.Name, number).
		WithCode("hangar.port_not_found").
		WithStep("see what is open", withTeam(req, "outplane", "hangar", "port", "list", hangar.Name)...)
	if names := core.PortNumbers(hangar.Endpoints()); len(names) > 0 {
		return e.WithHint("It has open: %s.", strings.Join(names, ", ")).
			WithDetail("availablePorts", names)
	}
	return e.WithHint("It has none open at all.")
}

// hangarPortRows is the shape list, get and both mutations report a port in.
func hangarPortRows(ports []core.HangarPort) []map[string]any {
	rows := make([]map[string]any, 0, len(ports))
	for _, p := range ports {
		rows = append(rows, map[string]any{
			"port":     p.Port,
			"scheme":   p.Scheme,
			"public":   p.Public,
			"url":      nilIfEmpty(p.URL),
			"internal": p.Internal,
		})
	}
	return rows
}

// plannedHangarPortRows is the set a dry run would send. The outside address is
// null rather than guessed, because the server assigns it; the internal one
// follows from the name and is not the server's to choose.
func plannedHangarPortRows(hangar core.Hangar, ports []core.NewPort) []map[string]any {
	rows := make([]map[string]any, 0, len(ports))
	for _, p := range ports {
		rows = append(rows, map[string]any{
			"port":     p.Port,
			"scheme":   p.Scheme,
			"public":   p.Public,
			"url":      nil,
			"internal": hangar.Name + ":" + strconv.Itoa(p.Port),
		})
	}
	return rows
}

// hangarPortChangeTable reports both halves, as portChangeTable does: what was
// asked for, and the whole set that was sent.
func hangarPortChangeTable(hangar core.Hangar, action string, named []int, ports []map[string]any,
	changed bool) output.Table {

	return output.Table{
		Single:  true,
		Columns: []string{"action", "numbers", "hangar", "serving", "changed"},
		Total:   1,
		Rows: []map[string]any{{
			"action":   action,
			"numbers":  named,
			"hangar":   hangar.Name,
			"hangarId": hangar.ID,
			"serving":  len(ports),
			"ports":    ports,
			"changed":  changed,
		}},
	}
}
