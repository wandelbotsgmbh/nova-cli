package models_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/internal/livetest"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/novaapi"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/tool"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/toolsets/models"
)

func connect(t *testing.T, c *novaapi.Client) *sdk.ClientSession {
	t.Helper()
	server := sdk.NewServer(&sdk.Implementation{Name: "test", Version: "test"}, nil)
	for _, reg := range models.Tools(tool.Env{Client: c}) {
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

func serve(t *testing.T, answers map[string]string) *novaapi.Client {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		answer, ok := answers[r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(answer))
	}))
	t.Cleanup(server.Close)
	return novaapi.NewClient(server.URL, nil, nil, nil)
}

func call(t *testing.T, cs *sdk.ClientSession, name string, args map[string]any) (*sdk.CallToolResult, string, string) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	raw, _ := json.Marshal(res.StructuredContent)
	return res, res.Content[0].(*sdk.TextContent).Text, string(raw)
}

func TestList_FilterAndNames(t *testing.T) {
	c := serve(t, map[string]string{
		"/motion-group-models":                      `["UniversalRobots_UR5e","ABB_1010_037_15","UniversalRobots_UR10e"]`,
		"/experimental/motion-group-models/catalog": `[{"motion_group_name":"ABB_1010_037_15","readable_name":"IRB 1010 0.37/1.5"}]`,
	})
	cs := connect(t, c)
	res, text, got := call(t, cs, "list_motion_group_models", map[string]any{"query": "irb"})
	if res.IsError || !strings.Contains(text, "total=1") || !strings.Contains(got, `"name":"IRB 1010 0.37/1.5"`) {
		t.Fatalf("unexpected %q %s", text, got)
	}
	_, text, got = call(t, cs, "list_motion_group_models", map[string]any{"limit": 1})
	if !strings.Contains(got, `"model":"ABB_1010_037_15"`) || !strings.Contains(text, "next_offset=1") {
		t.Errorf("expected sorted first page with a next hint, got %q %s", text, got)
	}
}

func TestList_WithoutCatalog(t *testing.T) {
	c := serve(t, map[string]string{"/motion-group-models": `["B","A"]`})
	res, _, got := call(t, connect(t, c), "list_motion_group_models", map[string]any{})
	if res.IsError || got != `{"models":[{"model":"A"},{"model":"B"}],"more":false,"offset":0,"returned":2,"total":2}` {
		t.Errorf("unexpected %s", got)
	}
}

func TestDescribe(t *testing.T) {
	c := serve(t, map[string]string{
		"/motion-group-models/UR5e/kinematic":               `{"dh_parameters":[{"a":0,"type":"REVOLUTE_JOINT"},{"a":1,"type":"PRISMATIC_JOINT"}],"inverse_solver":"Universalrobots"}`,
		"/experimental/motion-group-models/UR5e/homejoints": `[0.0,-1.57079]`,
		"/experimental/motion-group-models/UR5e/limit":      `{"auto_limits":{"joints":[{"position":{"lower_limit":-6.283185,"upper_limit":6.283185},"velocity":3.13215303,"torque":null}],"tcp":{"velocity":1000.123}}}`,
	})
	res, text, got := call(t, connect(t, c), "describe_motion_group_model", map[string]any{"model": "UR5e"})
	if res.IsError {
		t.Fatalf("unexpected %q", text)
	}
	for _, want := range []string{`"joint_count":2`, `"joint_types":["revolute","prismatic"]`, `"home_joints":[0,-1.5708]`, `"upper":6.2832`, `"tcp_velocity_limit":1000.12`} {
		if !strings.Contains(got, want) {
			t.Errorf("expected %s in %s", want, got)
		}
	}
	if strings.Contains(got, "dh_parameters") || strings.Contains(got, "torque") {
		t.Errorf("raw model data leaked: %s", got)
	}
}

func TestDescribe_ExperimentalMissingAndNotFound(t *testing.T) {
	c := serve(t, map[string]string{"/motion-group-models/UR5e/kinematic": `{"dh_parameters":[{}]}`})
	cs := connect(t, c)
	if res, text, _ := call(t, cs, "describe_motion_group_model", map[string]any{"model": "UR5e"}); res.IsError {
		t.Errorf("missing experimental endpoints must not fail, got %q", text)
	}
	if res, text, _ := call(t, cs, "describe_motion_group_model", map[string]any{"model": "nope"}); !res.IsError || !strings.HasPrefix(text, "NOT_FOUND:") {
		t.Errorf("expected NOT_FOUND, got %q", text)
	}
}

func TestLive(t *testing.T) {
	cs := connect(t, livetest.Client(t))
	res, text, _ := call(t, cs, "list_motion_group_models", map[string]any{"query": "ur5e"})
	t.Log(text)
	if res.IsError {
		t.Fatal(text)
	}
	res, text, got := call(t, cs, "describe_motion_group_model", map[string]any{"model": "UniversalRobots_UR5e"})
	t.Logf("%s %s", text, got)
	if res.IsError || !strings.Contains(got, `"home_joints"`) {
		t.Errorf("unexpected %q %s", text, got)
	}
}
