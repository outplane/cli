//go:build linux

package execctx

import "syscall"

// ioctlReadTermios reads terminal settings on Linux.
const ioctlReadTermios = syscall.TCGETS
