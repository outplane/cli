# Out Plane CLI

Deploy and operate applications on [Out Plane](https://outplane.com) from your
terminal, your scripts, and your agents.

[![CI](https://github.com/outplane/cli/actions/workflows/ci.yml/badge.svg)](https://github.com/outplane/cli/actions/workflows/ci.yml)
[![npm](https://img.shields.io/npm/v/outplane)](https://www.npmjs.com/package/outplane)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue)](LICENSE)

A single static binary. No runtime, no interpreter, nothing to install
alongside it.

## Install

**macOS and Linux**

```bash
curl -fsSL https://outplane.com/install.sh | sh
```

Downloads the binary for your platform, checks it against the release's own
checksums, and puts it in `/usr/local/bin` when that is writable or
`~/.local/bin` when it is not. It never asks for `sudo`.

It also installs the [agent skill](https://github.com/outplane/skills) into any
coding tool it finds, so an agent knows how to use this CLI rather than guessing
at it. `OUTPLANE_SKIP_SKILLS=1` leaves your editor's configuration alone.

Pin a version, or choose where it goes:

```bash
OUTPLANE_VERSION=v0.2.4 OUTPLANE_INSTALL_DIR=~/bin \
  sh -c "$(curl -fsSL https://outplane.com/install.sh)"
```

**Windows, macOS and Linux**

```bash
npm i -g outplane
```

This package installs the binary and nothing else, with no install script, so
the agent skill is a separate step there: `outplane skills install`.

**Anywhere else**

Download an archive from
[releases](https://github.com/outplane/cli/releases), check it against
`checksums.txt`, and put the binary on your `PATH`.

Afterwards:

```bash
outplane update           # replaces itself, whichever way it was installed
outplane version
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

85 commands. `outplane --help` lists them, and every one has its own help with
runnable examples.

| Group | What it does |
| --- | --- |
| `app` | create, list, inspect, scale, pause, rename, delete, and open a shell on a running instance |
| `deploy` | deploy, follow a build, read its log, list what shipped |
| `env`, `env group` | variables on one application or shared across several, plus `pull`, `push` and `run` |
| `port` | which ports an application serves, and which of them are public |
| `build` | build method, directory, start command, path filters |
| `domain` | custom domains, the DNS record each one needs, which port it points at |
| `volume` | persistent disks, and mounting them |
| `db` | managed PostgreSQL, its roles, its databases, its connection strings |
| `ip-profile` | who can reach an application, by address |
| `registry` | credentials for pulling private images |
| `logs`, `requests`, `metrics` | what an application is printing, serving and using |
| `login`, `logout`, `team`, `link`, `unlink` | which credential, which team, which application a command acts on |
| `status`, `whoami`, `repos` | what is in effect right now, and where it came from |
| `skills` | install the agent skill into your coding tools, and keep it current |
| `update` | replace this binary with the newest release |
| `api` | any endpoint the CLI has no command for yet |

## For AI agents and CI

The whole command surface is machine-readable, with no authentication, no
configuration file and no network call:

```bash
outplane schema                    # every command, flag, output field, error code
outplane schema deploy create      # just that subtree
outplane app list --json --fields name,status
```

- **Output** is human-formatted on a terminal and JSON when piped. Streams are
  NDJSON. Data goes to stdout, everything else to stderr.
- **Exit codes** are a stable, append-only contract: `outplane help exit-codes`.
- **Errors** arrive as an object carrying a `kind`, a stable `code`, a `hint`
  and runnable `next_steps`.
- **Automation notes** on every command say what it does *not* do, which is
  usually the part that matters: a queued build is not a shipped deploy.
- **Destructive commands never prompt.** They exit 4 and return the exact
  command that would proceed, so the approval gate lives in your harness rather
  than in a terminal an agent does not have. Under an agent harness they refuse
  outright, whatever flags they are given.
- **`--dry-run`** on every mutating command prints what would be sent, without
  sending it.

## Packages

You install one. The rest are how the binary reaches your platform.

| Package | Contents |
| --- | --- |
| [`outplane`](https://www.npmjs.com/package/outplane) | what you install; finds and runs the binary below |
| `@outplane/cli-darwin-arm64` | macOS, Apple silicon |
| `@outplane/cli-darwin-amd64` | macOS, Intel |
| `@outplane/cli-linux-arm64` | Linux, arm64 |
| `@outplane/cli-linux-amd64` | Linux, x86-64 |
| `@outplane/cli-windows-arm64` | Windows, arm64 |
| `@outplane/cli-windows-amd64` | Windows, x86-64 |

They are optional dependencies with `os` and `cpu` set, so npm downloads the one
that matches and none of the others. Nothing is fetched after install: whatever
npm verified is what runs.

Every release publishes all seven at the same version, alongside the archives
and checksums on the [releases page](https://github.com/outplane/cli/releases).

## For coding agents

There is a skill that teaches an agent this CLI: which command answers which
question, what needs a deployment and what does not, and why a destructive
command hands back an invocation instead of asking.

```bash
outplane skills install
```

It is installed by the shell installer already, and it is open source at
[outplane/skills](https://github.com/outplane/skills). It also installs through
a plugin marketplace or `npx skills add outplane/skills`.

## Documentation

<https://docs.outplane.com/cli>

## Contributing

Read [CONTRIBUTING.md](CONTRIBUTING.md) first. It explains the
single-source-of-truth command registry, the layering, and the invariants that
are not negotiable.

## License

[Apache-2.0](LICENSE). Copyright 2026 Out Plane LLC.
