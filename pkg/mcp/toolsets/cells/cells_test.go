package cells_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/internal/livetest"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/novaapi"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/tool"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/toolsets/cells"
)

type elicitFunc = func(context.Context, *sdk.ElicitRequest) (*sdk.ElicitResult, error)

func connect(t *testing.T, c *novaapi.Client, elicit elicitFunc) *sdk.ClientSession {
	t.Helper()
	server := sdk.NewServer(&sdk.Implementation{Name: "test", Version: "test"}, nil)
	for _, reg := range cells.Tools(tool.Env{Client: c}) {
		reg.Add(server)
	}
	ct, st := sdk.NewInMemoryTransports()
	if _, err := server.Connect(context.Background(), st, nil); err != nil {
		t.Fatal(err)
	}
	opts := &sdk.ClientOptions{}
	if elicit != nil {
		opts.ElicitationHandler = elicit
	}
	cs, err := sdk.NewClient(&sdk.Implementation{Name: "c", Version: "c"}, opts).Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

type request struct {
	Method, Path, Query, Body string
}

// recorder answers by "METHOD path" and records every request.
type recorder struct {
	mu       sync.Mutex
	requests []request
}

func (r *recorder) server(t *testing.T, answers map[string]string) *novaapi.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		r.mu.Lock()
		r.requests = append(r.requests, request{req.Method, req.URL.Path, req.URL.RawQuery, string(body)})
		r.mu.Unlock()
		answer, ok := answers[req.Method+" "+req.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		status, out, _ := strings.Cut(answer, " ")
		w.Header().Set("Content-Type", "application/json")
		switch status {
		case "202":
			w.WriteHeader(http.StatusAccepted)
		case "204":
			w.WriteHeader(http.StatusNoContent)
		case "409":
			w.WriteHeader(http.StatusConflict)
		}
		_, _ = w.Write([]byte(out))
	}))
	t.Cleanup(srv.Close)
	return novaapi.NewClient(srv.URL, nil, nil, nil)
}

func (r *recorder) sent(method string) []request {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []request
	for _, req := range r.requests {
		if req.Method == method {
			out = append(out, req)
		}
	}
	return out
}

func call(t *testing.T, cs *sdk.ClientSession, name string, args map[string]any) (bool, string, string) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	raw, _ := json.Marshal(res.StructuredContent)
	return res.IsError, res.Content[0].(*sdk.TextContent).Text, string(raw)
}

func TestDeployCell_ValidatesNameBeforeSending(t *testing.T) {
	rec := &recorder{}
	cs := connect(t, rec.server(t, nil), nil)
	for _, bad := range []string{"", "Cell", "1cell", "cell_x", strings.Repeat("a", 33)} {
		isErr, text, _ := call(t, cs, "deploy_cell", map[string]any{"name": bad})
		if !isErr || !strings.HasPrefix(text, "INVALID_ARGUMENT") {
			t.Errorf("%q: expected INVALID_ARGUMENT, got %s", bad, text)
		}
	}
	if len(rec.requests) != 0 {
		t.Errorf("invalid names must not reach the API: %+v", rec.requests)
	}
}

func TestDeployCell_PendingAndConflict(t *testing.T) {
	rec := &recorder{}
	cs := connect(t, rec.server(t, map[string]string{"POST /cells": "202 "}), nil)
	isErr, text, _ := call(t, cs, "deploy_cell", map[string]any{"name": "mcp-test", "description": "d"})
	if isErr || !strings.Contains(text, "status=pending") {
		t.Fatalf("unexpected: %s", text)
	}
	if body := rec.sent("POST")[0].Body; body != `{"description":"d","name":"mcp-test"}` {
		t.Errorf("unexpected body %s", body)
	}

	cs = connect(t, (&recorder{}).server(t, map[string]string{"POST /cells": "409 "}), nil)
	isErr, text, _ = call(t, cs, "deploy_cell", map[string]any{"name": "cell"})
	if !isErr || text != "ALREADY_EXISTS: deploy_cell: a cell named 'cell' already exists" {
		t.Errorf("unexpected: %s", text)
	}
}

func TestSetCellStatus(t *testing.T) {
	rec := &recorder{}
	cs := connect(t, rec.server(t, map[string]string{"PUT /cells/c1/status": "202 "}), nil)
	isErr, text, _ := call(t, cs, "set_cell_status", map[string]any{"cell": "c1", "operating_state": "paused"})
	if !isErr || !strings.HasPrefix(text, "INVALID_ARGUMENT") {
		t.Errorf("expected INVALID_ARGUMENT, got %s", text)
	}
	isErr, text, _ = call(t, cs, "set_cell_status", map[string]any{"cell": "c1", "operating_state": "INACTIVE"})
	if isErr || !strings.Contains(text, "status=pending") {
		t.Fatalf("unexpected: %s", text)
	}
	if q := rec.sent("PUT")[0].Query; q != "operating_state=INACTIVE" {
		t.Errorf("unexpected query %q", q)
	}
}

func TestCheckCellUpdate_NoUpdateAndNotFound(t *testing.T) {
	cs := connect(t, (&recorder{}).server(t, map[string]string{"GET /cells/c1/update": "204 "}), nil)
	isErr, text, _ := call(t, cs, "check_cell_update", map[string]any{"cell": "c1"})
	if isErr || !strings.HasPrefix(text, "OK: no update available") {
		t.Errorf("unexpected: %s", text)
	}
	isErr, text, _ = call(t, cs, "check_cell_update", map[string]any{"cell": "nope"})
	if !isErr || text != "NOT_FOUND: check_cell_update: cell 'nope' not found" {
		t.Errorf("unexpected: %s", text)
	}
}

func TestDeleteCell_NeedsTheUsersApproval(t *testing.T) {
	answers := map[string]string{
		"GET /cells/c1":    `200 {"name":"c1","controllers":[{"name":"r1","configuration":{"kind":"VirtualController"}}],"apps":[]}`,
		"DELETE /cells/c1": "202 ",
	}

	t.Run("client without elicitation", func(t *testing.T) {
		rec := &recorder{}
		cs := connect(t, rec.server(t, answers), nil)
		isErr, text, _ := call(t, cs, "delete_cell", map[string]any{"cell": "c1"})
		if !isErr || !strings.HasPrefix(text, "APPROVAL_REQUIRED") || len(rec.sent("DELETE")) != 0 {
			t.Errorf("expected a refusal without DELETE, got %s", text)
		}
	})

	t.Run("declined", func(t *testing.T) {
		rec := &recorder{}
		cs := connect(t, rec.server(t, answers), func(context.Context, *sdk.ElicitRequest) (*sdk.ElicitResult, error) {
			return &sdk.ElicitResult{Action: "decline"}, nil
		})
		isErr, text, _ := call(t, cs, "delete_cell", map[string]any{"cell": "c1"})
		if !isErr || !strings.HasPrefix(text, "APPROVAL_REQUIRED") || len(rec.sent("DELETE")) != 0 {
			t.Errorf("expected a refusal without DELETE, got %s", text)
		}
	})

	t.Run("approved", func(t *testing.T) {
		rec := &recorder{}
		var asked string
		cs := connect(t, rec.server(t, answers), func(_ context.Context, req *sdk.ElicitRequest) (*sdk.ElicitResult, error) {
			asked = req.Params.Message
			return &sdk.ElicitResult{Action: "accept", Content: map[string]any{"approve": true}}, nil
		})
		isErr, text, _ := call(t, cs, "delete_cell", map[string]any{"cell": "c1"})
		if isErr || !strings.Contains(text, "status=pending") || len(rec.sent("DELETE")) != 1 {
			t.Fatalf("expected one DELETE after approval, got %s", text)
		}
		if !strings.Contains(asked, "'c1'") || !strings.Contains(asked, "1 controller(s)") {
			t.Errorf("the dialog must name what is deleted, got %q", asked)
		}
	})

	t.Run("unknown cell is not asked about", func(t *testing.T) {
		asked := false
		cs := connect(t, (&recorder{}).server(t, answers), func(context.Context, *sdk.ElicitRequest) (*sdk.ElicitResult, error) {
			asked = true
			return &sdk.ElicitResult{Action: "accept", Content: map[string]any{"approve": true}}, nil
		})
		isErr, text, _ := call(t, cs, "delete_cell", map[string]any{"cell": "nope"})
		if !isErr || !strings.HasPrefix(text, "NOT_FOUND") || asked {
			t.Errorf("unexpected: %s (asked %v)", text, asked)
		}
	})
}

// TestLiveCellLifecycle deploys mcp-test-cell, checks its status and update, deactivates
// and reactivates it, and deletes it again with an approved dialog.
func TestLiveCellLifecycle(t *testing.T) {
	const cell = "mcp-test-cell"
	cs := connect(t, livetest.Client(t), func(_ context.Context, req *sdk.ElicitRequest) (*sdk.ElicitResult, error) {
		if !strings.Contains(req.Params.Message, "'"+cell+"'") {
			return &sdk.ElicitResult{Action: "decline"}, nil
		}
		return &sdk.ElicitResult{Action: "accept", Content: map[string]any{"approve": true}}, nil
	})

	isErr, text, _ := call(t, cs, "deploy_cell", map[string]any{"name": cell, "description": "nova mcp live test"})
	if isErr && !strings.HasPrefix(text, "ALREADY_EXISTS") {
		t.Fatal(text)
	}
	t.Log(text)
	t.Cleanup(func() {
		_, text, _ := call(t, cs, "delete_cell", map[string]any{"cell": cell})
		t.Log("cleanup: " + text)
	})

	deadline := time.Now().Add(3 * time.Minute)
	for {
		isErr, text, _ = call(t, cs, "get_cell_status", map[string]any{"cell": cell})
		if !isErr && strings.Contains(text, "problems=0") && !strings.Contains(text, "services=0") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("cell not healthy in time: %s", text)
		}
		time.Sleep(5 * time.Second)
	}
	t.Log(text)

	isErr, text, _ = call(t, cs, "check_cell_update", map[string]any{"cell": cell})
	if isErr {
		t.Error(text)
	}
	t.Log(text)

	for _, state := range []string{"INACTIVE", "ACTIVE"} {
		isErr, text, _ = call(t, cs, "set_cell_status", map[string]any{"cell": cell, "operating_state": state})
		if isErr {
			t.Fatal(text)
		}
		deadline := time.Now().Add(2 * time.Minute)
		for {
			_, text, _ = call(t, cs, "get_cell_status", map[string]any{"cell": cell})
			if strings.Contains(text, "operating_state="+state) {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("cell not %s in time: %s", state, text)
			}
			time.Sleep(3 * time.Second)
		}
		t.Log(text)
	}
}
