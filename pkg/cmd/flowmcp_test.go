package cmd

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestFlowMCPPrint(t *testing.T) {
	withConfig(t, `current-instance: lab
instances:
  - name: lab
    kind: host
    host: 192.168.1.10
    cellname: line 1
`)
	out := mustRun(t, "flow", "mcp", "--print")
	for _, want := range []string{
		"endpoint: http://192.168.1.10/line%201/controlplane/api/v1/mcp\n",
		"register: claude mcp add novaflow -- nova flow mcp\n",
		"pinned:   claude mcp add novaflow -- nova flow mcp --instance lab\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
}

// ADR-0010: stdout carries only protocol messages, one per line.
func TestFlowMCPServesOnlyProtocolOnStdout(t *testing.T) {
	var gotPath string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"id"`) {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, "{\n  \"jsonrpc\": \"2.0\", \"id\": 1, \"result\": {}\n}")
	}))
	defer s.Close()
	withConfig(t, `current-instance: lab
instances:
  - name: lab
    kind: host
    host: `+s.URL+`
`)
	in, err := os.CreateTemp(t.TempDir(), "stdin")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = in.WriteString(`{"jsonrpc":"2.0","id":1,"method":"initialize"}` + "\n" + `{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n")
	_, _ = in.Seek(0, 0)
	orig := os.Stdin
	os.Stdin = in
	defer func() { os.Stdin = orig }()

	stdout, stderr, err := runAsProcess(t, "flow", "mcp")
	if err != nil {
		t.Fatalf("%v\n%s", err, stderr)
	}
	if stdout != `{"jsonrpc":"2.0","id":1,"result":{}}`+"\n" || stderr != "" {
		t.Errorf("stdout %q, stderr %q", stdout, stderr)
	}
	if gotPath != "/cell/controlplane/api/v1/mcp" {
		t.Errorf("path %q", gotPath)
	}
}

// A host stored with a trailing slash gives no "//" in the path.
func TestFlowMCPEndpointTrimsSlash(t *testing.T) {
	if got, want := flowMCPEndpoint("https://x.wandelbots.io/", "cell"), "https://x.wandelbots.io/cell/controlplane/api/v1/mcp"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
