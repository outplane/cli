package commands

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/outplane/cli/internal/clierr"
	"github.com/outplane/cli/internal/config"
	"github.com/outplane/cli/internal/output"
	"github.com/pkg/browser"
	"golang.org/x/term"
)

func init() {
	register("login", login)
	register("logout", logout)
	register("whoami", whoami)
}

// consoleTokensPath is where a user creates a token. Deriving the console host
// from the API host means a local or self-hosted deployment sends people to its
// own console rather than to production.
const consoleTokensPath = "/settings/api-tokens"

// login stores a token against the team it belongs to.
//
// Entirely local. The token carries everything needed to file it: its team id,
// its team's slug and its expiry are all claims, so signing in works on an
// aeroplane and a first run needs nothing but a paste.
//
// There used to be a verification request here, which existed to learn the slug
// rather than to verify: the slug was not a claim, and without it `--team acme`
// would have had to be `--team 3f2a9c14-...`. Once the slug moved into the
// token the request had no job left. It did catch a revoked token a few seconds
// earlier than the first real command would, which is not worth a network
// dependency on the one command a new user runs first.
func login(_ context.Context, req Request) (output.Table, error) {
	cli := req.CLI

	token, err := readToken(req)
	if err != nil {
		return output.Table{}, err
	}

	info, err := config.InspectToken(token)
	if err != nil {
		return output.Table{}, clierr.New(clierr.KindUsage, "%v", err).
			WithCode("auth.token_malformed").
			WithHint("An Out Plane token is a long string beginning with \"ey\". "+
				"Copy the whole value, including any trailing characters.").
			WithStep("create a token in the console", "outplane", "login", "--no-browser")
	}

	// A token minted before the slug became a claim. Rather than fall back to a
	// network lookup, which would keep the dependency alive forever for a case
	// that empties itself within one token lifetime, say what to do.
	if info.TeamSlug == "" {
		return output.Table{}, clierr.New(clierr.KindUsage, "this token does not name its team").
			WithCode("auth.token_pre_slug").
			WithHint("It was issued before tokens carried a team name. Create a new one; "+
				"the old token keeps working for anything already using it.").
			WithStep("create a replacement", "outplane", "login")
	}
	slug := info.TeamSlug

	existing, alreadyStored := config.FindCredential(slug)
	changed := !alreadyStored || existing.Token != token

	expiresAt := ""
	if !info.ExpiresAt.IsZero() {
		expiresAt = info.ExpiresAt.UTC().Format(time.RFC3339)
	}

	if err := config.StoreCredential(config.Credential{
		Token:     token,
		TeamID:    info.TeamID,
		TeamSlug:  slug,
		Name:      defaultTokenName(),
		ExpiresAt: expiresAt,
	}); err != nil {
		return output.Table{}, clierr.New(clierr.KindInternal, "could not store the token: %v", err)
	}

	cli.Out.Note("Signed in to %s.", slug)

	return output.Table{
		Single:  true,
		Columns: []string{"teamSlug", "teamId", "expiresAt", "changed"},
		Total:   1,
		Rows: []map[string]any{{
			"teamSlug":  slug,
			"teamId":    info.TeamID,
			"expiresAt": nilIfEmpty(expiresAt),
			"changed":   changed,
		}},
	}, nil
}

// readToken obtains the token, by whichever route the environment allows.
func readToken(req Request) (string, error) {
	// An explicit flag wins, though the help text discourages it: argv is
	// visible in process lists and CI logs.
	if v := strings.TrimSpace(req.Flags.String("token")); v != "" {
		return v, nil
	}

	if req.Flags.Bool("token-stdin") {
		return readTokenFromStdin()
	}

	// Without a terminal there is nobody to prompt. Say so, rather than
	// hanging on a read that will never return.
	if !req.CLI.Exec.Interactive() {
		return "", clierr.New(clierr.KindUsage, "no terminal available to prompt for a token").
			WithCode("auth.no_terminal").
			WithHint("Signing in needs either a terminal or an explicit token.").
			WithStep("pipe a token in", "cat", "token.txt", "|", "outplane", "login", "--token-stdin").
			WithStep("or skip signing in entirely", "OUTPLANE_TOKEN=<TOKEN>", "outplane", "app", "list")
	}

	openConsole(req)
	return promptForToken(req.CLI.Out)
}

// readTokenFromStdin reads a token piped in, tolerating a trailing newline.
func readTokenFromStdin() (string, error) {
	raw, err := io.ReadAll(os.Stdin)
	if err != nil {
		return "", clierr.New(clierr.KindUsage, "could not read the token from standard input: %v", err)
	}
	token := strings.TrimSpace(string(raw))
	if token == "" {
		return "", clierr.New(clierr.KindUsage, "no token was supplied on standard input").
			WithCode("auth.token_missing")
	}
	return token, nil
}

// openConsole points the user at the page where tokens are created.
//
// The URL is always printed, and printed BEFORE the browser is opened. Two
// common failures make that worth the extra line: the browser opens in a
// profile signed in as somebody else, or it opens behind the terminal and the
// user never sees it. In both cases a visible URL is the only recovery.
func openConsole(req Request) {
	url := consoleURL(req.CLI.Config.APIURL.Value) + consoleTokensPath

	req.CLI.Out.Note("Create a token here, then paste it below:")
	req.CLI.Out.Note("  %s", url)
	req.CLI.Out.Note("")

	if req.Flags.Bool("no-browser") || !req.CLI.Exec.CanOpenBrowser() {
		return
	}
	// A browser that will not open is not an error: the URL is already on
	// screen and the user can open it themselves.
	_ = browser.OpenURL(url)
}

// consoleURL derives the console host from the API host, so that a local or
// self-hosted deployment sends people to its own console.
//
// Falls back to production when the API URL is not one we recognise, because a
// wrong-looking link is more useful than no link.
func consoleURL(apiURL string) string {
	const production = "https://console.outplane.com"

	trimmed := strings.TrimPrefix(strings.TrimPrefix(apiURL, "https://"), "http://")
	host, _, _ := strings.Cut(trimmed, "/")

	switch {
	case strings.HasPrefix(host, "api."):
		return "https://console." + strings.TrimPrefix(host, "api.")
	case strings.HasPrefix(host, "localhost"), strings.HasPrefix(host, "127.0.0.1"):
		return "http://localhost:5173"
	default:
		return production
	}
}

// promptForToken reads the token without echoing it.
//
// Echo is disabled so the token does not end up in a screen recording, a
// screen share, or a scrollback buffer that someone later pastes into an issue.
func promptForToken(out *output.Writer) (string, error) {
	fmt.Fprint(out.Err, "Token: ")

	raw, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(out.Err)
	if err != nil {
		return "", clierr.New(clierr.KindUsage, "could not read the token: %v", err)
	}

	token := strings.TrimSpace(string(raw))
	if token == "" {
		return "", clierr.New(clierr.KindUsage, "no token was entered").
			WithCode("auth.token_missing")
	}
	return token, nil
}

// defaultTokenName labels the credential with the machine it lives on, so that
// a console showing three tokens says which laptop each belongs to, and losing
// a machine makes it obvious which one to revoke.
func defaultTokenName() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		return "cli"
	}
	// Strip a trailing .local, which macOS adds and nobody wants to read.
	return "cli: " + strings.TrimSuffix(host, ".local")
}

func logout(_ context.Context, req Request) (output.Table, error) {
	all := req.Flags.Bool("all")

	var removed []string
	if all {
		for _, c := range config.SignedInTeams() {
			removed = append(removed, c.TeamSlug)
		}
		if err := config.ForgetCredential(""); err != nil {
			return output.Table{}, clierr.New(clierr.KindInternal, "could not remove the credentials: %v", err)
		}
	} else {
		slug := req.CLI.Config.TeamSlug.Value
		if slug == "" {
			slug = config.ActiveTeamSlug()
		}
		if slug == "" {
			// Nothing to do is not a failure. Logging out twice should be
			// quiet, so that a teardown script can run unconditionally.
			req.CLI.Out.Note("Not signed in; nothing to remove.")
			return logoutResult(nil), nil
		}
		removed = []string{slug}
		if err := config.ForgetCredential(slug); err != nil {
			return output.Table{}, clierr.New(clierr.KindInternal, "could not remove the credential: %v", err)
		}
	}

	if len(removed) > 0 {
		req.CLI.Out.Note("Removed the stored credential for %s.", strings.Join(removed, ", "))
		req.CLI.Out.Note("The token itself is still valid. Revoke it in the console to disable it.")
	}
	return logoutResult(removed), nil
}

func logoutResult(removed []string) output.Table {
	if removed == nil {
		removed = []string{}
	}
	return output.Table{
		Single:  true,
		Columns: []string{"removed", "changed"},
		Total:   1,
		Rows: []map[string]any{{
			"removed": removed,
			"changed": len(removed) > 0,
		}},
	}
}

// whoami reports the active credential without touching the network.
//
// Answering offline is the point: "which team am I about to act as" is exactly
// the question worth asking when something is already going wrong, including
// when the API is unreachable.
func whoami(_ context.Context, req Request) (output.Table, error) {
	if req.CLI.Config.TeamError != nil {
		return output.Table{}, req.CLI.SignInError()
	}

	slug := req.CLI.Config.TeamSlug.Value
	cred, _ := config.FindCredential(slug)

	// Expiry is read from the token in use rather than from the credential
	// stored beside it. A token supplied through the environment has no stored
	// credential, so trusting that copy would report "never expires" for a
	// token that expires next week. See config.ExpiryOf.
	expiresAt, daysLeft := config.ExpiryOf(req.CLI.Config.Token.Value)

	return output.Table{
		Single:  true,
		Columns: []string{"teamSlug", "teamId", "tokenName", "expiresAt", "daysLeft", "source"},
		Total:   1,
		Rows: []map[string]any{{
			// Null, not the team id, when no slug is known. A slug is only
			// learned by signing in, and an environment token never does. The
			// id is right there in the next field; putting it here too would
			// hand a caller a GUID that looks like a slug and does not work as
			// one wherever slugs are accepted.
			"teamSlug":  nilIfEmpty(slug),
			"teamId":    req.CLI.Config.TeamID.Value,
			"tokenName": nilIfEmpty(cred.Name),
			"expiresAt": nilIfEmpty(expiresAt),
			"daysLeft":  daysLeftValue(expiresAt, daysLeft),
			"source":    string(req.CLI.Config.Token.Source),
		}},
	}, nil
}

// nilIfEmpty renders an absent value as JSON null rather than an empty string,
// so that a consumer can tell "no expiry" from "expiry unknown".
func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// daysLeftValue renders days remaining, or null when the token has no expiry.
//
// Null rather than -1, which is what this used to return for "never expires". A
// token that expired thirty hours ago also produces -1, because the division
// truncates toward zero, so the same value meant both "this never expires" and
// "this expired yesterday". Those are opposite facts and a caller deciding
// whether to rotate would act on either.
//
// Null now means there is no expiry at all; a negative number means the expiry
// has passed and by how much.
func daysLeftValue(expiresAt string, days int) any {
	if expiresAt == "" {
		return nil
	}
	return days
}
