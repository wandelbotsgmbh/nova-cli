//go:build windows

package tool

import "os"

// checkPrivate: the user cache dir on Windows is per user by ACL.
func checkPrivate(string, os.FileInfo) error { return nil }
