package cmd

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/wandelbotsgmbh/nova-cli/pkg/debug"
)

// fakeNovaflow is an instance with NovaFlow deployed by service-manager on cell
// "cell": flow "pick" and global "speed" exist.
func fakeNovaflow(t *testing.T) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		answer := func(status int, v any) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(v)
		}
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v2/system/version":
			answer(200, "26.8.0")
		case "GET /api/v2/cells/cell/apps":
			answer(200, []string{"forest", "controlplane", "flow"})
		case "GET /api/v2/internal/cells/cell/feature-flags":
			answer(200, map[string]any{
				"novaflow":         true,
				"novaflow_version": "1.2.0",
				"novaflow_status":  map[string]any{"enabled": true, "deployed_version": "1.2.0", "state": "deployed"},
			})
		case "GET /cell/forest/trees":
			answer(200, map[string]any{
				"total":    1,
				"has_more": false,
				"items": []map[string]any{{
					"id": "pick", "name": "Pick", "created_at": "2026-11-01T08:00:00Z", "updated_at": "2026-11-02T09:30:00Z",
				}},
				"corrupted": []map[string]any{{"id": "broken", "error": "invalid tree"}},
			})
		case "POST /cell/forest/trees":
			answer(201, map[string]any{})
		case "GET /cell/controlplane/api/v1/globals":
			answer(200, map[string]any{"globals": []map[string]any{{"key": "speed", "type": "number", "value": 1}}})
		case "PUT /cell/controlplane/api/v1/globals":
			answer(200, map[string]any{})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(s.Close)
	return s
}

func withFakeNovaflow(t *testing.T) {
	t.Helper()
	s := fakeNovaflow(t)
	withConfig(t, `current-instance: lab
instances:
  - name: lab
    kind: host
    host: `+s.URL+`
    cellname: cell
`)
	t.Setenv(debug.EnvVar, "")
	t.Cleanup(func() { debug.Enable(false) })
}

// decodeStdout fails unless stdout is exactly one JSON value.
func decodeStdout(t *testing.T, stdout string, v any) {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(stdout))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		t.Fatalf("stdout is no JSON result: %v\n%s", err, stdout)
	}
	if dec.More() {
		t.Fatalf("stdout carries more than the JSON result:\n%s", stdout)
	}
}

func TestFlowListJSON(t *testing.T) {
	withFakeNovaflow(t)
	stdout, stderr, err := runAsProcess(t, "flow", "list", "-o", "json")
	if err != nil {
		t.Fatalf("error: %v\nstderr: %s", err, stderr)
	}
	want := `[{"id":"pick","name":"Pick","created_at":"2026-11-01T08:00:00Z","updated_at":"2026-11-02T09:30:00Z"}]`
	if got := compactJSON(t, stdout); got != want {
		t.Errorf("stdout = %s, want %s", got, want)
	}
	if !strings.Contains(stderr, "WARN: flow broken can't be read: invalid tree") {
		t.Errorf("stderr lacks the warning about the corrupted flow: %q", stderr)
	}
}

func TestFlowListTextUnchanged(t *testing.T) {
	withFakeNovaflow(t)
	stdout, _, err := runAsProcess(t, "flow", "list")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(stdout, "ID    NAME  UPDATED\npick  Pick  ") {
		t.Errorf("stdout = %q", stdout)
	}
}

func TestFlowStatusJSON(t *testing.T) {
	withFakeNovaflow(t)
	stdout, stderr, err := runAsProcess(t, "flow", "status", "-o", "json")
	if err != nil {
		t.Fatalf("error: %v\nstderr: %s", err, stderr)
	}
	var got flowStatusResult
	decodeStdout(t, stdout, &got)
	if got.Cell != "cell" || !got.Installed || got.ManagedBy != "nova" || got.State != "deployed" ||
		got.Version != "1.2.0" || got.RequestedVersion != "1.2.0" || !strings.HasSuffix(got.EditorURL, "/cell/flow/") {
		t.Errorf("result = %+v", got)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want nothing", stderr)
	}
}

func TestFlowStatusJSONNotInstalled(t *testing.T) {
	s := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(s.Close)
	withConfig(t, "current-instance: lab\ninstances:\n  - name: lab\n    kind: host\n    host: "+s.URL+"\n    cellname: cell\n")
	t.Setenv(debug.EnvVar, "")

	stdout, stderr, err := runAsProcess(t, "flow", "status", "-o", "json")
	if err != nil {
		t.Fatalf("error: %v\nstderr: %s", err, stderr)
	}
	// no feature flag API: install goes through the installer app, as the text says
	if got, want := compactJSON(t, stdout), `{"cell":"cell","installed":false,"installs_with":"novaflow-installer"}`; got != want {
		t.Errorf("stdout = %s, want %s", got, want)
	}
}

// A feature flag API that fails is reported, as in the text output.
func TestFlowStatusJSONFeatureFlagsError(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/internal/cells/cell/feature-flags" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"detail":"forbidden"}`))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(s.Close)
	withConfig(t, "current-instance: lab\ninstances:\n  - name: lab\n    kind: host\n    host: "+s.URL+"\n    cellname: cell\n")
	t.Setenv(debug.EnvVar, "")

	stdout, stderr, err := runAsProcess(t, "flow", "status", "-o", "json")
	if err != nil {
		t.Fatalf("error: %v\nstderr: %s", err, stderr)
	}
	var got flowStatusResult
	decodeStdout(t, stdout, &got)
	if got.Installed || got.InstallsWith != "" || !strings.Contains(got.FeatureFlagsError, "403") {
		t.Errorf("result = %+v", got)
	}
	text, _, err := runAsProcess(t, "flow", "status")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "Feature flags:") || !strings.Contains(text, got.FeatureFlagsError) {
		t.Errorf("text output %q doesn't carry %q", text, got.FeatureFlagsError)
	}
}

// writeFlowBackup writes a backup with flows "pick" (exists on the fake) and
// "place" (new) and global "speed" (exists).
func writeFlowBackup(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "backup.tar.gz")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for name, content := range map[string]string{
		"manifest.json":    `{"format":1,"cell":"cell","flows":["pick","place"],"globals":1}`,
		"globals.json":     `[{"key":"speed","type":"number","value":2}]`,
		"flows/pick.json":  `{"id":"pick","name":"Pick","tree":{}}`,
		"flows/place.json": `{"id":"place","name":"Place","tree":{}}`,
	} {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestFlowBackupRestoreJSON(t *testing.T) {
	for _, tc := range []struct {
		name   string
		dryRun bool
	}{{"restore", false}, {"dry run", true}} {
		t.Run(tc.name, func(t *testing.T) {
			withFakeNovaflow(t)
			backup := writeFlowBackup(t)
			args := []string{"flow", "backup", "restore", backup, "-o", "json"}
			if tc.dryRun {
				args = append(args, "--dry-run")
			}
			stdout, stderr, err := runAsProcess(t, args...)
			if err != nil {
				t.Fatalf("error: %v\nstderr: %s", err, stderr)
			}
			var got flowRestoreResult
			decodeStdout(t, stdout, &got)
			want := []flowRestoreItem{
				{Kind: "global", Key: "speed", Action: "skip"},
				{Kind: "flow", Key: "pick", Action: "skip"},
				{Kind: "flow", Key: "place", Action: "create"},
			}
			if got.Cell != "cell" || got.Path != backup || got.DryRun != tc.dryRun || len(got.Items) != len(want) {
				t.Fatalf("result = %+v", got)
			}
			for i := range want {
				if got.Items[i] != want[i] {
					t.Errorf("item %d = %+v, want %+v", i, got.Items[i], want[i])
				}
			}
			// hints and progress stay on stderr
			hint := "Existing items were kept"
			if tc.dryRun {
				hint = "Dry run, nothing was changed"
			}
			if !strings.Contains(stderr, hint) {
				t.Errorf("stderr lacks %q: %q", hint, stderr)
			}
		})
	}
}

func TestFlowBackupRestoreTextUnchanged(t *testing.T) {
	withFakeNovaflow(t)
	stdout, _, err := runAsProcess(t, "flow", "backup", "restore", writeFlowBackup(t))
	if err != nil {
		t.Fatal(err)
	}
	want := "global  speed  skip (exists)\nflow    pick   skip (exists)\nflow    place  create\n"
	if stdout != want {
		t.Errorf("stdout = %q, want %q", stdout, want)
	}
}

// fakeTools puts stubs of git and uvx first in PATH; uvx creates the project
// directory and writes to stdout like copier may.
func fakeTools(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell script stubs")
	}
	bin := t.TempDir()
	stubs := map[string]string{
		"git": "#!/bin/sh\nexit 0\n",
		"uvx": "#!/bin/sh\necho 'copier says hello'\nfor last; do :; done\nmkdir -p \"$last\"\n",
	}
	for name, script := range stubs {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestFlowActionCreateJSON(t *testing.T) {
	withFakeNovaflow(t)
	fakeTools(t)
	stdout, stderr, err := runAsProcess(t, "flow", "action", "create", "gripper-actions", "--action", "OpenGripper", "-o", "json")
	if err != nil {
		t.Fatalf("error: %v\nstderr: %s", err, stderr)
	}
	var got createdFlowAction
	decodeStdout(t, stdout, &got)
	if got.Name != "gripper-actions" || !filepath.IsAbs(got.Path) || filepath.Base(got.Path) != "gripper-actions" {
		t.Errorf("result = %+v", got)
	}
	if fi, err := os.Stat(got.Path); err != nil || !fi.IsDir() {
		t.Errorf("no project directory at the reported path: %v", err)
	}
	// copier's output and the next steps go to stderr
	for _, want := range []string{"copier says hello", "Next steps:"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q: %q", want, stderr)
		}
	}
}
