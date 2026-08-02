package commands

import (
	"context"
	"encoding/json"
	"io"
	"os"

	"github.com/outplane/cli/internal/api"
	"github.com/outplane/cli/internal/clierr"
	"github.com/outplane/cli/internal/core"
	"github.com/outplane/cli/internal/output"
)

func init() {
	register("api", rawAPI)
}

// The escape hatch.
//
// Every other command in this CLI is a promise: it knows what it asks for, what
// comes back and what can go wrong. This one promises only the credential and
// the transport. It exists because a CLI that covers most of an API leaves
// people reaching for curl and pasting a token onto a command line, which is
// worse in every way that matters.
//
// Two things make it more than a curl alias, and both are the reason it is
// worth having rather than documenting curl:
//
// The credential never appears. It comes from the same store every other
// command uses, so it is not in argv, not in shell history and not in a CI log.
//
// It is not a hole in the safety model. `app delete` refuses to run under an
// agent harness and demands two flags from a human; routing the same call
// through here would make that theatre. A write through this command therefore
// carries the same gate, minus the resource-specific confirmation it cannot
// know how to ask for.
func rawAPI(ctx context.Context, req Request) (output.Table, error) {
	if len(req.Args) < 2 {
		return output.Table{}, missingCall(req)
	}

	body, err := core.ReadBody(req.Flags.String("data"), os.ReadFile, readStdin)
	if err != nil {
		return output.Table{}, err
	}

	call, err := core.NewRawRequest(req.Args[0], req.Args[1], req.Flags.Strings("query"), body)
	if err != nil {
		return output.Table{}, err
	}

	if err := checkCallAllowed(req, call); err != nil {
		return output.Table{}, err
	}

	client, err := req.CLI.APIClient()
	if err != nil {
		return output.Table{}, err
	}

	if req.CLI.DryRun {
		req.CLI.Out.Note("Would send %s %s. Nothing was sent.", call.Method, call.Path)
		if len(call.Body) > 0 {
			req.CLI.Out.Note("Body: %s", short(string(call.Body)))
		}
		return streamed(), nil
	}

	response, err := core.Call(ctx, client, call)
	if err != nil {
		return output.Table{}, err
	}

	// Written here rather than through the table renderer, because there are no
	// columns to render: the shape belongs to whichever endpoint was called and
	// this command has never seen it.
	return streamed(), writeJSON(req, chosen(req, response))
}

// chosen picks which half of the answer to print.
//
// The envelope's data is what every other command would have decoded, and is
// what somebody wants nine times in ten. --raw gives the whole reply, including
// the statusCode and message fields this CLI normally hides, for the tenth:
// when the envelope itself is the question.
//
// An endpoint that returns nothing prints null rather than an empty line, so
// that the output is always one JSON document and a consumer never has to
// handle "sometimes empty".
func chosen(req Request, r api.RawResponse) json.RawMessage {
	if req.Flags.Bool("raw") && len(r.Envelope) > 0 {
		return r.Envelope
	}
	if len(r.Data) > 0 {
		return r.Data
	}
	return json.RawMessage("null")
}

// writeJSON prints one JSON document, indented, with nothing around it.
func writeJSON(req Request, value any) error {
	enc := json.NewEncoder(req.CLI.Out.Out)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(value); err != nil {
		return clierr.New(clierr.KindInternal, "could not write the response: %v", err)
	}
	return nil
}

// checkCallAllowed applies the confirmation this command cannot infer.
//
// A read is always allowed: it is exactly what the other read commands do, with
// a path the CLI has not been taught. Anything else changes something the CLI
// cannot name, so it is acknowledged once with --yes, and refused outright
// under an agent harness. That refusal is the point: without it, `api DELETE`
// would be the way around every gate `app delete` has.
func checkCallAllowed(req Request, call core.RawRequest) error {
	if call.Reads() {
		return nil
	}

	argv := append([]string{"outplane", "api", call.Method, call.Path}, "--yes")

	if harness := req.CLI.Exec.AgentHarness; harness != "" {
		return clierr.New(clierr.KindConfirmation,
			"a %s through the escape hatch needs a person, and this is running under %s",
			call.Method, harness).
			WithCode("confirmation.required").
			WithHint("This command can call any endpoint, including the ones behind "+
				"`app delete` and `db delete`. Those refuse to run here for a reason, and "+
				"reaching them through this command would be the same act with none of the "+
				"checks. A command that names what it changes is the way to do this.").
			WithStep("see which commands exist", "outplane", "schema").
			WithConfirmCommand(argv...)
	}

	if !req.Flags.Bool("yes") {
		return clierr.New(clierr.KindConfirmation,
			"a %s through the escape hatch needs acknowledging", call.Method).
			WithCode("confirmation.required").
			WithHint("This command cannot tell what the endpoint does, so it cannot warn "+
				"about it, name the resource or offer to undo it. --yes says you know "+
				"what %s %s does.", call.Method, call.Path).
			WithStep("see what would be sent", "outplane", "api", call.Method, call.Path, "--dry-run").
			WithConfirmCommand(argv...)
	}
	return nil
}

func missingCall(req Request) error {
	e := clierr.New(clierr.KindUsage, "no method and path given").
		WithCode("usage.missing_argument").
		WithHint("Give an HTTP method and a path, which is the part after /api.").
		WithStep("read something", "outplane", "api", "GET", "/App/GetAppsByTeamId").
		WithStep("see the endpoints the CLI itself calls", "outplane", "schema")
	if len(req.Args) == 1 {
		return e.WithDetail("given", req.Args[0])
	}
	return e
}

func readStdin() ([]byte, error) { return io.ReadAll(os.Stdin) }
