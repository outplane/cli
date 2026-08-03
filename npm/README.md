# outplane

Deploy and operate applications on [Out Plane](https://outplane.com) from your
terminal, your scripts, and your agents.

```bash
npm i -g outplane
```

This package is a delivery mechanism, not a JavaScript application. What it
installs is a single static Go binary; npm is how that binary reaches Windows,
and how teams who install every tool this way can install this one.

On macOS and Linux there is a second channel that needs no Node at all:

```bash
curl -fsSL https://outplane.com/install.sh | sh
```

## Getting started

```bash
outplane login            # opens the console; approve there, nothing to copy
outplane link checkout    # remember this directory's team and app
outplane status           # what the next command will act on, and why
outplane app list         # everything in the current team
```

`login` opens a browser and waits: the console hands the token to a listener on
`127.0.0.1`, so no secret is copied, pasted, or left in a scrollback. On a
machine with no browser it prints the page address and reads a pasted token
instead. In CI, set `OUTPLANE_TOKEN` and skip signing in altogether.

Tokens are kept in the operating system's keychain. Where there is none, which
is every container and most headless servers, they go into a file readable only
by its owner, and `outplane status` says which of the two answered.

## What it covers

85 commands, over applications, deployments, environment variables, ports,
build settings, custom domains, volumes, managed PostgreSQL, IP access
profiles, registry credentials, logs, requests and metrics.

`outplane --help` lists them. Every one has its own help, with runnable
examples.

## Teach your coding agent

```bash
outplane skills install
```

Installs a skill that tells an agent which command answers which question, what
needs a deployment and what does not, and why a destructive command hands back
an invocation instead of asking. It is open source at
[outplane/skills](https://github.com/outplane/skills).

This package installs the binary and nothing else, with no install script, so
unlike the shell installer it does not do this for you.

## For AI agents and CI

The whole command surface is machine-readable, with no authentication, no
configuration file and no network call:

```bash
outplane schema                    # every command, flag, output field, error code
outplane app list --json --fields name,status
```

Output is human-formatted on a terminal and JSON when piped; streams are NDJSON.
Exit codes are a stable, append-only contract (`outplane help exit-codes`).
Errors arrive as an object with a stable code, a hint and runnable next steps.
Destructive commands never prompt: they exit 4 and hand back the exact command
that would proceed, so the approval gate lives in your harness rather than in a
terminal an agent does not have.

## How this package works

Installing `outplane` pulls in exactly one more package, the binary for your
platform:

| Package | Contents |
| --- | --- |
| `@outplane/cli-darwin-arm64` | macOS, Apple silicon |
| `@outplane/cli-darwin-amd64` | macOS, Intel |
| `@outplane/cli-linux-arm64` | Linux, arm64 |
| `@outplane/cli-linux-amd64` | Linux, x86-64 |
| `@outplane/cli-windows-arm64` | Windows, arm64 |
| `@outplane/cli-windows-amd64` | Windows, x86-64 |

They are optional dependencies with `os` and `cpu` set, so npm downloads the one
that matches and skips the other five. There is no postinstall script and
nothing is fetched afterwards: a package that downloads its binary after install
has an integrity check covering nothing, and whatever npm verified here is what
runs.

The `outplane` command itself is a small Node shim that finds that binary and
runs it, inheriting your terminal and passing back its exit code, so an
interactive session such as `outplane app shell` behaves exactly as it would
without npm in the middle.

Every release publishes all seven packages at the same version, alongside the
archives and checksums on the
[releases page](https://github.com/outplane/cli/releases).

## Links

- Documentation: <https://docs.outplane.com/cli>
- Source: <https://github.com/outplane/cli>
- Issues: <https://github.com/outplane/cli/issues>

## License

[Apache-2.0](https://github.com/outplane/cli/blob/master/LICENSE).
Copyright 2026 Out Plane LLC.
