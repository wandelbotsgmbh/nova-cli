//go:build !windows

package tool

import (
	"fmt"
	"os"
	"syscall"
)

func checkPrivate(dir string, info os.FileInfo) error {
	if st, ok := info.Sys().(*syscall.Stat_t); ok && int(st.Uid) != os.Getuid() {
		return fmt.Errorf("download dir %s belongs to another user; choose another with --download-dir", dir)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("download dir %s is accessible by other users (mode %s); run 'chmod 700 %s' or choose another with --download-dir", dir, info.Mode().Perm(), dir)
	}
	return nil
}
