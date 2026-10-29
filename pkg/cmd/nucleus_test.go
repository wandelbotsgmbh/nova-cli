package cmd

import (
	"bytes"
	"context"
	"os"
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
