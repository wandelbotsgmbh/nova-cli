package tool

import (
	"fmt"
	"os"
	"path/filepath"
)

// DefaultDownloadDir is a per-user directory for files tools save (diagnosis packages,
// backups): the user cache dir, not the shared temp dir, where another local user could
// prepare the directory or plant symlinks.
func DefaultDownloadDir() (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cache, "nova", "mcp-downloads"), nil
}

// PrepareDownloadDir creates dir (0700) and refuses one that belongs to another user or
// is open to others: tools write backups and logs there, which can contain credentials.
func PrepareDownloadDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("download dir %s is not a directory", dir)
	}
	return checkPrivate(dir, info)
}
