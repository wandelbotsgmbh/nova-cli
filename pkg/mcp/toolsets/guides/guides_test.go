package guides_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/internal/livetest"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/novaapi"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/tool"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/toolsets/guides"
)

const fixtureSpec = `
openapi: 3.1.0
info: {title: NOVA, version: 2.8.0}
paths:
  /cells/{cell}/controllers/{controller}/motion-groups/{motion-group}/state:
    parameters:
      - $ref: '#/components/parameters/Cell'
    get:
      operationId: getCurrentMotionGroupState
      summary: State of a motion group
      tags: [Motion Group]
      x-scope: can_operate_controllers
      parameters:
        - {name: response_coordinate_system, in: query, schema: {type: string}}
      responses:
        200:
          description: OK
          content:
            application/json:
              schema: {$ref: '#/components/schemas/MotionGroupState'}
  /cells/{cell}/controllers/{controller}/execution/jogging:
    get:
      operationId: executeJogging
      summary: Execute jogging
      description: "> Websocket endpoint\n\nJog a motion group."
      tags: [Jogging]
      requestBody:
        required: true
        content:
          application/json:
            schema: {$ref: '#/components/schemas/Node'}
      responses:
        '200': {description: OK}
  /experimental/cells/{cell}/programs:
    get:
      operationId: listPrograms
      summary: List programs
      tags: [Program]
      responses:
        '200': {description: OK}
components:
  parameters:
    Cell: {name: cell, in: path, required: true, description: The cell., schema: {type: string}}
  schemas:
    MotionGroupState:
      type: object
      required: [joint_position]
      properties:
        joint_position: {$ref: '#/components/schemas/Joints'}
        standstill: {type: boolean, description: Standing still., example: true}
    Joints: {type: array, items: {type: number, format: double}}
    Node:
      type: object
      properties:
        child: {$ref: '#/components/schemas/Node'}
`

func serveSpec(t *testing.T, body string) (*novaapi.Client, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/ui/public.openapi.yaml" {
			http.NotFound(w, r)
			return
		}
		calls.Add(1)
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return novaapi.NewClient(server.URL+"/api/v2", nil, nil, nil), &calls
}

func connect(t *testing.T, client *novaapi.Client) *sdk.ClientSession {
	t.Helper()
	server := sdk.NewServer(&sdk.Implementation{Name: "t", Version: "t"}, nil)
	for _, reg := range guides.Tools(tool.Env{Client: client}) {
		if reg.Toolset != tool.Guides || !reg.ReadOnly {
			t.Fatalf("every guides tool is a read in the guides toolset: %+v", reg)
		}
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

func call(t *testing.T, cs *sdk.ClientSession, name string, args map[string]any, out any) (string, bool) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if out != nil && !res.IsError {
		raw, _ := json.Marshal(res.StructuredContent)
		if err := json.Unmarshal(raw, out); err != nil {
			t.Fatalf("%s: decode %s: %v", name, raw, err)
		}
	}
	return res.Content[0].(*sdk.TextContent).Text, res.IsError
}

func TestGetGuide_EveryTopic(t *testing.T) {
	cs := connect(t, novaapi.NewClient("http://nova.invalid/api/v2", nil, nil, nil))
	for _, topic := range guides.Topics {
		var out struct {
			Guide string `json:"guide"`
		}
		text, isErr := call(t, cs, "get_guide", map[string]any{"topic": topic}, &out)
		if isErr || !strings.HasPrefix(text, "OK:") {
			t.Fatalf("%s: %s", topic, text)
		}
		if !strings.Contains(out.Guide, "```python") || !strings.Contains(out.Guide, "Connecting from a script") {
			t.Errorf("%s: guide needs an example and the connection notes", topic)
		}
	}
	if text, isErr := call(t, cs, "get_guide", map[string]any{"topic": "teleport"}, nil); !isErr || !strings.HasPrefix(text, "INVALID_ARGUMENT:") {
		t.Errorf("unknown topic: %s", text)
	}
}

func TestSearchAPIDocs(t *testing.T) {
	client, calls := serveSpec(t, fixtureSpec)
	cs := connect(t, client)

	var out struct {
		Operations []struct {
			OperationID  string `json:"operation_id"`
			WebSocket    bool   `json:"websocket"`
			Experimental bool   `json:"experimental"`
		} `json:"operations"`
		Total int `json:"total"`
	}
	text, isErr := call(t, cs, "search_api_docs", map[string]any{"query": "motion group"}, &out)
	if isErr || out.Total != 2 || out.Operations[0].OperationID != "getCurrentMotionGroupState" {
		t.Fatalf("expected the state operation first of 2 hits, got %s %+v", text, out)
	}
	if !out.Operations[1].WebSocket {
		t.Errorf("executeJogging must be marked as WebSocket: %+v", out.Operations[1])
	}

	call(t, cs, "search_api_docs", map[string]any{"query": "programs"}, &out)
	if out.Total != 1 || !out.Operations[0].Experimental {
		t.Errorf("experimental flag: %+v", out)
	}
	call(t, cs, "search_api_docs", map[string]any{"query": "motion", "tag": "jogging"}, &out)
	if out.Total != 1 || out.Operations[0].OperationID != "executeJogging" {
		t.Errorf("tag filter: %+v", out)
	}
	call(t, cs, "search_api_docs", map[string]any{"query": "motion", "limit": 1}, &out)
	if out.Total != 2 || len(out.Operations) != 1 {
		t.Errorf("pagination: %+v", out)
	}
	if text, _ := call(t, cs, "search_api_docs", map[string]any{"query": "teleport"}, &out); out.Total != 0 || !strings.Contains(text, "no operation matches") {
		t.Errorf("no hit: %s", text)
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("the spec must be fetched once per process, got %d fetches", n)
	}
	if text, isErr := call(t, cs, "search_api_docs", map[string]any{"query": " "}, nil); !isErr || !strings.HasPrefix(text, "INVALID_ARGUMENT:") {
		t.Errorf("empty query: %s", text)
	}
}

func TestDescribeAPIOperation(t *testing.T) {
	client, _ := serveSpec(t, fixtureSpec)
	cs := connect(t, client)

	var out map[string]any
	text, isErr := call(t, cs, "describe_api_operation", map[string]any{"operation_id": "getcurrentmotiongroupstate"}, &out)
	if isErr {
		t.Fatal(text)
	}
	raw, _ := json.Marshal(out)
	for _, want := range []string{`"name":"cell"`, `"in":"path"`, `"name":"response_coordinate_system"`,
		`"status":"200"`, `"schema_name":"MotionGroupState"`, `"schema_name":"Joints"`, `"scope":"can_operate_controllers"`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("missing %s in %s", want, raw)
		}
	}
	if strings.Contains(string(raw), "example") {
		t.Errorf("examples are noise and must be dropped: %s", raw)
	}

	// a self-referencing schema stops at the cycle
	text, isErr = call(t, cs, "describe_api_operation", map[string]any{"operation_id": "executeJogging", "depth": 6}, &out)
	raw, _ = json.Marshal(out)
	if isErr || !strings.Contains(string(raw), `"child":{"$ref":"Node"}`) || out["websocket"] != true {
		t.Errorf("cycle: %s %s", text, raw)
	}

	if text, isErr := call(t, cs, "describe_api_operation", map[string]any{"operation_id": "teleport"}, nil); !isErr || !strings.HasPrefix(text, "NOT_FOUND:") {
		t.Errorf("unknown id: %s", text)
	}
}

func TestSpecUnavailable(t *testing.T) {
	client, calls := serveSpec(t, "{not yaml: [")
	cs := connect(t, client)
	for i := 0; i < 2; i++ {
		if text, isErr := call(t, cs, "search_api_docs", map[string]any{"query": "x"}, nil); !isErr || !strings.HasPrefix(text, "INTERNAL:") {
			t.Errorf("broken spec: %s", text)
		}
	}
	if calls.Load() != 2 {
		t.Errorf("a failed load must be retried, got %d fetches", calls.Load())
	}
}

func TestLiveSearchAndDescribe(t *testing.T) {
	cs := connect(t, livetest.Client(t))
	var found struct {
		Operations []struct {
			OperationID string `json:"operation_id"`
		} `json:"operations"`
		Total int `json:"total"`
	}
	text, isErr := call(t, cs, "search_api_docs", map[string]any{"query": "motion group state"}, &found)
	if isErr || found.Total == 0 {
		t.Fatalf("search: %s", text)
	}
	t.Log(text)
	for _, id := range []string{"executeTrajectory", "planTrajectory", "addRobotController", "getMotionGroupDescription"} {
		var out map[string]any
		text, isErr := call(t, cs, "describe_api_operation", map[string]any{"operation_id": id}, &out)
		raw, _ := json.Marshal(out)
		if isErr {
			t.Fatalf("%s: %s", id, text)
		}
		if len(raw) > 26_000 {
			t.Errorf("%s: %d bytes, the cap is not applied", id, len(raw))
		}
		t.Logf("%s: %d bytes; %s", id, len(raw), text)
	}
}
