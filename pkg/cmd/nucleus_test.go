package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/wandelbotsgmbh/nova-cli/pkg/omniverse/nucleus"
	"github.com/wandelbotsgmbh/nova-cli/pkg/omniverse/nucleus/fakeserver"
)

// newTestRootCmd creates a fresh root command with the nucleus subcommands
// wired to a fake server. Returns the root command and the fake server.
func newTestRootCmd() (*cobra.Command, *fakeserver.Server) {
	fake := fakeserver.New()

	root := &cobra.Command{Use: "nova"}
	nuc := &cobra.Command{
		Use: "nucleus",
	}
	root.AddCommand(nuc)

	// Shared flags (same as init() in nucleus.go).
	nuc.PersistentFlags().String("nucleus-host", fake.Host(), "")
	nuc.PersistentFlags().String("nucleus-token", "fake-token", "")
	nuc.PersistentFlags().Bool("nucleus-tls", false, "")

	// Register subcommands — reuse the RunE closures from the real commands
	// but on fresh cobra.Command instances so flags don't conflict.
	ls := &cobra.Command{Use: nucleusLsCmd.Use, Args: nucleusLsCmd.Args, RunE: nucleusLsCmd.RunE}
	ls.Flags().BoolP("long", "l", false, "")
	nuc.AddCommand(ls)

	mkdir := &cobra.Command{Use: nucleusMkdirCmd.Use, Args: nucleusMkdirCmd.Args, RunE: nucleusMkdirCmd.RunE}
	nuc.AddCommand(mkdir)

	rm := &cobra.Command{Use: nucleusRmCmd.Use, Args: nucleusRmCmd.Args, RunE: nucleusRmCmd.RunE}
	rm.Flags().BoolP("recursive", "r", false, "")
	nuc.AddCommand(rm)

	stat := &cobra.Command{Use: nucleusStatCmd.Use, Args: nucleusStatCmd.Args, RunE: nucleusStatCmd.RunE}
	nuc.AddCommand(stat)

	mv := &cobra.Command{Use: nucleusMvCmd.Use, Args: nucleusMvCmd.Args, RunE: nucleusMvCmd.RunE}
	nuc.AddCommand(mv)

	cp := &cobra.Command{Use: nucleusCpCmd.Use, Args: nucleusCpCmd.Args, RunE: nucleusCpCmd.RunE}
	cp.Flags().BoolP("parents", "p", false, "")
	cp.Flags().BoolP("progress", "P", false, "")
	cp.Flags().IntP("parallel", "j", 2, "")
	nuc.AddCommand(cp)

	return root, fake
}

// run executes a command string and returns stdout, stderr, and error.
func run(root *cobra.Command, args string) (string, string, error) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.SetArgs(strings.Fields(args))
	err := root.Execute()
	return stdout.String(), stderr.String(), err
}

// dialFakeForTest creates a nucleus client connected to the fake server.
func dialFakeForTest(t *testing.T, fake *fakeserver.Server) *nucleus.Client {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := nucleus.Dial(ctx, nucleus.ClientConfig{
		Host: fake.Host(), APIToken: "fake-token", TLS: false,
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	return client
}

func TestNucleusLs(t *testing.T) {
	root, fake := newTestRootCmd()
	defer fake.Close()

	fake.SetEntries("/", []nucleus.Entry{
		{Path: "/Projects", PathType: "folder"},
		{Path: "/Users", PathType: "folder"},
	})

	out, _, err := run(root, "nucleus ls /")
	if err != nil {
		t.Fatalf("ls /: %v", err)
	}
	if !strings.Contains(out, "/Projects/") {
		t.Errorf("expected /Projects/ in output, got: %s", out)
	}
	if !strings.Contains(out, "/Users/") {
		t.Errorf("expected /Users/ in output, got: %s", out)
	}
}

func TestNucleusLsNonExistent(t *testing.T) {
	root, fake := newTestRootCmd()
	defer fake.Close()

	_, _, err := run(root, "nucleus ls /Nonexistent/")
	if err == nil {
		t.Fatal("expected error for non-existent path")
	}
}

func TestNucleusMkdir(t *testing.T) {
	root, fake := newTestRootCmd()
	defer fake.Close()

	// Need root entries so mkdir can add to parent.
	fake.SetEntries("/", []nucleus.Entry{})
	fake.SetEntries("/Projects/", []nucleus.Entry{})

	_, _, err := run(root, "nucleus mkdir /Projects/NewFolder")
	if err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Verify the directory was registered in the fake server.
	_, _ = fake.GetFile("/Projects/NewFolder/")
}

func TestNucleusRmFile(t *testing.T) {
	root, fake := newTestRootCmd()
	defer fake.Close()

	fake.SetEntries("/Projects/", []nucleus.Entry{
		{Path: "/Projects/file.txt", PathType: "file"},
	})
	fake.SetFile("/Projects/file.txt", []byte("content"))

	_, _, err := run(root, "nucleus rm /Projects/file.txt")
	if err != nil {
		t.Fatalf("rm: %v", err)
	}
}

func TestNucleusRmNonEmptyWithoutFlag(t *testing.T) {
	root, fake := newTestRootCmd()
	defer fake.Close()

	fake.SetEntries("/Projects/dir/", []nucleus.Entry{
		{Path: "/Projects/dir/file.txt", PathType: "file"},
	})

	_, _, err := run(root, "nucleus rm /Projects/dir/")
	if err == nil {
		t.Fatal("expected error for non-empty directory without -r")
	}
	if !strings.Contains(err.Error(), "-r") {
		t.Errorf("expected hint about -r flag, got: %v", err)
	}
}

func TestNucleusRmRecursive(t *testing.T) {
	root, fake := newTestRootCmd()
	defer fake.Close()

	fake.SetEntries("/Projects/", []nucleus.Entry{
		{Path: "/Projects/dir", PathType: "folder"},
	})
	fake.SetEntries("/Projects/dir/", []nucleus.Entry{
		{Path: "/Projects/dir/file.txt", PathType: "file"},
	})
	fake.SetFile("/Projects/dir/file.txt", []byte("content"))

	_, _, err := run(root, "nucleus rm -r /Projects/dir/")
	if err != nil {
		t.Fatalf("rm -r: %v", err)
	}
}

func TestNucleusStat(t *testing.T) {
	root, fake := newTestRootCmd()
	defer fake.Close()

	fake.SetEntries("/Projects/", []nucleus.Entry{
		{Path: "/Projects/file.usd", PathType: "file", Size: 4096, CreatedBy: "admin"},
	})

	out, _, err := run(root, "nucleus stat /Projects/file.usd")
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if !strings.Contains(out, "file") {
		t.Errorf("expected 'file' type in output, got: %s", out)
	}
	if !strings.Contains(out, "4096") {
		t.Errorf("expected size 4096 in output, got: %s", out)
	}
}

func TestNucleusStatRoot(t *testing.T) {
	root, fake := newTestRootCmd()
	defer fake.Close()

	out, _, err := run(root, "nucleus stat /")
	if err != nil {
		t.Fatalf("stat /: %v", err)
	}
	if !strings.Contains(out, "folder") {
		t.Errorf("expected 'folder' type in output, got: %s", out)
	}
}

func TestNucleusCpUploadSmallFile(t *testing.T) {
	root, fake := newTestRootCmd()
	defer fake.Close()

	fake.SetEntries("/Projects/", []nucleus.Entry{})

	// Create a temp file to upload.
	tmpFile := t.TempDir() + "/test.txt"
	if err := writeTestFile(tmpFile, "hello nucleus"); err != nil {
		t.Fatal(err)
	}

	_, _, err := run(root, "nucleus cp "+tmpFile+" n:/Projects/test.txt")
	if err != nil {
		t.Fatalf("cp upload: %v", err)
	}

	// Verify the file was stored in the fake server.
	data, ok := fake.GetFile("/Projects/test.txt")
	if !ok {
		t.Fatal("file not stored in fake server")
	}
	if string(data) != "hello nucleus" {
		t.Errorf("file content = %q, want %q", string(data), "hello nucleus")
	}
}

func TestNucleusCpDownload(t *testing.T) {
	root, fake := newTestRootCmd()
	defer fake.Close()

	fake.SetFile("/Projects/readme.md", []byte("# Hello"))
	fake.SetEntries("/Projects/", []nucleus.Entry{
		{Path: "/Projects/readme.md", PathType: "file"},
	})

	dst := t.TempDir() + "/downloaded.md"
	_, _, err := run(root, "nucleus cp n:/Projects/readme.md "+dst)
	if err != nil {
		t.Fatalf("cp download: %v", err)
	}

	data, err := readTestFile(dst)
	if err != nil {
		t.Fatalf("reading downloaded file: %v", err)
	}
	if data != "# Hello" {
		t.Errorf("file content = %q, want %q", data, "# Hello")
	}
}

func TestNucleusCpUploadMissingParent(t *testing.T) {
	root, fake := newTestRootCmd()
	defer fake.Close()

	tmpFile := t.TempDir() + "/test.txt"
	if err := writeTestFile(tmpFile, "data"); err != nil {
		t.Fatal(err)
	}

	_, _, err := run(root, "nucleus cp "+tmpFile+" n:/Projects/nonexistent/test.txt")
	if err == nil {
		t.Fatal("expected error for missing parent directory")
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("expected 'does not exist' in error, got: %v", err)
	}
}

func TestNucleusCpUploadWithParentsFlag(t *testing.T) {
	root, fake := newTestRootCmd()
	defer fake.Close()

	fake.SetEntries("/", []nucleus.Entry{})
	fake.SetEntries("/Projects/", []nucleus.Entry{})

	tmpFile := t.TempDir() + "/test.txt"
	if err := writeTestFile(tmpFile, "data"); err != nil {
		t.Fatal(err)
	}

	_, _, err := run(root, "nucleus cp -p "+tmpFile+" n:/Projects/new/dir/test.txt")
	if err != nil {
		t.Fatalf("cp -p: %v", err)
	}
}

func TestNucleusCpNucleusToNucleus(t *testing.T) {
	root, fake := newTestRootCmd()
	defer fake.Close()

	fake.SetEntries("/Projects/", []nucleus.Entry{
		{Path: "/Projects/a.txt", PathType: "file"},
	})
	fake.SetFile("/Projects/a.txt", []byte("content-a"))

	_, _, err := run(root, "nucleus cp n:/Projects/a.txt n:/Projects/b.txt")
	if err != nil {
		t.Fatalf("cp nucleus->nucleus: %v", err)
	}

	data, ok := fake.GetFile("/Projects/b.txt")
	if !ok {
		t.Fatal("copied file not in fake server")
	}
	if string(data) != "content-a" {
		t.Errorf("copied content = %q, want %q", string(data), "content-a")
	}
}

func TestNucleusMv(t *testing.T) {
	root, fake := newTestRootCmd()
	defer fake.Close()

	_, _, err := run(root, "nucleus mv n:/Projects/old.txt n:/Projects/new.txt")
	if err != nil {
		t.Fatalf("mv: %v", err)
	}
}

func TestNucleusCpRequiresNucleusPrefix(t *testing.T) {
	root, fake := newTestRootCmd()
	defer fake.Close()

	_, _, err := run(root, "nucleus cp /local/a /local/b")
	if err == nil {
		t.Fatal("expected error when neither path has n: prefix")
	}
	if !strings.Contains(err.Error(), "n:") {
		t.Errorf("expected hint about n: prefix, got: %v", err)
	}
}

func TestNucleusCpUploadDirectory(t *testing.T) {
	root, fake := newTestRootCmd()
	defer fake.Close()

	fake.SetEntries("/", []nucleus.Entry{})
	fake.SetEntries("/Projects/", []nucleus.Entry{})

	// Create a local directory tree.
	tmpDir := t.TempDir()
	srcDir := tmpDir + "/mydir"
	if err := os.MkdirAll(srcDir+"/sub", 0755); err != nil {
		t.Fatal(err)
	}
	if err := writeTestFile(srcDir+"/a.txt", "file-a"); err != nil {
		t.Fatal(err)
	}
	if err := writeTestFile(srcDir+"/sub/b.txt", "file-b"); err != nil {
		t.Fatal(err)
	}

	_, _, err := run(root, "nucleus cp "+srcDir+" n:/Projects/mydir")
	if err != nil {
		t.Fatalf("cp upload dir: %v", err)
	}

	// Verify files were stored.
	data, ok := fake.GetFile("/Projects/mydir/a.txt")
	if !ok {
		t.Fatal("a.txt not uploaded")
	}
	if string(data) != "file-a" {
		t.Errorf("a.txt = %q, want %q", string(data), "file-a")
	}

	data, ok = fake.GetFile("/Projects/mydir/sub/b.txt")
	if !ok {
		t.Fatal("sub/b.txt not uploaded")
	}
	if string(data) != "file-b" {
		t.Errorf("sub/b.txt = %q, want %q", string(data), "file-b")
	}
}

func TestNucleusCpDownloadDirectory(t *testing.T) {
	root, fake := newTestRootCmd()
	defer fake.Close()

	fake.SetEntries("/Projects/mydir/", []nucleus.Entry{
		{Path: "/Projects/mydir/a.txt", PathType: "file"},
		{Path: "/Projects/mydir/sub", PathType: "folder"},
	})
	fake.SetEntries("/Projects/mydir/sub/", []nucleus.Entry{
		{Path: "/Projects/mydir/sub/b.txt", PathType: "file"},
	})
	fake.SetFile("/Projects/mydir/a.txt", []byte("file-a"))
	fake.SetFile("/Projects/mydir/sub/b.txt", []byte("file-b"))

	dstDir := t.TempDir() + "/downloaded"
	_, _, err := run(root, "nucleus cp n:/Projects/mydir/ "+dstDir)
	if err != nil {
		t.Fatalf("cp download dir: %v", err)
	}

	data, err := readTestFile(dstDir + "/a.txt")
	if err != nil {
		t.Fatalf("reading a.txt: %v", err)
	}
	if data != "file-a" {
		t.Errorf("a.txt = %q, want %q", data, "file-a")
	}

	data, err = readTestFile(dstDir + "/sub/b.txt")
	if err != nil {
		t.Fatalf("reading sub/b.txt: %v", err)
	}
	if data != "file-b" {
		t.Errorf("sub/b.txt = %q, want %q", data, "file-b")
	}
}

// A recursive download must never write the ingress error page (served with
// HTTP 200 when LFT is overloaded) under the target file name — issue #41.
// Transient failures are retried; here the first LFT fetch of b.txt fails.
func TestNucleusCpDownloadDirectoryRetriesErrorPage(t *testing.T) {
	root, fake := newTestRootCmd()
	defer fake.Close()
	fake.UseLFT(true)

	fake.SetEntries("/Projects/mydir/", []nucleus.Entry{
		{Path: "/Projects/mydir/a.txt", PathType: "file"},
		{Path: "/Projects/mydir/b.txt", PathType: "file"},
	})
	fake.SetFile("/Projects/mydir/a.txt", []byte("file-a"))
	fake.SetFile("/Projects/mydir/b.txt", []byte("file-b"))
	fake.FailLFT("/Projects/mydir/b.txt", 1)

	dstDir := t.TempDir() + "/downloaded"
	_, _, err := run(root, "nucleus cp n:/Projects/mydir/ "+dstDir)
	if err != nil {
		t.Fatalf("cp download dir: %v", err)
	}
	for name, want := range map[string]string{"a.txt": "file-a", "b.txt": "file-b"} {
		data, err := readTestFile(dstDir + "/" + name)
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		if data != want {
			t.Errorf("%s = %q, want %q", name, data, want)
		}
	}
	if n := fake.LFTRequests("/Projects/mydir/b.txt"); n != 2 {
		t.Errorf("LFT requests for b.txt = %d, want 2", n)
	}
}

// When a file keeps failing, the rest of the tree is still downloaded, the
// bad file is not written at all, and the command fails (non-zero exit).
func TestNucleusCpDownloadDirectorySkipsFailedFile(t *testing.T) {
	fake := fakeserver.New()
	defer fake.Close()
	fake.UseLFT(true)

	fake.SetEntries("/Projects/mydir/", []nucleus.Entry{
		{Path: "/Projects/mydir/a.txt", PathType: "file"},
		{Path: "/Projects/mydir/bad.png", PathType: "file"},
		{Path: "/Projects/mydir/sub", PathType: "folder"},
	})
	fake.SetEntries("/Projects/mydir/sub/", []nucleus.Entry{
		{Path: "/Projects/mydir/sub/c.txt", PathType: "file"},
	})
	fake.SetFile("/Projects/mydir/a.txt", []byte("file-a"))
	fake.SetFile("/Projects/mydir/bad.png", []byte("real-png"))
	fake.SetFile("/Projects/mydir/sub/c.txt", []byte("file-c"))
	fake.FailLFT("/Projects/mydir/bad.png", 1000)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, err := nucleus.Dial(ctx, nucleus.ClientConfig{
		Host: fake.Host(), APIToken: "fake-token", TLS: false,
		ReadMaxAttempts: 3, ReadRetryBackoff: 5 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer client.Close()

	dstDir := t.TempDir() + "/downloaded"
	var stderr bytes.Buffer
	_, err = downloadRecursive(ctx, client, "/Projects/mydir/", dstDir, &stderr, 2)
	if err == nil {
		t.Fatal("expected error when a file cannot be downloaded")
	}
	if !strings.Contains(err.Error(), "1 file(s) failed") || !strings.Contains(err.Error(), "/Projects/mydir/bad.png") {
		t.Errorf("error should summarize the skipped file, got: %v", err)
	}
	if !strings.Contains(stderr.String(), "skipping /Projects/mydir/bad.png") {
		t.Errorf("stderr should warn about the skipped file, got: %q", stderr.String())
	}

	if _, statErr := os.Stat(dstDir + "/bad.png"); !os.IsNotExist(statErr) {
		data, _ := readTestFile(dstDir + "/bad.png")
		t.Errorf("bad.png must not be written, found content %q (stat err: %v)", data, statErr)
	}
	if _, statErr := os.Stat(dstDir + "/bad.png.part"); !os.IsNotExist(statErr) {
		t.Errorf("bad.png.part must be cleaned up (stat err: %v)", statErr)
	}
	for name, want := range map[string]string{"a.txt": "file-a", "sub/c.txt": "file-c"} {
		data, err := readTestFile(dstDir + "/" + name)
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		if data != want {
			t.Errorf("%s = %q, want %q", name, data, want)
		}
	}
}

// A single-file download that keeps failing must not be misreported as a
// missing directory, and must not leave a file behind.
func TestNucleusCpDownloadSingleFileFails(t *testing.T) {
	fake := fakeserver.New()
	defer fake.Close()
	fake.UseLFT(true)
	fake.SetFile("/Projects/bad.png", []byte("real-png"))
	fake.FailLFT("/Projects/bad.png", 1000)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, err := nucleus.Dial(ctx, nucleus.ClientConfig{
		Host: fake.Host(), APIToken: "fake-token", TLS: false,
		ReadMaxAttempts: 2, ReadRetryBackoff: 5 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer client.Close()

	dst := t.TempDir() + "/bad.png"
	_, err = downloadRecursive(ctx, client, "/Projects/bad.png", dst, io.Discard, 1)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "Error 503") {
		t.Errorf("error should name the server error page, got: %v", err)
	}
	if _, statErr := os.Stat(dst); !os.IsNotExist(statErr) {
		t.Errorf("bad.png must not be written (stat err: %v)", statErr)
	}
	if _, statErr := os.Stat(dst + ".part"); !os.IsNotExist(statErr) {
		t.Errorf("bad.png.part must be cleaned up (stat err: %v)", statErr)
	}
}

// A recursive download keeps going when the server drops the API WebSocket:
// the client reconnects and the remaining files are still fetched.
func TestNucleusCpDownloadDirectorySurvivesConnectionDrop(t *testing.T) {
	fake := fakeserver.New()
	defer fake.Close()
	fake.UseLFT(true)

	fake.SetEntries("/Projects/mydir/", []nucleus.Entry{
		{Path: "/Projects/mydir/a.txt", PathType: "file"},
		{Path: "/Projects/mydir/sub", PathType: "folder"},
	})
	fake.SetEntries("/Projects/mydir/sub/", []nucleus.Entry{
		{Path: "/Projects/mydir/sub/b.txt", PathType: "file"},
	})
	fake.SetFile("/Projects/mydir/a.txt", []byte("file-a"))
	fake.SetFile("/Projects/mydir/sub/b.txt", []byte("file-b"))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, err := nucleus.Dial(ctx, nucleus.ClientConfig{
		Host: fake.Host(), APIToken: "fake-token", TLS: false,
		ReadMaxAttempts: 3, ReadRetryBackoff: 5 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer client.Close()

	// Drop the connection before the download starts: the first List and
	// every following operation must go through a fresh connection.
	fake.DropConnections()
	deadline := time.Now().Add(5 * time.Second)
	for !client.Conn().Closed() {
		if time.Now().After(deadline) {
			t.Fatal("connection never reported closed")
		}
		time.Sleep(5 * time.Millisecond)
	}

	dstDir := t.TempDir() + "/downloaded"
	var stderr bytes.Buffer
	if _, err := downloadRecursive(ctx, client, "/Projects/mydir/", dstDir, &stderr, 3); err != nil {
		t.Fatalf("downloadRecursive: %v (stderr: %s)", err, stderr.String())
	}
	for name, want := range map[string]string{"a.txt": "file-a", "sub/b.txt": "file-b"} {
		data, err := readTestFile(dstDir + "/" + name)
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		if data != want {
			t.Errorf("%s = %q, want %q", name, data, want)
		}
	}
	if n := client.Reconnects(); n != 1 {
		t.Errorf("Reconnects() = %d, want 1", n)
	}
	if stderr.Len() != 0 {
		t.Errorf("unexpected warnings: %s", stderr.String())
	}
}

// Parallel download of a wide tree: every file lands with the right content,
// the -j flag is honoured end to end, and transient error pages on some files
// are retried without disturbing the others.
func TestNucleusCpDownloadDirectoryParallel(t *testing.T) {
	root, fake := newTestRootCmd()
	defer fake.Close()
	fake.UseLFT(true)

	const n = 40
	var entries []nucleus.Entry
	want := map[string]string{}
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("tex_%02d.png", i)
		remote := "/Projects/big/" + name
		content := strings.Repeat(fmt.Sprintf("%02d", i), 50+i) // varied sizes, multi-block with bsize 4
		entries = append(entries, nucleus.Entry{Path: remote, PathType: "file"})
		fake.SetFile(remote, []byte(content))
		want[name] = content
		if i%7 == 0 {
			fake.FailLFT(remote, 1)
		}
	}
	entries = append(entries, nucleus.Entry{Path: "/Projects/big/sub", PathType: "folder"})
	fake.SetEntries("/Projects/big/", entries)
	fake.SetEntries("/Projects/big/sub/", []nucleus.Entry{{Path: "/Projects/big/sub/deep.usd", PathType: "file"}})
	fake.SetFile("/Projects/big/sub/deep.usd", []byte("deep-content"))
	want["sub/deep.usd"] = "deep-content"

	dstDir := t.TempDir() + "/downloaded"
	_, stderr, err := run(root, "nucleus cp -j 8 n:/Projects/big/ "+dstDir)
	if err != nil {
		t.Fatalf("cp -j 8: %v (stderr: %s)", err, stderr)
	}
	for name, content := range want {
		data, err := readTestFile(dstDir + "/" + name)
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		if data != content {
			t.Errorf("%s has wrong content (len %d, want %d)", name, len(data), len(content))
		}
	}
	parts, _ := filepath.Glob(dstDir + "/*.part")
	if len(parts) != 0 {
		t.Errorf("leftover .part files: %v", parts)
	}
	if got := fake.LFTRequests("/Projects/big/tex_07.png"); got != 2 {
		t.Errorf("tex_07.png LFT requests = %d, want 2 (error page + retry)", got)
	}
}

// Cancelling the context stops the workers promptly and surfaces the
// cancellation instead of a pile of per-file warnings.
func TestNucleusCpDownloadDirectoryCancel(t *testing.T) {
	fake := fakeserver.New()
	defer fake.Close()
	fake.UseLFT(true)

	var entries []nucleus.Entry
	for i := 0; i < 30; i++ {
		remote := fmt.Sprintf("/Projects/big/f%02d", i)
		entries = append(entries, nucleus.Entry{Path: remote, PathType: "file"})
		fake.SetFile(remote, []byte("x"))
		fake.FailLFT(remote, 1000) // every fetch retries -> plenty of time to cancel
	}
	fake.SetEntries("/Projects/big/", entries)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, err := nucleus.Dial(ctx, nucleus.ClientConfig{
		Host: fake.Host(), APIToken: "fake-token", TLS: false,
		ReadMaxAttempts: 50, ReadRetryBackoff: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer client.Close()

	dlCtx, dlCancel := context.WithCancel(ctx)
	go func() {
		time.Sleep(100 * time.Millisecond)
		dlCancel()
	}()
	var stderr bytes.Buffer
	start := time.Now()
	_, err = downloadRecursive(dlCtx, client, "/Projects/big/", t.TempDir()+"/dl", &stderr, 4)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Errorf("download did not stop promptly after cancel")
	}
	if stderr.Len() != 0 {
		t.Errorf("cancellation must not be reported as skipped files: %s", stderr.String())
	}
}

// --- helpers ---

func writeTestFile(path, content string) error {
	return writeFile(path, []byte(content))
}

func readTestFile(path string) (string, error) {
	data, err := readFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
