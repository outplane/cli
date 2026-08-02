module github.com/outplane/cli

// Pinned to the toolchain this module is built with. Nothing in the code
// depends on anything newer, so raising it is a one-line change.
go 1.25.0

// Dependencies are declared here as the code that needs them lands.
//
// Each one earns its place by being smaller than what it replaces. Every
// dependency is also a chance to reintroduce cgo, so the rule below is not
// negotiable.
//
// Direct dependencies, and why each one:
//
//   github.com/spf13/cobra          command tree, shell completions
//   github.com/coder/websocket      the app shell bridge. Zero dependencies of
//                                   its own, and a context-aware API, which is
//                                   what makes an interrupted session end
//                                   cleanly. The alternative was hand-writing
//                                   RFC 6455: framing, masking and ping/pong
//                                   are ours to get wrong exactly once
//   golang.org/x/term               raw mode and terminal size for `app shell`
//   github.com/pkg/browser          opens the console for browser login
//
// Planned, as the code that needs them lands:
//
//   github.com/itchyny/gojq         embedded --jq, so users need no jq binary
//   github.com/zalando/go-keyring   OS keychain. Pure Go: keeps CGO_ENABLED=0
//   github.com/knadh/koanf/v2       config file + env var layering
//
// HARD RULE: this module must build with CGO_ENABLED=0 on every target. A
// dependency that pulls cgo turns the binary dynamic, which breaks the
// `FROM scratch` and Alpine cases that motivated choosing Go in the first
// place. CI fails the build if `go list -deps` reports any package with
// CgoFiles. Never swap go-keyring for 99designs/keyring or keybase/go-keychain.

require (
	github.com/coder/websocket v1.8.15
	github.com/pkg/browser v0.0.0-20240102092130-5ac0b6a4141c
	github.com/spf13/cobra v1.10.2
	golang.org/x/term v0.45.0
)

require (
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/spf13/pflag v1.0.9 // indirect
	golang.org/x/sys v0.47.0 // indirect
)
