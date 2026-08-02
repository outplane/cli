package commands

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/outplane/cli/internal/api"
	"github.com/outplane/cli/internal/clierr"
	"github.com/outplane/cli/internal/core"
	"github.com/outplane/cli/internal/output"
)

func init() {
	register("domain list", domainList)
	register("domain dns", domainDNS)
	register("domain add", domainAdd)
	register("domain point", domainPoint)
	register("domain unpoint", domainUnpoint)
	register("domain remove", domainRemove)
}

// domainList reports the team's routes.
func domainList(ctx context.Context, req Request) (output.Table, error) {
	client, err := req.CLI.APIClient()
	if err != nil {
		return output.Table{}, err
	}

	domains, err := core.ListDomains(ctx, client)
	if err != nil {
		return output.Table{}, err
	}

	if ref := strings.TrimSpace(req.Flags.String("app")); ref != "" {
		app, err := resolveApp(ctx, req, ref)
		if err != nil {
			return output.Table{}, err
		}
		domains = onlyForApp(domains, app.ID)
	}

	table := output.Table{
		Columns: []string{"host", "path", "app", "url"},
		Total:   len(domains),
	}
	unpointed := 0
	for _, d := range domains {
		if d.App == "" {
			unpointed++
		}
		table.Rows = append(table.Rows, domainRow(d))
	}

	if unpointed > 0 {
		table.Footer = plural(unpointed, "route") + " point nowhere and answer nothing. " +
			"Send one to an application with `outplane domain point`."
	}
	return table, nil
}

func onlyForApp(domains []core.Domain, appID string) []core.Domain {
	var kept []core.Domain
	for _, d := range domains {
		if d.AppID == appID {
			kept = append(kept, d)
		}
	}
	return kept
}

// domainDNS says what to put in a zone, and asks the server nothing.
//
// A reader setting up a domain wants this before they have created anything,
// and it is a pure function of the host: a subdomain takes a CNAME, an apex
// domain cannot have one and takes an A record.
func domainDNS(ctx context.Context, req Request) (output.Table, error) {
	if len(req.Args) == 0 || strings.TrimSpace(req.Args[0]) == "" {
		return output.Table{}, clierr.New(clierr.KindUsage, "no host given").
			WithCode("usage.missing_argument").
			WithStep("see what a host needs", "outplane", "domain", "dns", "app.example.com")
	}

	host := core.NormalizeHost(req.Args[0])
	if err := core.CheckHost(host); err != nil {
		return output.Table{}, err
	}

	record := core.RecordFor(host)
	return output.Table{
		Single:  true,
		Columns: []string{"host", "type", "name", "value"},
		Total:   1,
		Rows: []map[string]any{{
			"host":  host,
			"type":  record.Type,
			"name":  record.Name,
			"value": record.Value,
		}},
		Footer: "Add this record at your DNS provider. A certificate is issued once the " +
			"record resolves, which takes as long as your zone's TTL.",
	}, nil
}

// domainAdd registers a route, and points it when told where.
func domainAdd(ctx context.Context, req Request) (output.Table, error) {
	if len(req.Args) == 0 || strings.TrimSpace(req.Args[0]) == "" {
		return output.Table{}, clierr.New(clierr.KindUsage, "no host given").
			WithCode("usage.missing_argument").
			WithStep("add a domain", "outplane", "domain", "add", "app.example.com",
				"--app", "<APP_NAME>", "--port", "3000")
	}

	host := core.NormalizeHost(req.Args[0])
	path := core.NormalizeDomainPath(req.Flags.String("path"))

	if err := core.CheckHost(host); err != nil {
		return output.Table{}, err
	}
	if err := core.CheckDomainPath(req.Flags.String("path")); err != nil {
		return output.Table{}, err
	}

	client, err := req.CLI.APIClient()
	if err != nil {
		return output.Table{}, err
	}

	portID, appName, err := targetPort(ctx, req, client)
	if err != nil {
		return output.Table{}, err
	}

	record := core.RecordFor(host)

	if req.CLI.DryRun {
		req.CLI.Out.Note("Would add %s%s. Nothing was sent.", host, pathSuffix(path))
		describeDNS(req, host, record)
		return domainSingle(core.Domain{Host: host, Path: path, App: appName}, false), nil
	}

	domain, err := core.AddDomain(ctx, client, host, path, portID)
	if err != nil {
		// The server's refusals here are about what already exists, and it says
		// which. What it cannot say is where to look, so that is added and the
		// message is left alone.
		if e := clierr.AsError(err); e != nil && e.Kind == clierr.KindUsage {
			return output.Table{}, e.WithStep("see the team's domains", "outplane", "domain", "list")
		}
		return output.Table{}, err
	}

	req.CLI.Out.Note("Added %s.", domain.URL)

	// Whether it points somewhere is decided by what was asked for, not by the
	// response: the create endpoint answers without the application's name even
	// when it attached one, and reading it back would say "points nowhere"
	// about a route that does.
	if portID == "" {
		req.CLI.Out.Note("It points nowhere yet: outplane domain point %s --app <APP_NAME> --port <PORT>", host)
	} else {
		req.CLI.Out.Note("It goes to %s.", appName)
		domain.App = appName
	}
	describeDNS(req, host, record)

	return domainSingle(domain, true), nil
}

// describeDNS prints the record to create, every time a domain is added,
// because a domain that resolves nowhere is the normal first failure and the
// fix is always this.
func describeDNS(req Request, host string, record core.DNSRecord) {
	req.CLI.Out.Note("DNS: %s %s → %s", record.Type, record.Name, record.Value)
	req.CLI.Out.Note("A certificate is issued once that resolves.")
}

// domainPoint sends a route to an application's port.
func domainPoint(ctx context.Context, req Request) (output.Table, error) {
	domain, err := targetDomain(ctx, req, "point")
	if err != nil {
		return output.Table{}, err
	}

	client, err := req.CLI.APIClient()
	if err != nil {
		return output.Table{}, err
	}

	portID, appName, err := targetPort(ctx, req, client)
	if err != nil {
		return output.Table{}, err
	}
	if portID == "" {
		return output.Table{}, clierr.New(clierr.KindUsage, "no application and port given").
			WithCode("usage.missing_argument").
			WithHint("A route goes to one port of one application.").
			WithStep("point it", "outplane", "domain", "point", domain.Host,
				"--app", "<APP_NAME>", "--port", "3000")
	}

	if req.CLI.DryRun {
		req.CLI.Out.Note("Would point %s at %s. Nothing was sent.", domain.URL, appName)
		return domainSingle(domain, false), nil
	}

	updated, err := core.PointDomain(ctx, client, domain.ID, portID, domain.Path)
	if err != nil {
		return output.Table{}, err
	}

	req.CLI.Out.Note("%s now goes to %s.", updated.URL, appName)
	return domainSingle(updated, true), nil
}

// domainUnpoint leaves the route registered and sends it nowhere.
//
// Separate from removing it, because the two differ in what has to happen
// afterwards: an unpointed route keeps the host reserved and its certificate,
// and a removed one gives both up.
func domainUnpoint(ctx context.Context, req Request) (output.Table, error) {
	domain, err := targetDomain(ctx, req, "unpoint")
	if err != nil {
		return output.Table{}, err
	}

	client, err := req.CLI.APIClient()
	if err != nil {
		return output.Table{}, err
	}

	if domain.App == "" {
		req.CLI.Out.Note("%s already points nowhere.", domain.Host)
		return domainSingle(domain, false), nil
	}

	if req.CLI.DryRun {
		req.CLI.Out.Note("Would stop %s going to %s. Nothing was sent.", domain.URL, domain.App)
		return domainSingle(domain, false), nil
	}

	updated, err := core.PointDomain(ctx, client, domain.ID, "", domain.Path)
	if err != nil {
		return output.Table{}, err
	}

	req.CLI.Out.Note("%s no longer goes to %s. The route and its certificate are kept.",
		updated.URL, domain.App)
	return domainSingle(updated, true), nil
}

// domainRemove deletes a route.
func domainRemove(ctx context.Context, req Request) (output.Table, error) {
	domain, err := targetDomain(ctx, req, "remove")
	if err != nil {
		return output.Table{}, err
	}

	client, err := req.CLI.APIClient()
	if err != nil {
		return output.Table{}, err
	}

	if req.CLI.DryRun {
		req.CLI.Out.Note("%s would stop answering, and its certificate would be given up.", domain.URL)
		return domainSingle(domain, false), nil
	}

	if err := checkDomainConfirmed(req, domain); err != nil {
		return output.Table{}, err
	}

	if err := core.RemoveDomain(ctx, client, domain.ID); err != nil {
		return output.Table{}, err
	}

	req.CLI.Out.Note("Removed %s.", domain.URL)
	return domainSingle(domain, true), nil
}

func checkDomainConfirmed(req Request, domain core.Domain) error {
	confirm := func(hint string, args ...any) error {
		e := clierr.New(clierr.KindConfirmation, "removing %s needs confirmation", domain.Host).
			WithCode("confirmation.required").
			WithHint(hint, args...)

		argv := []string{"outplane", "domain", "remove", domain.Host}
		if domain.Path != "/" {
			argv = append(argv, "--path", domain.Path)
		}
		return e.WithConfirmCommand(append(argv, "--yes", "--confirm-name", domain.Host)...)
	}

	if harness := req.CLI.Exec.AgentHarness; harness != "" {
		return confirm("This is running under %s, where the CLI cannot be the thing that "+
			"approves taking a public address down. Hand the command below to whoever is "+
			"accountable for it.", harness)
	}

	if !req.Flags.Bool("yes") || req.Flags.String("confirm-name") == "" {
		return confirm("Traffic to %s stops immediately and the certificate is given up. "+
			"Both --yes and --confirm-name are required.", domain.Host)
	}

	if given := req.Flags.String("confirm-name"); given != domain.Host {
		return clierr.New(clierr.KindUsage,
			"--confirm-name says %q and the domain is %q", given, domain.Host).
			WithCode("domain.confirm_name_mismatch").
			WithDetail("expected", domain.Host).
			WithDetail("given", given)
	}
	return nil
}

// targetPort resolves --app and --port into the port record a route binds to.
//
// A route is attached to a port rather than to an application, so the number
// has to become an id. The application's own ports are the only place that
// mapping exists.
func targetPort(ctx context.Context, req Request, client *api.Client) (portID, appName string, err error) {
	ref := strings.TrimSpace(req.Flags.String("app"))
	rawPort := strings.TrimSpace(req.Flags.String("port"))

	if ref == "" && rawPort == "" {
		return "", "", nil
	}
	if ref == "" {
		return "", "", clierr.New(clierr.KindUsage, "--port needs --app").
			WithCode("domain.app_required").
			WithHint("A port number only identifies a port within one application.")
	}

	app, err := resolveApp(ctx, req, ref)
	if err != nil {
		return "", "", err
	}

	detail, err := core.GetApp(ctx, client, app.ID)
	if err != nil {
		return "", "", err
	}
	if len(detail.Endpoints) == 0 {
		return "", "", clierr.New(clierr.KindUsage, "%s serves no ports", app.Name).
			WithCode("domain.no_ports").
			WithHint("A domain has to point at a port, and this application has none.")
	}

	if rawPort == "" {
		if len(detail.Endpoints) != 1 {
			return "", "", choosePort(app.Name, detail.Endpoints)
		}
		return detail.Endpoints[0].PortID, app.Name, nil
	}

	number, err := strconv.Atoi(rawPort)
	if err != nil {
		return "", "", clierr.New(clierr.KindUsage, "--port is not a number: %q", rawPort).
			WithCode("usage.bad_port")
	}
	for _, e := range detail.Endpoints {
		if e.Port == number {
			return e.PortID, app.Name, nil
		}
	}
	return "", "", choosePort(app.Name, detail.Endpoints)
}

func choosePort(app string, endpoints []core.Endpoint) error {
	ports := make([]string, 0, len(endpoints))
	for _, e := range endpoints {
		ports = append(ports, strconv.Itoa(e.Port)+"/"+e.Scheme)
	}
	return clierr.New(clierr.KindUsage, "%s does not serve that port", app).
		WithCode("domain.port_not_found").
		WithHint("It serves: %s. A TCP port cannot take a domain.", strings.Join(ports, ", ")).
		WithDetail("ports", ports)
}

// targetDomain resolves the host argument, and the path when the host carries
// more than one route.
func targetDomain(ctx context.Context, req Request, verb string) (core.Domain, error) {
	if len(req.Args) == 0 || strings.TrimSpace(req.Args[0]) == "" {
		return core.Domain{}, clierr.New(clierr.KindUsage, "no domain given").
			WithCode("usage.missing_argument").
			WithStep("see the team's domains", "outplane", "domain", "list").
			WithStep(verb+" one", "outplane", "domain", verb, "app.example.com")
	}

	client, err := req.CLI.APIClient()
	if err != nil {
		return core.Domain{}, err
	}

	domain, err := core.FindDomain(ctx, client, req.Args[0], req.Flags.String("path"))
	if err == nil {
		return domain, nil
	}

	var notFound *core.DomainNotFoundError
	if errors.As(err, &notFound) {
		e := clierr.New(clierr.KindNotFound, "%v", notFound).
			WithCode("domain.not_found").
			WithStep("see the team's domains", "outplane", "domain", "list")
		if len(notFound.Available) > 0 {
			e = e.WithHint("This team has: %s.", strings.Join(notFound.Available, ", ")).
				WithDetail("availableDomains", notFound.Available)
		} else {
			e = e.WithHint("This team has no custom domains yet.")
		}
		return core.Domain{}, e
	}

	var ambiguous *core.AmbiguousDomainError
	if errors.As(err, &ambiguous) {
		return core.Domain{}, clierr.New(clierr.KindUsage, "%v", ambiguous).
			WithCode("domain.ambiguous").
			WithHint("It carries: %s. Name one with --path.", strings.Join(ambiguous.Paths, ", ")).
			WithDetail("paths", ambiguous.Paths)
	}

	return core.Domain{}, err
}

func pathSuffix(path string) string {
	if path == "/" {
		return ""
	}
	return " at " + path
}

func domainRow(d core.Domain) map[string]any {
	return map[string]any{
		"id":     nilIfEmpty(d.ID),
		"host":   d.Host,
		"path":   d.Path,
		"app":    nilIfEmpty(d.App),
		"appId":  nilIfEmpty(d.AppID),
		"portId": nilIfEmpty(d.PortID),
		"ssl":    d.SSL,
		"url":    nilIfEmpty(d.URL),
	}
}

func domainSingle(d core.Domain, changed bool) output.Table {
	row := domainRow(d)
	row["changed"] = changed
	if d.URL == "" {
		row["url"] = fmt.Sprintf("https://%s%s", d.Host, pathOrEmpty(d.Path))
	}
	return output.Table{
		Single:  true,
		Columns: []string{"host", "path", "app", "url", "changed"},
		Total:   1,
		Rows:    []map[string]any{row},
	}
}

func pathOrEmpty(path string) string {
	if path == "/" {
		return ""
	}
	return path
}
