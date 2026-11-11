package programs_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/internal/livetest"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/novaapi"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/tool"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/toolsets/programs"
)

func connect(t *testing.T, c *novaapi.Client) *sdk.ClientSession {
	t.Helper()
	server := sdk.NewServer(&sdk.Implementation{Name: "test", Version: "test"}, nil)
	for _, reg := range programs.Tools(tool.Env{Client: c}) {
		reg.Add(server)
	}
	ct, st := sdk.NewInMemoryTransports()
	if _, err := server.Connect(context.Background(), st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := sdk.NewClient(&sdk.Implementation{Name: "c", Version: "c"}, nil).Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func call(t *testing.T, cs *sdk.ClientSession, name string, args map[string]any) (*sdk.CallToolResult, string) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool %s: %v", name, err)
	}
	return res, res.Content[0].(*sdk.TextContent).Text
}

type fakeCell struct {
	mu          sync.Mutex
	kinds       map[string]string // controller -> kind
	startStatus int
	started     map[string]any
	stopped     bool
}

func (f *fakeCell) client(t *testing.T) *novaapi.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		p := r.URL.Path
		switch {
		case p == "/cells/cell/controllers":
			names := []string{}
			for n := range f.kinds {
				names = append(names, n)
			}
			_ = json.NewEncoder(w).Encode(names)
		case strings.HasPrefix(p, "/cells/cell/controllers/"):
			name := strings.TrimPrefix(p, "/cells/cell/controllers/")
			_, _ = io.WriteString(w, `{"name":"`+name+`","configuration":{"kind":"`+f.kinds[name]+`"}}`)
		case p == "/experimental/cells/cell/programs":
			_, _ = io.WriteString(w, `[{"program":"weld","app":"b","name":"Weld"},{"program":"pick","app":"a","name":"Pick","input_schema":{"type":"object"}}]`)
		case p == "/experimental/cells/cell/programs/pick" && r.Method == http.MethodGet:
			_, _ = io.WriteString(w, `{"program":"pick","app":"a","name":"Pick","input_schema":{"type":"object","properties":{"count":{"type":"integer"}}}}`)
		case p == "/experimental/cells/cell/programs/pick/start":
			_ = json.NewDecoder(r.Body).Decode(&f.started)
			if f.startStatus != 0 {
				w.WriteHeader(f.startStatus)
				_, _ = io.WriteString(w, `{}`)
				return
			}
			_, _ = io.WriteString(w, `{"run":"run-1","program":"pick","app":"a","state":"PREPARING","stdout":"secret log"}`)
		case p == "/experimental/cells/cell/programs/pick/stop":
			f.stopped = true
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{}`)
		}
	}))
	t.Cleanup(srv.Close)
	return novaapi.NewClient(srv.URL, nil, nil, nil)
}

func TestListAndDescribe(t *testing.T) {
	cs := connect(t, (&fakeCell{}).client(t))
	res, text := call(t, cs, "list_programs", map[string]any{"cell": "cell"})
	progs := res.StructuredContent.(map[string]any)["programs"].([]any)
	if res.IsError || len(progs) != 2 || progs[0].(map[string]any)["program"] != "pick" {
		t.Fatalf("unexpected list %q %v", text, progs)
	}
	res, text = call(t, cs, "describe_program", map[string]any{"cell": "cell", "program": "pick"})
	if res.IsError || !strings.Contains(text, "app=a") || res.StructuredContent.(map[string]any)["input_schema"] == nil {
		t.Fatalf("unexpected describe %q", text)
	}
	_, text = call(t, cs, "describe_program", map[string]any{"cell": "cell", "program": "nope"})
	if !strings.HasPrefix(text, "NOT_FOUND:") {
		t.Errorf("unexpected describe %q", text)
	}
}

func TestStart_RefusedWithAPhysicalControllerInTheCell(t *testing.T) {
	f := &fakeCell{kinds: map[string]string{"sim": "VirtualController", "kuka": "KukaController"}}
	cs := connect(t, f.client(t))
	res, text := call(t, cs, "start_program", map[string]any{"cell": "cell", "program": "pick"})
	if !res.IsError || !strings.HasPrefix(text, "APPROVAL_REQUIRED:") || f.started != nil {
		t.Fatalf("expected APPROVAL_REQUIRED without a start, got %q (started %v)", text, f.started)
	}
}

func TestStart_AllVirtual(t *testing.T) {
	f := &fakeCell{kinds: map[string]string{"sim": "VirtualController"}}
	cs := connect(t, f.client(t))
	res, text := call(t, cs, "start_program", map[string]any{"cell": "cell", "program": "pick", "arguments": map[string]any{"count": 3}})
	if res.IsError || !strings.Contains(text, "run=run-1") || !strings.Contains(text, "state=PREPARING") {
		t.Fatalf("unexpected result %q", text)
	}
	if f.started["arguments"].(map[string]any)["count"].(float64) != 3 {
		t.Errorf("arguments not passed: %v", f.started)
	}
	raw, _ := json.Marshal(res.StructuredContent)
	if strings.Contains(string(raw), "secret log") {
		t.Errorf("run output must not be handed on: %s", raw)
	}
}

func TestStart_AlreadyRunning(t *testing.T) {
	f := &fakeCell{kinds: map[string]string{}, startStatus: http.StatusNotAcceptable}
	cs := connect(t, f.client(t))
	_, text := call(t, cs, "start_program", map[string]any{"cell": "cell", "program": "pick"})
	if !strings.HasPrefix(text, "ALREADY_EXISTS:") {
		t.Fatalf("unexpected result %q", text)
	}
}

func TestStop_NeverGated(t *testing.T) {
	f := &fakeCell{kinds: map[string]string{"kuka": "KukaController"}}
	cs := connect(t, f.client(t))
	res, text := call(t, cs, "stop_program", map[string]any{"cell": "cell", "program": "pick"})
	if res.IsError || !f.stopped {
		t.Fatalf("stop must work with physical controllers in the cell, got %q", text)
	}
}

func TestLivePrograms(t *testing.T) {
	cs := connect(t, livetest.Client(t))
	res, text := call(t, cs, "list_programs", map[string]any{"cell": livetest.Cell()})
	t.Log(text)
	if res.IsError {
		t.Fatal(text)
	}
	_, text = call(t, cs, "describe_program", map[string]any{"cell": livetest.Cell(), "program": "test.mcp-test-missing"})
	t.Log(text)
	if !strings.HasPrefix(text, "NOT_FOUND:") && !strings.HasPrefix(text, "INTERNAL:") {
		t.Errorf("unexpected answer for a missing program: %q", text)
	}
}
