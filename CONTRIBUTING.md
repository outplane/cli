# Working on the Out Plane CLI

This file is for anyone changing the CLI itself, human or agent. It is not
documentation for using it; that lives at
[docs.outplane.com/cli](https://docs.outplane.com/cli).

## What this is

A command line client for the Out Plane platform, written in Go. Its users are
humans at a terminal, CI pipelines, and AI coding agents, in that order of
visibility but not of importance. Every rule below exists because one of those
three needs it.

## The one rule that matters most

**Commands are declared once, in `internal/registry`, as plain struct values.**

From that single declaration come the cobra command tree, the `--help` text, the
`outplane schema` document, shell completions and the documentation pages. There
is no second place to write a command's description, its examples, its error
codes or its output fields.

If you find yourself editing help text in one file and a schema in another,
stop: you are working against the design, and the two will drift apart. That is
not hypothetical. The global flag list was once declared twice, and the copies
diverged until the schema advertised two flags that did not exist and omitted
one that did.

To add a command: copy an existing declaration, change it, register it. That is
the whole procedure. `internal/registry/app.go` holds the reference examples,
`appList` for a read-only command and `appDelete` for a destructive one.

## Layering

```
cmd/outplane        cobra front end. Knows about flags and terminals.
internal/registry   command declarations. The source of truth.
internal/api        the ONLY package that speaks HTTP to the Out Plane API.
internal/core       domain logic. Knows nothing about HTTP or terminals.
internal/output     rendering: tables, JSON, NDJSON.
internal/help       the help renderer.
internal/execctx    ExecutionContext: TTY, CI and agent detection.
internal/config     config file, credential storage, directory link file.
internal/install    how this binary was installed, and what would update it.
```

Dependencies point downward only. `core` must stay testable without a network
and without a terminal, because the same functions will later back an MCP tool
server.

## Hard invariants

**`CGO_ENABLED=0`, always.** Go was chosen because one static binary runs in a
`FROM scratch` image, on Alpine, and as a non-root user, and because CI
containers and agent sandboxes are exactly those environments. A dependency that
pulls in cgo makes the binary dynamic and destroys that property. CI fails the
build if `go list -deps` reports any package with `CgoFiles`.

**stdout carries result data only.** Progress, warnings and human-readable
errors go to stderr. `outplane app list --json | jq` must never break.

**Exit codes are a public contract.** Append only. Never reuse or redefine.

```
0 ok │ 1 general │ 2 usage │ 3 auth │ 4 confirmation required │ 5 not found
6 conflict │ 7 quota │ 8 upstream 5xx │ 9 upgrade required
124 timeout │ 130 interrupted
```

**Command names, flag names, JSON field names and error codes are frozen once
shipped.** Additive changes only. Agents cache patterns and may have learned
commands from training data; renaming a flag breaks callers you cannot see.

**Never guess at an unknown enum value.** If the server returns a status we do
not recognise, report it verbatim and keep waiting; do not decide it means
success or failure. A wrong guess turns a red pipeline green.

**Never accept an input and ignore it.** A flag that does not apply is an error,
not a no-op. A field name that does not exist is an error, not an omission. Both
of those shipped once and both read to the caller as success.

## Writing comments

Comments explain **why**, not what. The code already says what it does.

Write down facts a reader cannot infer, especially absences:

```go
// There is no URL in this response, and none can be derived from it. A public
// address needs the app's port, which is a separate request per app, so
// listing does not fetch one.
```

Comments are for guiding the reader through the code. Keep them to that: they
are not the place for release notes, personal asides, or comparisons with other
products.

## Writing user-facing text

This is not decoration. It is the interface an agent uses.

- Every leaf command needs **at least three runnable examples**. One uses
  `--json` or `-o ndjson`. One is safe to run as-is. Every mutating command has
  one using `--dry-run`.
- Examples are declared in the registry, never written into a help string.
- `AutomationNotes` is required for anything long-running, asynchronous,
  paginated, clamped, or read-modify-write. It says what the command does *not*
  do and which command to run next.
- **Write automation notes as declarative facts, never as instructions aimed at
  the agent.** "Without --wait this returns at Queued" is correct. "You should
  now run..." is not: agent harnesses flag directive phrasing as prompt
  injection.
- Every error message states the fix, not only the problem.
- `Related` names three to six sibling commands, so an agent that lands on the
  wrong one is a single hop from the right one.

## Before opening a pull request

```bash
go build ./... && go vet ./... && gofmt -l .
```

`gofmt -l .` must print nothing. Run the command you changed against a real
team as well: every defect found in this codebase so far was found by running a
command against real data, not by reading it.
