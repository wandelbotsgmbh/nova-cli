package httpx

import "syscall"

// Windows reports Winsock codes; syscall.ECONNREFUSED and friends are made-up values
// there that no network error carries.
const (
	errConnRefused = syscall.Errno(10061) // WSAECONNREFUSED
	errConnReset   = syscall.Errno(10054) // WSAECONNRESET
	errPipe        = syscall.Errno(10053) // WSAECONNABORTED
)
