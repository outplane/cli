# Out Plane CLI

Deploy and operate applications on Out Plane from your terminal.

> **Early.** The command surface below is settled and will not change incompatibly, but not all of it is implemented yet. `outplane schema` always reports what this build actually does.

## Install

macOS and Linux:

```bash
curl -fsSL https://outplane.com/install.sh | sh
```

Windows, macOS and Linux:

```bash
npm i -g outplane
```

Nothing else is needed. The CLI is a single native binary with no runtime
dependency; the npm package is a delivery mechanism for that binary, not a
JavaScript application.

## Getting started

```bash
outplane login            # paste a token created in the console
outplane link checkout    # remember this directory's team and app
outplane status           # what the next command will act on, and why
outplane app list         # everything in the current team
```

## For AI agents and CI

The whole command surface is machine-readable, with no authentication, no
config file and no network call:

```bash
outplane schema                    # every command, flag, output field, error code
outplane schema deploy create      # just that subtree
outplane deploy create --help --json
```

Output is human-formatted on a terminal and JSON when piped. Streams are NDJSON.
Data goes to stdout, everything else to stderr. Exit codes are a stable,
append-only contract.

Destructive commands never prompt. They exit 4 and return the exact command to
replay, so the approval gate lives in your harness rather than in a terminal the
agent does not have.

In CI, set `OUTPLANE_TOKEN` and skip the login step entirely.

## Documentation

<https://docs.outplane.com/cli>

## Contributing

Read [CONTRIBUTING.md](CONTRIBUTING.md) first. It explains the
single-source-of-truth command registry, the layering, and the invariants that
are not negotiable.

## License

[Apache-2.0](LICENSE). Copyright 2026 Out Plane LLC.
