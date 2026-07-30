package execctx

import "runtime"

// isLinux is a named wrapper so the browser-detection logic reads as a
// sentence and so tests can reason about it without build tags.
func isLinux() bool { return runtime.GOOS == "linux" }
