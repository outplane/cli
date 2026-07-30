module github.com/outplane/cli

// Pinned to the toolchain this module is built with. Nothing in the code
// depends on anything newer, so raising it is a one-line change.
go 1.25.0

// Dependencies are declared here as the code that needs them lands.
//
// Nothing external is used yet: the command tree, the schema and the help
// renderer are all standard library. That is not an accident. Every dependency
// is a chance to reintroduce cgo, and the first milestone is more useful if it
// builds from a clean checkout with no network at all.
//
// Planned direct dependencies, and why each one:
//
//   github.com/spf13/cobra          command tree, shell completions
//   github.com/coder/websocket      app shell exec bridge and log streaming
//   github.com/itchyny/gojq         embedded --jq, so users need no jq binary
//   github.com/zalando/go-keyring   OS keychain. Pure Go: keeps CGO_ENABLED=0
//   golang.org/x/term               raw mode and terminal size for `shell`
//   github.com/pkg/browser          opens the console for browser login
//   github.com/knadh/koanf/v2       config file + env var layering
//
// HARD RULE: this module must build with CGO_ENABLED=0 on every target. A
// dependency that pulls cgo turns the binary dynamic, which breaks the
// `FROM scratch` and Alpine cases that motivated choosing Go in the first
// place. CI fails the build if `go list -deps` reports any package with
// CgoFiles. Never swap go-keyring for 99designs/keyring or keybase/go-keychain.

require (
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/pkg/browser v0.0.0-20240102092130-5ac0b6a4141c // indirect
	github.com/spf13/cobra v1.10.2 // indirect
	github.com/spf13/pflag v1.0.9 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/term v0.45.0 // indirect
)
