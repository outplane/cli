# Out Plane CLI

Deploy and operate applications on Out Plane from your terminal.

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
outplane login                 # paste a token from the console, or use --browser
outplane link --app checkout   # remember this directory's app
outplane deploy create --wait  # build, deploy, and wait for the result
outplane logs -f               # follow the running application's logs
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

This repository is private. If you are changing the code, read
[AGENTS.md](AGENTS.md) first: it explains the single-source-of-truth command
registry and the invariants that are not negotiable.

## License

Proprietary. See [LICENSE](LICENSE). Binaries are free to download and use;
the source is not public.
