//go:build windows

package execctx

import (
	"os"
	"syscall"
)

// isTerminal reports whether f is a console.
//
// GetConsoleMode succeeds only for a console handle, which is the same test the
// Unix build makes with an ioctl: ask for something only a terminal has.
func isTerminal(f *os.File) bool {
	var mode uint32
	return syscall.GetConsoleMode(syscall.Handle(f.Fd()), &mode) == nil
}
