package commands

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/outplane/cli/internal/clierr"
	"github.com/outplane/cli/internal/core"
	"github.com/outplane/cli/internal/output"
)

func init() {
	register("deploy logs", deployLogs)
}

// deployLogs prints one deployment's build output.
//
// It needs no application reference, unlike almost every other command: the
// build log endpoint is keyed by deployment id alone and authorises on team
// membership. The build's own pod phase, which comes back beside the text, is
// what tells --follow when to stop, so there is nothing else to ask for either.
func deployLogs(ctx context.Context, req Request) (output.Table, error) {
	client, err := req.CLI.APIClient()
	if err != nil {
		return output.Table{}, err
	}

	id, err := deploymentArg(req.Args[0])
	if err != nil {
		return output.Table{}, err
	}

	deadline, err := waitDeadline(req)
	if err != nil {
		return output.Table{}, err
	}

	follow := req.Flags.Bool("follow")
	reader := &core.BuildLogReader{DeploymentID: id}
	wrote := false

	for {
		text, finished, err := reader.Next(ctx, client)
		if err != nil {
			// An interrupt cancels the read in flight. Without this the loop
			// would take the cancelled read for the end of the build output
			// and exit 0 on a command the reader stopped.
			if e := clierr.Cancelled(ctx, "interrupted"); e != nil {
				return output.Table{}, e
			}
			// Nothing has been printed yet, so this is the whole answer and it
			// can be explained properly. Once output has started, a later
			// failure ends the stream without pretending the earlier text was
			// wrong.
			if !wrote {
				return output.Table{}, explainMissingBuild(id, err)
			}
			return streamed(), nil
		}

		if text != "" {
			fmt.Fprint(req.CLI.Out.Out, text)
			wrote = true
		}

		if !follow || finished {
			if !wrote {
				req.CLI.Out.Note("Deployment %d has produced no build output.", id)
			}
			return streamed(), nil
		}

		if time.Now().After(deadline) {
			return output.Table{}, clierr.New(clierr.KindTimeout,
				"the build for deployment %d had not finished when the timeout expired", id).
				WithCode("deploy.timeout").
				WithHint("The build is still running; only this command stopped watching.")
		}

		select {
		case <-ctx.Done():
			return output.Table{}, clierr.New(clierr.KindInterrupted, "interrupted")
		case <-time.After(pollInterval):
		}
	}
}

// streamed reports that the handler has already written everything.
func streamed() output.Table { return output.Table{Streamed: true} }

// deploymentArg parses the id, which is the one argument in the CLI that is a
// number rather than a name.
func deploymentArg(raw string) (int, error) {
	id, err := strconv.Atoi(raw)
	if err != nil || id <= 0 {
		return 0, clierr.New(clierr.KindUsage, "%q is not a deployment id", raw).
			WithCode("usage.bad_deployment_id").
			WithHint("A deployment id is a number, reported by `outplane deploy create`.").
			WithStep("see recent deployments", "outplane", "deploy", "list")
	}
	return id, nil
}

// explainMissingBuild turns a failed first read into the answers that are
// actually true, which the status code alone cannot distinguish.
//
// Three things arrive here identically: the deployment does not exist, it
// deployed an image that was already built so there was never a build, or the
// build's output has since been cleaned up. None of them is an empty log, and
// reporting one would read as "the build printed nothing".
func explainMissingBuild(id int, err error) error {
	// A credential or quota problem is about the caller, not the deployment.
	// Rewriting those would send somebody to fix the wrong thing.
	var e *clierr.Error
	if errors.As(err, &e) && (e.Kind == clierr.KindAuth || e.Kind == clierr.KindQuota) {
		return err
	}

	// Everything else is rewritten, the server's own wording included. Asking
	// for a deployment that does not exist currently answers "Sequence contains
	// no elements", which is a database library talking to itself. The original
	// stays in details for a bug report; the reader gets a sentence about their
	// deployment.
	return clierr.New(clierr.KindNotFound, "no build output for deployment %d", id).
		WithCode("deploy.no_build").
		WithHint("Either the deployment does not exist, or it deployed an image that was "+
			"already built so there was no build, or the output has since been cleaned up.").
		WithStep("see this team's applications", "outplane", "app", "list").
		WithDetail("reason", err.Error())
}
