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
	// files are created 0600; what matters is that nobody else can add or replace
	// entries (planted files, symlinks)
	if info.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("download dir %s is writable by other users (mode %s); run 'chmod 700 %s' or choose another with --download-dir", dir, info.Mode().Perm(), dir)
	}
	return nil
}
