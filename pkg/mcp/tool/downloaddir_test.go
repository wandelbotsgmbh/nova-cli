//go:build !windows

package tool_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/tool"
)

func TestPrepareDownloadDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "downloads")
	if err := tool.PrepareDownloadDir(dir); err != nil {
		t.Fatalf("new dir: %v", err)
	}
	if info, _ := os.Stat(dir); info.Mode().Perm() != 0o700 {
		t.Errorf("mode %s, want 0700", info.Mode().Perm())
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := tool.PrepareDownloadDir(dir); err != nil {
		t.Errorf("a dir only readable by others is fine (files are 0600): %v", err)
	}
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := tool.PrepareDownloadDir(dir); err == nil || !strings.Contains(err.Error(), "writable by other users") {
		t.Errorf("an open dir must be refused, got %v", err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	if err := tool.PrepareDownloadDir(link); err == nil {
		t.Error("a symlink must be refused")
	}
}
