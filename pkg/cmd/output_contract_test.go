package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/wandelbotsgmbh/nova-cli/pkg/debug"
)

// fakeInstance answers the NOVA API v2 calls of the list commands.
func fakeInstance(t *testing.T) *httptest.Server {
	t.Helper()
	answers := map[string]any{
		"/api/v2/system/version":         "26.1.0",
		"/api/v2/cells":                  []string{"cell", "line2"},
		"/api/v2/cells/cell/controllers": []string{"ur5", "kuka"},
		"/api/v2/cells/cell/apps":        []string{"waypoints"},
	}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a, ok := answers[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(a)
	}))
	t.Cleanup(s.Close)
	return s
}

func withFakeInstance(t *testing.T) {
	t.Helper()
	s := fakeInstance(t)
	withConfig(t, `current-instance: lab
instances:
  - name: lab
    kind: host
    host: `+s.URL+`
`)
	t.Setenv(debug.EnvVar, "")
	t.Cleanup(func() { debug.Enable(false) })
}

// runAsProcess executes the root command like main does, without SetOut/SetErr:
// cobra then falls back to the process's stdout and stderr, which are captured.
// With writers set, cmd.Println would write data to the "out" writer and hide
// that it goes to stderr in the real CLI.
func runAsProcess(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	resetState(rootCmd)
	rootCmd.SetOut(nil)
	rootCmd.SetErr(nil)
	rootCmd.SetArgs(args)

	capture := func(f **os.File) func() string {
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		orig := *f
		*f = w
		done := make(chan string)
		go func() {
			b, _ := io.ReadAll(r)
			done <- string(b)
		}()
		return func() string {
			*f = orig
			w.Close()
			return <-done
		}
	}
	stdout, stderr := capture(&os.Stdout), capture(&os.Stderr)
	err := executeRoot()
	return stdout(), stderr(), err
}

// ADR-0010: data goes to stdout, so `$(nova cell list)` and pipes work; nothing
// else is written on success.
func TestListCommandsWriteDataToStdout(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"cell", "list"}, "cell\nline2\n"},
		{[]string{"controller", "list"}, "ur5\nkuka\n"},
		{[]string{"app", "list"}, "waypoints\n"},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			withFakeInstance(t)
			stdout, stderr, err := runAsProcess(t, tc.args...)
			if err != nil {
				t.Fatalf("error: %v\nstderr: %s", err, stderr)
			}
			if stdout != tc.want {
				t.Errorf("stdout = %q, want %q", stdout, tc.want)
			}
			if stderr != "" {
				t.Errorf("stderr = %q, want nothing", stderr)
			}
		})
	}
}

// --debug and NOVA_DEBUG only add to stderr; stdout stays parseable.
func TestDebugOutputGoesToStderr(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		env  string
	}{
		{"flag", []string{"cell", "list", "--debug"}, ""},
		{"env", []string{"cell", "list"}, "1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withFakeInstance(t)
			t.Setenv(debug.EnvVar, tc.env)
			stdout, log, err := runAsProcess(t, tc.args...)
			if err != nil {
				t.Fatal(err)
			}
			if stdout != "cell\nline2\n" {
				t.Errorf("stdout = %q, want only the cells", stdout)
			}
			for _, want := range []string{
				`debug: target: instance "lab" (from config): kind host`,
				"debug: http: > GET http://127.0.0.1",
				"/api/v2/cells\n",
				"debug: http: < 200 OK",
				`debug: http: < ["cell","line2"]`,
			} {
				if !strings.Contains(log, want) {
					t.Errorf("debug output lacks %q:\n%s", want, log)
				}
			}
		})
	}
}

func TestNoDebugOutputByDefault(t *testing.T) {
	withFakeInstance(t)
	t.Setenv(debug.EnvVar, "0")
	_, stderr, err := runAsProcess(t, "cell", "list")
	if err != nil {
		t.Fatal(err)
	}
	if stderr != "" {
		t.Errorf("unexpected stderr output:\n%s", stderr)
	}
}
