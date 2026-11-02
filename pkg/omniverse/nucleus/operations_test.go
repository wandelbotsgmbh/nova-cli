package nucleus_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/wandelbotsgmbh/nova-cli/pkg/omniverse/nucleus"
	"github.com/wandelbotsgmbh/nova-cli/pkg/omniverse/nucleus/connlib"
	"github.com/wandelbotsgmbh/nova-cli/pkg/omniverse/nucleus/fakeserver"
)

func dialFake(t *testing.T, fake *fakeserver.Server) *nucleus.Client {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := nucleus.Dial(ctx, nucleus.ClientConfig{
		Host:     fake.Host(),
		APIToken: "fake-token",
		TLS:      false,
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	return client
}

func TestDial(t *testing.T) {
	fake := fakeserver.New()
	defer fake.Close()

	client := dialFake(t, fake)
	defer client.Close()

	info := client.Info()
	if info.Username != "testuser" {
		t.Errorf("username = %q, want %q", info.Username, "testuser")
	}
	if info.ConnectionID != "conn-test-001" {
		t.Errorf("connection_id = %q, want %q", info.ConnectionID, "conn-test-001")
	}
	if info.Status != "OK" {
		t.Errorf("status = %q, want %q", info.Status, "OK")
	}
}

func TestList(t *testing.T) {
	fake := fakeserver.New()
	defer fake.Close()
	fake.SetEntries("/", []nucleus.Entry{
		{Path: "/Projects", PathType: "folder"},
		{Path: "/Users", PathType: "folder"},
	})
	fake.SetEntries("/Projects/", []nucleus.Entry{
		{Path: "/Projects/Scene.usd", PathType: "file", Size: 1024},
		{Path: "/Projects/Materials", PathType: "folder"},
	})

	client := dialFake(t, fake)
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	t.Run("root", func(t *testing.T) {
		entries, err := client.List(ctx, "/")
		if err != nil {
			t.Fatalf("List /: %v", err)
		}
		if len(entries) != 2 {
			t.Fatalf("got %d entries, want 2", len(entries))
		}
		if entries[0].Path != "/Projects" {
			t.Errorf("entries[0].Path = %q, want %q", entries[0].Path, "/Projects")
		}
		if !entries[0].IsDir() {
			t.Error("entries[0] should be a directory")
		}
	})

	t.Run("subdir", func(t *testing.T) {
		entries, err := client.List(ctx, "/Projects/")
		if err != nil {
			t.Fatalf("List /Projects/: %v", err)
		}
		if len(entries) != 2 {
			t.Fatalf("got %d entries, want 2", len(entries))
		}
		if entries[0].Path != "/Projects/Scene.usd" {
			t.Errorf("entries[0].Path = %q, want %q", entries[0].Path, "/Projects/Scene.usd")
		}
		if entries[0].Size != 1024 {
			t.Errorf("entries[0].Size = %d, want 1024", entries[0].Size)
		}
	})

	t.Run("not_found", func(t *testing.T) {
		_, err := client.List(ctx, "/Nonexistent/")
		if err == nil {
			t.Fatal("expected error for nonexistent path")
		}
	})
}

func TestStat(t *testing.T) {
	fake := fakeserver.New()
	defer fake.Close()
	fake.SetEntries("/", []nucleus.Entry{
		{Path: "/Projects", PathType: "folder"},
	})
	fake.SetEntries("/Projects/", []nucleus.Entry{
		{Path: "/Projects/Scene.usd", PathType: "file", Size: 2048, CreatedBy: "admin"},
	})

	client := dialFake(t, fake)
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	t.Run("root", func(t *testing.T) {
		entry, err := client.Stat(ctx, "/")
		if err != nil {
			t.Fatalf("Stat /: %v", err)
		}
		if entry.PathType != "folder" {
			t.Errorf("type = %q, want folder", entry.PathType)
		}
	})

	t.Run("folder", func(t *testing.T) {
		entry, err := client.Stat(ctx, "/Projects")
		if err != nil {
			t.Fatalf("Stat /Projects: %v", err)
		}
		if entry.PathType != "folder" {
			t.Errorf("type = %q, want folder", entry.PathType)
		}
	})

	t.Run("file", func(t *testing.T) {
		entry, err := client.Stat(ctx, "/Projects/Scene.usd")
		if err != nil {
			t.Fatalf("Stat: %v", err)
		}
		if entry.PathType != "file" {
			t.Errorf("type = %q, want file", entry.PathType)
		}
		if entry.Size != 2048 {
			t.Errorf("size = %d, want 2048", entry.Size)
		}
		if entry.CreatedBy != "admin" {
			t.Errorf("created_by = %q, want admin", entry.CreatedBy)
		}
	})

	t.Run("not_found", func(t *testing.T) {
		_, err := client.Stat(ctx, "/Nonexistent")
		if err == nil {
			t.Fatal("expected error for nonexistent path")
		}
	})
}

func TestCreateFolder(t *testing.T) {
	fake := fakeserver.New()
	defer fake.Close()

	fake.SetEntries("/", []nucleus.Entry{})
	fake.SetEntries("/Projects/", []nucleus.Entry{})

	client := dialFake(t, fake)
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.CreateFolder(ctx, "/Projects/NewFolder/"); err != nil {
		t.Fatalf("CreateFolder: %v", err)
	}
}

func TestDelete(t *testing.T) {
	fake := fakeserver.New()
	defer fake.Close()

	fake.SetEntries("/Projects/", []nucleus.Entry{
		{Path: "/Projects/OldFile.usd", PathType: "file"},
	})
	fake.SetFile("/Projects/OldFile.usd", []byte("data"))

	client := dialFake(t, fake)
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Delete(ctx, "/Projects/OldFile.usd"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
}

func TestCopy(t *testing.T) {
	fake := fakeserver.New()
	defer fake.Close()

	fake.SetEntries("/Projects/", []nucleus.Entry{
		{Path: "/Projects/A.usd", PathType: "file"},
	})
	fake.SetFile("/Projects/A.usd", []byte("asset-data"))

	client := dialFake(t, fake)
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Copy(ctx, "/Projects/A.usd", "/Projects/B.usd"); err != nil {
		t.Fatalf("Copy: %v", err)
	}

	data, ok := fake.GetFile("/Projects/B.usd")
	if !ok {
		t.Fatal("copied file not found")
	}
	if string(data) != "asset-data" {
		t.Errorf("copied content = %q, want %q", string(data), "asset-data")
	}
}

func TestMove(t *testing.T) {
	fake := fakeserver.New()
	defer fake.Close()

	client := dialFake(t, fake)
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Move(ctx, "/Projects/Old.usd", "/Projects/New.usd"); err != nil {
		t.Fatalf("Move: %v", err)
	}
}

func TestWriteFile(t *testing.T) {
	fake := fakeserver.New()
	defer fake.Close()

	fake.SetEntries("/Projects/", []nucleus.Entry{})

	client := dialFake(t, fake)
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	content := []byte("hello nucleus")
	if err := client.WriteFile(ctx, "/Projects/test.txt", content); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	data, ok := fake.GetFile("/Projects/test.txt")
	if !ok {
		t.Fatal("written file not found")
	}
	if string(data) != "hello nucleus" {
		t.Errorf("content = %q, want %q", string(data), "hello nucleus")
	}
}

func TestReadFile(t *testing.T) {
	fake := fakeserver.New()
	defer fake.Close()

	fake.SetFile("/Projects/readme.md", []byte("# Hello World"))

	client := dialFake(t, fake)
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	data, err := client.ReadFile(ctx, "/Projects/readme.md")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "# Hello World" {
		t.Errorf("content = %q, want %q", string(data), "# Hello World")
	}
}

func TestDeleteRecursive(t *testing.T) {
	fake := fakeserver.New()
	defer fake.Close()

	fake.SetEntries("/Projects/", []nucleus.Entry{
		{Path: "/Projects/dir", PathType: "folder"},
	})
	fake.SetEntries("/Projects/dir/", []nucleus.Entry{
		{Path: "/Projects/dir/a.txt", PathType: "file"},
		{Path: "/Projects/dir/b.txt", PathType: "file"},
	})
	fake.SetFile("/Projects/dir/a.txt", []byte("a"))
	fake.SetFile("/Projects/dir/b.txt", []byte("b"))

	client := dialFake(t, fake)
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Without recursive, should fail.
	err := client.Delete(ctx, "/Projects/dir/")
	if err == nil {
		t.Fatal("expected FOLDER_NOT_EMPTY error")
	}

	// With recursive, should succeed.
	if err := client.DeleteRecursive(ctx, "/Projects/dir/"); err != nil {
		t.Fatalf("DeleteRecursive: %v", err)
	}
}

func TestEnsureParentDirs(t *testing.T) {
	fake := fakeserver.New()
	defer fake.Close()

	fake.SetEntries("/", []nucleus.Entry{})
	fake.SetEntries("/Projects/", []nucleus.Entry{})

	client := dialFake(t, fake)
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.EnsureParentDirs(ctx, "/Projects/deep/nested/file.txt"); err != nil {
		t.Fatalf("EnsureParentDirs: %v", err)
	}

	// Should be able to list the created directories.
	entries, err := client.List(ctx, "/Projects/deep/")
	if err != nil {
		t.Fatalf("List created dir: %v", err)
	}
	_ = entries
}

// dialFakeFastRetry dials the fake server with a retry policy that keeps
// tests quick: 3 attempts, 10ms base backoff.
func dialFakeFastRetry(t *testing.T, fake *fakeserver.Server) *nucleus.Client {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := nucleus.Dial(ctx, nucleus.ClientConfig{
		Host:             fake.Host(),
		APIToken:         "fake-token",
		TLS:              false,
		ReadMaxAttempts:  3,
		ReadRetryBackoff: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	return client
}

func TestReadFileLFT(t *testing.T) {
	fake := fakeserver.New()
	defer fake.Close()
	fake.UseLFT(true)
	fake.SetFile("/Projects/tex.png", []byte("binary-image-data"))

	client := dialFakeFastRetry(t, fake)
	defer client.Close()

	ctx := context.Background()
	data, err := client.ReadFile(ctx, "/Projects/tex.png")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "binary-image-data" {
		t.Errorf("content = %q, want %q", data, "binary-image-data")
	}
	if n := fake.LFTRequests("/Projects/tex.png"); n != 1 {
		t.Errorf("LFT requests = %d, want 1", n)
	}
}

// The ingress in front of LFT serves its own error page with HTTP 200 when
// the service is overloaded (issue #41). ReadFile must not return that page
// as file content; it retries with a fresh ticket and succeeds.
func TestReadFileLFTRetriesErrorPage(t *testing.T) {
	fake := fakeserver.New()
	defer fake.Close()
	fake.UseLFT(true)
	fake.SetFile("/Projects/tex.png", []byte("binary-image-data"))
	fake.FailLFT("/Projects/tex.png", 2)

	client := dialFakeFastRetry(t, fake)
	defer client.Close()

	data, err := client.ReadFile(context.Background(), "/Projects/tex.png")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "binary-image-data" {
		t.Errorf("content = %q, want %q", data, "binary-image-data")
	}
	if n := fake.LFTRequests("/Projects/tex.png"); n != 3 {
		t.Errorf("LFT requests = %d, want 3 (2 error pages + 1 success)", n)
	}
}

func TestReadFileLFTGivesUp(t *testing.T) {
	fake := fakeserver.New()
	defer fake.Close()
	fake.UseLFT(true)
	fake.SetFile("/Projects/tex.png", []byte("binary-image-data"))
	fake.FailLFT("/Projects/tex.png", 100)

	client := dialFakeFastRetry(t, fake)
	defer client.Close()

	data, err := client.ReadFile(context.Background(), "/Projects/tex.png")
	if err == nil {
		t.Fatalf("ReadFile succeeded with content %q, want error", data)
	}
	if data != nil {
		t.Errorf("ReadFile returned data %q alongside error", data)
	}
	if !nucleus.IsTransient(err) {
		t.Errorf("error is not transient: %v", err)
	}
	if !strings.Contains(err.Error(), "Error 503: Service Unavailable") {
		t.Errorf("error should name the server's error page, got: %v", err)
	}
	if n := fake.LFTRequests("/Projects/tex.png"); n != 3 {
		t.Errorf("LFT requests = %d, want 3 (max attempts)", n)
	}
}

func TestReadFileLFTNotFoundIsNotRetried(t *testing.T) {
	fake := fakeserver.New()
	defer fake.Close()
	fake.UseLFT(true)
	fake.SetFile("/Projects/tex.png", []byte("x"))

	client := dialFakeFastRetry(t, fake)
	defer client.Close()

	// Server-side "not a file" errors come back immediately.
	_, err := client.ReadFile(context.Background(), "/Projects/missing.png")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if nucleus.IsTransient(err) {
		t.Errorf("INVALID_URI must not be transient: %v", err)
	}
}

func TestReadFileRetryHonoursContext(t *testing.T) {
	fake := fakeserver.New()
	defer fake.Close()
	fake.UseLFT(true)
	fake.SetFile("/Projects/tex.png", []byte("x"))
	fake.FailLFT("/Projects/tex.png", 100)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := nucleus.Dial(ctx, nucleus.ClientConfig{
		Host: fake.Host(), APIToken: "fake-token", TLS: false,
		ReadMaxAttempts: 10, ReadRetryBackoff: time.Hour,
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer client.Close()

	readCtx, readCancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer readCancel()
	start := time.Now()
	_, err = client.ReadFile(readCtx, "/Projects/tex.png")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want context deadline exceeded", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Errorf("ReadFile did not stop waiting on context cancel")
	}
}

// The server (or a proxy) can drop the API WebSocket mid-session — seen
// after ~30 minutes of sustained downloads. Read-only operations must
// re-dial and carry on instead of failing every remaining file.
func TestReadFileReconnectsAfterConnectionDrop(t *testing.T) {
	fake := fakeserver.New()
	defer fake.Close()
	fake.UseLFT(true)
	fake.SetFile("/Projects/tex.png", []byte("binary-image-data"))

	client := dialFakeFastRetry(t, fake)
	defer client.Close()

	ctx := context.Background()
	if _, err := client.ReadFile(ctx, "/Projects/tex.png"); err != nil {
		t.Fatalf("first ReadFile: %v", err)
	}

	fake.DropConnections()
	waitForClosed(t, client)

	data, err := client.ReadFile(ctx, "/Projects/tex.png")
	if err != nil {
		t.Fatalf("ReadFile after drop: %v", err)
	}
	if string(data) != "binary-image-data" {
		t.Errorf("content = %q, want %q", data, "binary-image-data")
	}
	if n := client.Reconnects(); n != 1 {
		t.Errorf("Reconnects() = %d, want 1", n)
	}
	if n := fake.APIConnections(); n != 2 {
		t.Errorf("server accepted %d API connections, want 2", n)
	}
	if client.Info().Username != "testuser" {
		t.Errorf("connection info not refreshed after reconnect: %+v", client.Info())
	}
}

func TestListReconnectsAfterConnectionDrop(t *testing.T) {
	fake := fakeserver.New()
	defer fake.Close()
	fake.SetEntries("/Projects/", []nucleus.Entry{
		{Path: "/Projects/a.usd", PathType: "file"},
	})

	client := dialFakeFastRetry(t, fake)
	defer client.Close()

	fake.DropConnections()
	waitForClosed(t, client)

	entries, err := client.List(context.Background(), "/Projects/")
	if err != nil {
		t.Fatalf("List after drop: %v", err)
	}
	if len(entries) != 1 || entries[0].Path != "/Projects/a.usd" {
		t.Errorf("entries = %+v", entries)
	}
	if n := client.Reconnects(); n != 1 {
		t.Errorf("Reconnects() = %d, want 1", n)
	}
}

// Mutating operations are not replayed automatically; they surface the
// dropped connection so the caller can decide.
func TestCreateFolderReportsClosedConnection(t *testing.T) {
	fake := fakeserver.New()
	defer fake.Close()

	client := dialFakeFastRetry(t, fake)
	defer client.Close()

	fake.DropConnections()
	waitForClosed(t, client)

	err := client.CreateFolder(context.Background(), "/Projects/new/")
	if !errors.Is(err, connlib.ErrClosed) {
		t.Errorf("err = %v, want connlib.ErrClosed", err)
	}
	if n := client.Reconnects(); n != 0 {
		t.Errorf("Reconnects() = %d, want 0 for a mutating op", n)
	}
}

// waitForClosed blocks until the client's read loop has noticed the drop.
func waitForClosed(t *testing.T, client *nucleus.Client) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !client.Conn().Closed() {
		if time.Now().After(deadline) {
			t.Fatal("connection never reported closed")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
