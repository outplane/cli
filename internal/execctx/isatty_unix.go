//go:build !windows

package execctx

import (
	"os"
	"syscall"
	"unsafe"
)

// isTerminal reports whether f is a terminal.
//
// It asks the kernel for the file's terminal settings, which succeeds only for
// a real terminal. Nothing else distinguishes one reliably: a terminal, a
// serial port and /dev/null are all character devices, so the file's mode
// cannot tell them apart, and the difference decides whether this CLI prints a
// table or JSON and whether it believes it is being driven by a person.
//
// The ioctl number differs per platform and is defined beside this file.
func isTerminal(f *os.File) bool {
	var termios [128]byte
	_, _, errno := syscall.Syscall6(
		syscall.SYS_IOCTL,
		f.Fd(),
		uintptr(ioctlReadTermios),
		uintptr(unsafe.Pointer(&termios[0])),
		0, 0, 0,
	)
	return errno == 0
}
