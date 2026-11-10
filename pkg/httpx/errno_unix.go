//go:build !windows

package httpx

import "syscall"

const (
	errConnRefused = syscall.ECONNREFUSED
	errConnReset   = syscall.ECONNRESET
	errPipe        = syscall.EPIPE
)
