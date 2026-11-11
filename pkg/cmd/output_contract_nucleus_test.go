package cmd

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/wandelbotsgmbh/nova-cli/pkg/nucleusservers"
	"github.com/wandelbotsgmbh/nova-cli/pkg/omniverse/nucleus"
	"github.com/wandelbotsgmbh/nova-cli/pkg/omniverse/nucleus/fakeserver"
)

// withFakeNucleus selects a Nucleus server that points to a fake server.
func withFakeNucleus(t *testing.T) *fakeserver.Server {
	t.Helper()
	fake := fakeserver.New()
	t.Cleanup(fake.Close)
	withConfig(t, `current-nucleus: fake
nucleus-servers:
  - name: fake
    host: `+fake.Host()+`
    token: fake-secret-token
    tls: false
`)
	t.Setenv(nucleusservers.EnvVar, "")
	t.Setenv("NUCLEUS_TOKEN", "")
	return fake
}

// runJSONNucleus runs the command like the real process and decodes stdout into v.
// In JSON mode stdout carries only the JSON, and nothing else is written on success.
func runJSONNucleus(t *testing.T, v any, args ...string) string {
	t.Helper()
	stdout, stderr, err := runAsProcess(t, args...)
	if err != nil {
		t.Fatalf("nova %s: %v\nstderr: %s", strings.Join(args, " "), err, stderr)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want nothing", stderr)
	}
	dec := json.NewDecoder(strings.NewReader(stdout))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		t.Fatalf("stdout is no JSON of the expected shape: %v\n%s", err, stdout)
	}
	return stdout
}

func TestNucleusLsJSON(t *testing.T) {
	fake := withFakeNucleus(t)
	fake.SetEntries("/Projects/", []nucleus.Entry{
		{Path: "/Projects/Scene.usd", PathType: "file", Size: 4096, ModifiedTimestamp: 1767225600, ModifiedBy: "alice", Hash: "abc", Version: 3},
		{Path: "/Projects/Sims", PathType: "folder"},
	})
	fake.SetEntries("/Empty/", []nucleus.Entry{})

	for _, long := range []bool{false, true} {
		args := []string{"nucleus", "ls", "/Projects/", "-o", "json"}
		if long {
			args = append(args, "-l")
		}
		var got []nucleusEntryJSON
		runJSONNucleus(t, &got, args...)
		want := []nucleusEntryJSON{
			{Path: "/Projects/Scene.usd", Type: "file", Size: 4096, Modified: "2026-01-01T00:00:00Z", ModifiedBy: "alice", Hash: "abc", Version: 3},
			{Path: "/Projects/Sims", Type: "folder"},
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("ls -o json (long %t) = %+v, want %+v", long, got, want)
		}
	}

	if stdout, _, err := runAsProcess(t, "nucleus", "ls", "/Empty/", "-o", "json"); err != nil || stdout != "[]\n" {
		t.Errorf("empty directory: %q %v, want []", stdout, err)
	}
}

func TestNucleusStatJSON(t *testing.T) {
	fake := withFakeNucleus(t)
	fake.SetEntries("/Projects/", []nucleus.Entry{
		{Path: "/Projects/file.usd", PathType: "file", Size: 0, CreatedTimestamp: 1767225600, CreatedBy: "admin"},
	})
	var got nucleusEntryJSON
	out := runJSONNucleus(t, &got, "nucleus", "stat", "/Projects/file.usd", "-o", "json")
	want := nucleusEntryJSON{Path: "/Projects/file.usd", Type: "file", Created: "2026-01-01T00:00:00Z", CreatedBy: "admin"}
	if got != want {
		t.Errorf("stat -o json = %+v, want %+v", got, want)
	}
	// sizes are always there, also 0
	if !strings.Contains(out, `"size": 0`) {
		t.Errorf("size missing:\n%s", out)
	}
}

func TestNucleusCpDownloadJSON(t *testing.T) {
	fake := withFakeNucleus(t)
	fake.SetEntries("/Projects/mydir/", []nucleus.Entry{
		{Path: "/Projects/mydir/a.txt", PathType: "file"},
		{Path: "/Projects/mydir/sub", PathType: "folder"},
	})
	fake.SetEntries("/Projects/mydir/sub/", []nucleus.Entry{
		{Path: "/Projects/mydir/sub/b.txt", PathType: "file"},
	})
	fake.SetFile("/Projects/mydir/a.txt", []byte("file-a"))
	fake.SetFile("/Projects/mydir/sub/b.txt", []byte("bb"))

	// relative destinations are reported as absolute paths
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	t.Chdir(dir)
	var got nucleusCopyResult
	runJSONNucleus(t, &got, "nucleus", "cp", "n:/Projects/mydir/", "dl", "-o", "json")
	six, two := int64(6), int64(2)
	want := nucleusCopyResult{
		Source:      "n:/Projects/mydir/",
		Destination: filepath.Join(dir, "dl"),
		Files: []nucleusCopiedFile{
			{Path: filepath.Join(dir, "dl", "a.txt"), NucleusPath: "/Projects/mydir/a.txt", Size: &six},
			{Path: filepath.Join(dir, "dl", "sub", "b.txt"), NucleusPath: "/Projects/mydir/sub/b.txt", Size: &two},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("cp -o json =\n%s\nwant\n%s", mustJSON(t, got), mustJSON(t, want))
	}

	// a single file
	got = nucleusCopyResult{}
	runJSONNucleus(t, &got, "nucleus", "cp", "n:/Projects/mydir/a.txt", "a.txt", "-o", "json")
	want = nucleusCopyResult{
		Source: "n:/Projects/mydir/a.txt", Destination: filepath.Join(dir, "a.txt"),
		Files: []nucleusCopiedFile{{Path: filepath.Join(dir, "a.txt"), NucleusPath: "/Projects/mydir/a.txt", Size: &six}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("cp file -o json =\n%s\nwant\n%s", mustJSON(t, got), mustJSON(t, want))
	}
}

func TestNucleusCpUploadJSON(t *testing.T) {
	fake := withFakeNucleus(t)
	fake.SetEntries("/Projects/", []nucleus.Entry{})
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	if err := writeTestFile(filepath.Join(dir, "scene.usd"), "hello"); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	var got nucleusCopyResult
	runJSONNucleus(t, &got, "nucleus", "cp", "scene.usd", "n:/Projects/scene.usd", "-o", "json")
	five := int64(5)
	want := nucleusCopyResult{
		Source: filepath.Join(dir, "scene.usd"), Destination: "n:/Projects/scene.usd",
		Files: []nucleusCopiedFile{{Path: filepath.Join(dir, "scene.usd"), NucleusPath: "/Projects/scene.usd", Size: &five}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("cp upload -o json =\n%s\nwant\n%s", mustJSON(t, got), mustJSON(t, want))
	}
}

func TestNucleusServerListJSON(t *testing.T) {
	withFakeNucleus(t)
	withStdin(t, "")
	mustRun(t, "nucleus", "server", "add", "wb", "--host", "nucleus.wb.wandelbots.io")

	var got []nucleusServerJSON
	out := runJSONNucleus(t, &got, "nucleus", "server", "list", "-o", "json")
	if strings.Contains(out, "fake-secret-token") || strings.Contains(out, `"token"`) {
		t.Fatalf("server list -o json prints the token:\n%s", out)
	}
	if len(got) != 2 || got[0].Name != "fake" || !got[0].Selected || !got[0].HasToken || got[0].TLS ||
		got[1] != (nucleusServerJSON{Name: "wb", Host: "nucleus.wb.wandelbots.io", TLS: true}) {
		t.Errorf("server list -o json = %+v", got)
	}

	withConfig(t, "")
	if stdout, _, err := runAsProcess(t, "nucleus", "server", "list", "-o", "json"); err != nil || stdout != "[]\n" {
		t.Errorf("no servers: %q %v, want []", stdout, err)
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
