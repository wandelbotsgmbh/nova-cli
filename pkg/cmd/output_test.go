package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wandelbotsgmbh/nova-cli/pkg/debug"
)

// ADR-0010: -o json prints a stable JSON shape on stdout and nothing on stderr.
func TestListCommandsJSON(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"cell", "list", "-o", "json"}, `[{"name":"cell"},{"name":"line2"}]`},
		{[]string{"controller", "list", "--output", "json"}, `[{"name":"ur5"},{"name":"kuka"}]`},
		{[]string{"app", "list", "-o", "json"}, `[{"name":"waypoints"}]`},
		// an empty list is [], not null
		{[]string{"app", "list", "-o", "json", "--instance", "lab2"}, `[]`},
		// text is the default and can be given explicitly
		{[]string{"cell", "list", "-o", "text"}, "cell\nline2"},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			s := fakeInstance(t)
			withConfig(t, `current-instance: lab
instances:
  - name: lab
    kind: host
    host: `+s.URL+`
  - name: lab2
    kind: host
    host: `+s.URL+`
    cellname: line2
`)
			t.Setenv(debug.EnvVar, "")
			stdout, stderr, err := runAsProcess(t, tc.args...)
			if err != nil {
				t.Fatalf("error: %v\nstderr: %s", err, stderr)
			}
			if got := compactJSON(t, stdout); got != tc.want {
				t.Errorf("stdout = %q, want %q", got, tc.want)
			}
			if stderr != "" {
				t.Errorf("stderr = %q, want nothing", stderr)
			}
		})
	}
}

func TestOutputFlagRejectsUnknownFormat(t *testing.T) {
	withFakeInstance(t)
	stdout, stderr, err := runAsProcess(t, "cell", "list", "-o", "yaml")
	if err == nil {
		t.Fatal("no error")
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want nothing", stdout)
	}
	if want := `invalid argument "yaml" for "-o, --output" flag: must be text or json`; !strings.Contains(stderr, want) {
		t.Errorf("stderr = %q, want %q", stderr, want)
	}
}

// #60: commands that write files name them in their JSON result.
func TestCellSaveJSONNamesTheFile(t *testing.T) {
	withFakeInstance(t)
	stdout, stderr, err := runAsProcess(t, "cell", "save", "-o", "json")
	if err != nil {
		t.Fatalf("error: %v\nstderr: %s", err, stderr)
	}
	var got savedCell
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("stdout is no JSON result: %v\n%s", err, stdout)
	}
	if got.Cell != "cell" || got.Format != "yaml" || !filepath.IsAbs(got.Path) || filepath.Base(got.Path) != "cell.cell" {
		t.Errorf("result = %+v", got)
	}
	if _, err := os.Stat(got.Path); err != nil {
		t.Errorf("the named file doesn't exist: %v", err)
	}
}

func TestCellSaveTextPrintsNothing(t *testing.T) {
	withFakeInstance(t)
	stdout, stderr, err := runAsProcess(t, "cell", "save")
	if err != nil {
		t.Fatalf("error: %v\nstderr: %s", err, stderr)
	}
	if stdout != "" || stderr != "" {
		t.Errorf("stdout = %q, stderr = %q, want nothing", stdout, stderr)
	}
}

func TestCatalogListJSON(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/catalog.yaml" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte("schemaVersion: v1\nentries:\n  - name: alpha\n  - name: beta\n"))
	}))
	t.Cleanup(s.Close)
	withConfig(t, "catalog:\n  hosts:\n    - name: mine\n      url: "+s.URL+"/\n")
	t.Setenv(debug.EnvVar, "")

	stdout, stderr, err := runAsProcess(t, "catalog", "list", "mine", "-o", "json")
	if err != nil {
		t.Fatalf("error: %v\nstderr: %s", err, stderr)
	}
	want := `[{"name":"alpha","catalog":"mine"},{"name":"beta","catalog":"mine"}]`
	if got := compactJSON(t, stdout); got != want {
		t.Errorf("stdout = %s, want %s", got, want)
	}

	stdout, _, err = runAsProcess(t, "catalog", "list", "mine")
	if err != nil {
		t.Fatal(err)
	}
	if stdout != "alpha\nbeta\n" {
		t.Errorf("text stdout = %q", stdout)
	}
}

// compactJSON returns JSON without whitespace (keeping the field order), and
// other output trimmed.
func compactJSON(t *testing.T, s string) string {
	t.Helper()
	var b bytes.Buffer
	if err := json.Compact(&b, []byte(s)); err != nil {
		return strings.TrimSpace(s)
	}
	return b.String()
}
