//go:build darwin

package execctx

import "syscall"

// ioctlReadTermios reads terminal settings on Darwin and the BSDs.
const ioctlReadTermios = syscall.TIOCGETA
