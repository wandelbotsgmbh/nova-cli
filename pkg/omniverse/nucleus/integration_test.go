package nucleus_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/wandelbotsgmbh/nova-cli/pkg/omniverse/nucleus"
)

const integrationHost = "nucleus.dev.wandelbots.io"

// dialIntegration connects to the real Nucleus server.
// Skips the test if NUCLEUS_TOKEN is not set.
func dialIntegration(t *testing.T) *nucleus.Client {
	t.Helper()
	token := os.Getenv("NUCLEUS_TOKEN")
	if token == "" {
		t.Skip("NUCLEUS_TOKEN not set — skipping integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	client, err := nucleus.Dial(ctx, nucleus.ClientConfig{
		Host:     integrationHost,
		APIToken: token,
		TLS:      true,
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	return client
}

// testDir returns a unique test directory path for this test run.
func testDir(t *testing.T) string {
	t.Helper()
	// Use a sanitised test name to avoid conflicts between parallel runs.
	name := strings.ReplaceAll(t.Name(), "/", "_")
	return "/Projects/_nova_cli_test_" + name + "/"
}

func TestIntegrationDial(t *testing.T) {
	client := dialIntegration(t)
	defer client.Close()

	info := client.Info()
	if info.Status != "OK" {
		t.Errorf("status = %q, want OK", info.Status)
	}
	if info.Username == "" {
		t.Error("username is empty")
	}
	t.Logf("Connected as %s (server %s)", info.Username, info.Version)
}

func TestIntegrationListRoot(t *testing.T) {
	client := dialIntegration(t)
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	entries, err := client.List(ctx, "/")
	if err != nil {
		t.Fatalf("List /: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("root has no entries")
	}

	// Root should always have /Projects and /Users.
	var hasProjects, hasUsers bool
	for _, e := range entries {
		t.Logf("  %s (%s)", e.Path, e.PathType)
		if strings.TrimRight(e.Path, "/") == "/Projects" {
			hasProjects = true
		}
		if strings.TrimRight(e.Path, "/") == "/Users" {
			hasUsers = true
		}
	}
	if !hasProjects {
		t.Error("expected /Projects in root listing")
	}
	if !hasUsers {
		t.Error("expected /Users in root listing")
	}
}

func TestIntegrationMkdirAndRm(t *testing.T) {
	client := dialIntegration(t)
	defer client.Close()

	dir := testDir(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Create.
	if err := client.CreateFolder(ctx, dir); err != nil {
		t.Fatalf("CreateFolder %s: %v", dir, err)
	}
	t.Logf("Created %s", dir)

	// Stat.
	entry, err := client.Stat(ctx, dir)
	if err != nil {
		t.Fatalf("Stat %s: %v", dir, err)
	}
	if !entry.IsDir() {
		t.Errorf("expected directory, got %s", entry.PathType)
	}

	// Delete.
	if err := client.Delete(ctx, dir); err != nil {
		t.Fatalf("Delete %s: %v", dir, err)
	}
	t.Logf("Deleted %s", dir)
}

func TestIntegrationWriteReadDelete(t *testing.T) {
	client := dialIntegration(t)
	defer client.Close()

	dir := testDir(t)
	filePath := strings.TrimRight(dir, "/") + "/hello.txt"
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Setup: create directory.
	if err := client.CreateFolder(ctx, dir); err != nil {
		t.Fatalf("CreateFolder: %v", err)
	}
	defer func() {
		_ = client.DeleteRecursive(context.Background(), dir)
	}()

	// Write.
	content := []byte("hello from integration test!")
	if err := client.WriteFile(ctx, filePath, content); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	t.Logf("Wrote %d bytes to %s", len(content), filePath)

	// List.
	entries, err := client.List(ctx, dir)
	if err != nil {
		t.Fatalf("List %s: %v", dir, err)
	}
	found := false
	for _, e := range entries {
		if strings.HasSuffix(e.Path, "/hello.txt") {
			found = true
			if e.Size != int64(len(content)) {
				t.Errorf("size = %d, want %d", e.Size, len(content))
			}
		}
	}
	if !found {
		t.Errorf("hello.txt not found in listing of %s", dir)
	}

	// Read.
	data, err := client.ReadFile(ctx, filePath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != string(content) {
		t.Errorf("content = %q, want %q", string(data), string(content))
	}
	t.Logf("Read back %d bytes", len(data))
}

func TestIntegrationCopyFile(t *testing.T) {
	client := dialIntegration(t)
	defer client.Close()

	dir := testDir(t)
	src := strings.TrimRight(dir, "/") + "/original.txt"
	dst := strings.TrimRight(dir, "/") + "/copied.txt"
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Setup.
	if err := client.CreateFolder(ctx, dir); err != nil {
		t.Fatalf("CreateFolder: %v", err)
	}
	defer func() {
		_ = client.DeleteRecursive(context.Background(), dir)
	}()

	if err := client.WriteFile(ctx, src, []byte("copy me")); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Copy.
	if err := client.Copy(ctx, src, dst); err != nil {
		t.Fatalf("Copy: %v", err)
	}
	t.Logf("Copied %s -> %s", src, dst)

	// Verify.
	data, err := client.ReadFile(ctx, dst)
	if err != nil {
		t.Fatalf("ReadFile copied: %v", err)
	}
	if string(data) != "copy me" {
		t.Errorf("copied content = %q, want %q", string(data), "copy me")
	}
}

func TestIntegrationCopyDirectory(t *testing.T) {
	client := dialIntegration(t)
	defer client.Close()

	dir := testDir(t)
	srcDir := strings.TrimRight(dir, "/") + "/src/"
	dstDir := strings.TrimRight(dir, "/") + "/dst/"
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Setup.
	if err := client.CreateFolder(ctx, dir); err != nil {
		t.Fatalf("CreateFolder: %v", err)
	}
	defer func() {
		_ = client.DeleteRecursive(context.Background(), dir)
	}()

	if err := client.CreateFolder(ctx, srcDir); err != nil {
		t.Fatalf("CreateFolder src: %v", err)
	}
	if err := client.WriteFile(ctx, srcDir+"a.txt", []byte("aaa")); err != nil {
		t.Fatalf("WriteFile a.txt: %v", err)
	}
	if err := client.WriteFile(ctx, srcDir+"b.txt", []byte("bbb")); err != nil {
		t.Fatalf("WriteFile b.txt: %v", err)
	}

	// Copy directory.
	if err := client.Copy(ctx, srcDir, dstDir); err != nil {
		t.Fatalf("Copy dir: %v", err)
	}
	t.Logf("Copied %s -> %s", srcDir, dstDir)

	// Verify.
	entries, err := client.List(ctx, dstDir)
	if err != nil {
		t.Fatalf("List dst: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("got %d entries in dst, want 2", len(entries))
	}

	data, err := client.ReadFile(ctx, dstDir+"a.txt")
	if err != nil {
		t.Fatalf("ReadFile a.txt: %v", err)
	}
	if string(data) != "aaa" {
		t.Errorf("a.txt content = %q, want %q", string(data), "aaa")
	}
}

func TestIntegrationMoveFile(t *testing.T) {
	client := dialIntegration(t)
	defer client.Close()

	dir := testDir(t)
	src := strings.TrimRight(dir, "/") + "/before.txt"
	dst := strings.TrimRight(dir, "/") + "/after.txt"
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Setup.
	if err := client.CreateFolder(ctx, dir); err != nil {
		t.Fatalf("CreateFolder: %v", err)
	}
	defer func() {
		_ = client.DeleteRecursive(context.Background(), dir)
	}()

	if err := client.WriteFile(ctx, src, []byte("move me")); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Move.
	if err := client.Move(ctx, src, dst); err != nil {
		t.Fatalf("Move: %v", err)
	}
	t.Logf("Moved %s -> %s", src, dst)

	// Verify source is gone.
	_, err := client.Stat(ctx, src)
	if err == nil {
		t.Error("source still exists after move")
	}

	// Verify destination exists.
	data, err := client.ReadFile(ctx, dst)
	if err != nil {
		t.Fatalf("ReadFile moved: %v", err)
	}
	if string(data) != "move me" {
		t.Errorf("moved content = %q, want %q", string(data), "move me")
	}
}

func TestIntegrationEnsureParentDirs(t *testing.T) {
	client := dialIntegration(t)
	defer client.Close()

	dir := testDir(t)
	deepFile := strings.TrimRight(dir, "/") + "/a/b/c/file.txt"
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	defer func() {
		_ = client.DeleteRecursive(context.Background(), dir)
	}()

	// Create parent dirs for a deeply nested file.
	if err := client.EnsureParentDirs(ctx, deepFile); err != nil {
		t.Fatalf("EnsureParentDirs: %v", err)
	}

	// Write the file.
	if err := client.WriteFile(ctx, deepFile, []byte("deep")); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Read back.
	data, err := client.ReadFile(ctx, deepFile)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "deep" {
		t.Errorf("content = %q, want %q", string(data), "deep")
	}
	t.Logf("Created and read %s through 3 levels of auto-created dirs", deepFile)
}

func TestIntegrationDeleteRecursive(t *testing.T) {
	client := dialIntegration(t)
	defer client.Close()

	dir := testDir(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Setup: dir with nested content.
	if err := client.EnsureParentDirs(ctx, dir+"sub/file.txt"); err != nil {
		t.Fatalf("EnsureParentDirs: %v", err)
	}
	if err := client.WriteFile(ctx, dir+"top.txt", []byte("top")); err != nil {
		t.Fatalf("WriteFile top: %v", err)
	}
	if err := client.WriteFile(ctx, dir+"sub/file.txt", []byte("nested")); err != nil {
		t.Fatalf("WriteFile nested: %v", err)
	}

	// Non-recursive delete should fail.
	if err := client.Delete(ctx, dir); err == nil {
		t.Fatal("expected FOLDER_NOT_EMPTY error")
	}

	// Recursive delete should succeed.
	if err := client.DeleteRecursive(ctx, dir); err != nil {
		t.Fatalf("DeleteRecursive: %v", err)
	}
	t.Logf("Recursively deleted %s", dir)

	// Verify gone.
	_, err := client.List(ctx, dir)
	if err == nil {
		t.Error("directory still exists after recursive delete")
	}
}
