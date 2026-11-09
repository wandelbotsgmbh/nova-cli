package mcp_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	novamcp "github.com/wandelbotsgmbh/nova-cli/pkg/mcp"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/novaapi"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/tool"
)

// connect builds a server registered with the given toolsets/readOnly over
// an in-memory transport and returns a connected client session, exercising
// the real MCP protocol layer (schema validation, annotations, JSON-RPC)
// instead of calling tool functions directly.
func connect(t *testing.T, api novaapi.API, toolsets []string, readOnly bool) *sdk.ClientSession {
	t.Helper()
	server := sdk.NewServer(&sdk.Implementation{Name: "test", Version: "test"}, nil)
	novamcp.Register(server, api, tool.Env{Client: novaapi.NewClient("http://nova.invalid/api/v2", nil, nil, nil)}, toolsets, readOnly)

	clientTransport, serverTransport := sdk.NewInMemoryTransports()
	ctx := context.Background()
	if _, err := server.Connect(ctx, serverTransport, nil); err != nil {
		t.Fatalf("server.Connect: %v", err)
	}
	client := sdk.NewClient(&sdk.Implementation{Name: "test-client", Version: "test"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client.Connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func toolNames(t *testing.T, result *sdk.ListToolsResult) []string {
	t.Helper()
	names := make([]string, len(result.Tools))
	for i, tool := range result.Tools {
		names[i] = tool.Name
	}
	return names
}

func TestRegister_PortedToolsArePresent(t *testing.T) {
	session := connect(t, &fakeAPI{}, novamcp.AllToolsets(), false)
	result, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	want := []string{
		"get_context", "list_cells", "describe_cell", "list_controllers",
		"describe_controller", "list_robot_configurations", "create_virtual_controller",
		"delete_controller",
	}
	// the tools ported from the instance MCP service; the toolset packages add more
	got := toolNames(t, result)
	for _, name := range want {
		found := false
		for _, g := range got {
			if g == name {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected tool %q to be registered, got %v", name, got)
		}
	}
}

func TestRegister_ToolsetFiltering(t *testing.T) {
	session := connect(t, &fakeAPI{}, []string{novamcp.ToolsetCells}, false)
	result, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	got := toolNames(t, result)
	want := []string{"check_cell_update", "delete_cell", "deploy_cell", "describe_cell", "get_cell_status",
		"list_cells", "set_cell_status", "update_cell_version"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("toolset %q: got %v, want exactly %v", novamcp.ToolsetCells, got, want)
	}
}

func TestRegister_ReadOnlyStripsWriteTools(t *testing.T) {
	session := connect(t, &fakeAPI{}, novamcp.AllToolsets(), true)
	result, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	for _, tool := range result.Tools {
		if tool.Name == "create_virtual_controller" || tool.Name == "delete_controller" {
			t.Errorf("read-only mode must strip %q, but it was registered", tool.Name)
		}
		if tool.Annotations != nil && !tool.Annotations.ReadOnlyHint && tool.Name != "" {
			// Every remaining tool under --read-only must itself be read-only.
			t.Errorf("tool %q survived read-only filtering but is not annotated read-only", tool.Name)
		}
	}
}

// TestListTools_DeterministicOrder pins tools/list ordering (ADR-0014: a
// SHOULD since protocol 2026-07-28, for prompt caching). Two independent
// servers built from the same registration must list tools identically.
func TestListTools_DeterministicOrder(t *testing.T) {
	sessionA := connect(t, &fakeAPI{}, novamcp.AllToolsets(), false)
	sessionB := connect(t, &fakeAPI{}, novamcp.AllToolsets(), false)

	resultA, err := sessionA.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools A: %v", err)
	}
	resultB, err := sessionB.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools B: %v", err)
	}

	namesA, namesB := toolNames(t, resultA), toolNames(t, resultB)
	if len(namesA) != len(namesB) {
		t.Fatalf("got different tool counts: %v vs %v", namesA, namesB)
	}
	for i := range namesA {
		if namesA[i] != namesB[i] {
			t.Fatalf("tools/list order is not deterministic at index %d: %q vs %q", i, namesA[i], namesB[i])
		}
	}
}

func TestDescribeController_NeverIncludesPhysicalConnectionFields(t *testing.T) {
	api := &fakeAPI{controller: novaapi.ControllerInfo{Name: "kuka-1", Manufacturer: "KukaController", Physical: true}}
	session := connect(t, api, novamcp.AllToolsets(), false)

	result, err := session.CallTool(context.Background(), &sdk.CallToolParams{
		Name:      "describe_controller",
		Arguments: map[string]any{"cell": "cell-1", "controller": "kuka-1"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %+v", result.Content)
	}

	raw, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structured content: %v", err)
	}
	for _, field := range []string{"controller_ip", "rsi_server", "egm_server", "password", "controller_port"} {
		if strings.Contains(string(raw), field) {
			t.Errorf("describe_controller leaked field %q: %s", field, raw)
		}
	}
}

func TestCreateVirtualController_RejectsUnknownPreset(t *testing.T) {
	api := &fakeAPI{configurations: []string{"universalrobots-ur5e"}}
	session := connect(t, api, novamcp.AllToolsets(), false)

	result, err := session.CallTool(context.Background(), &sdk.CallToolParams{
		Name: "create_virtual_controller",
		Arguments: map[string]any{
			"cell": "cell-1", "name": "sim-1", "manufacturer": "kuka", "type": "not-a-real-preset",
		},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected an isError result for an unknown preset, got success")
	}
	if len(api.createdSpecs) != 0 {
		t.Error("expected no controller to be created for an unknown preset")
	}
	text := result.Content[0].(*sdk.TextContent).Text
	if !strings.HasPrefix(text, novaapi.CodeInvalidArgument+":") {
		t.Errorf("expected %s code, got %q", novaapi.CodeInvalidArgument, text)
	}
}

func TestCreateVirtualController_Success(t *testing.T) {
	api := &fakeAPI{
		configurations: []string{"universalrobots-ur5e"},
		deployResult:   novaapi.DeployResult{Name: "sim-1", Status: novaapi.StatusPending},
	}
	session := connect(t, api, novamcp.AllToolsets(), false)

	result, err := session.CallTool(context.Background(), &sdk.CallToolParams{
		Name: "create_virtual_controller",
		Arguments: map[string]any{
			"cell": "cell-1", "name": "sim-1", "manufacturer": "universalrobots", "type": "universalrobots-ur5e",
		},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %+v", result.Content)
	}
	if len(api.createdSpecs) != 1 || api.createdSpecs[0].Name != "sim-1" {
		t.Errorf("expected exactly one create call for sim-1, got %+v", api.createdSpecs)
	}
	text := result.Content[0].(*sdk.TextContent).Text
	if !strings.HasPrefix(text, "OK:") || !strings.Contains(text, "status=pending") {
		t.Errorf("unexpected result line: %q", text)
	}
}

// TestDeleteController_ReadOnlyModeIsUnreachable asserts the server-side
// guard rail end to end: under --read-only, delete_controller is not merely
// hidden from tools/list, it cannot be invoked at all.
func TestDeleteController_ReadOnlyModeIsUnreachable(t *testing.T) {
	api := &fakeAPI{}
	session := connect(t, api, novamcp.AllToolsets(), true)

	_, err := session.CallTool(context.Background(), &sdk.CallToolParams{
		Name:      "delete_controller",
		Arguments: map[string]any{"cell": "cell-1", "controller": "kuka-1"},
	})
	if err == nil {
		t.Fatal("expected calling an unregistered tool under read-only mode to fail")
	}
	if len(api.deletedNames) != 0 {
		t.Error("expected delete_controller to never run under read-only mode")
	}
}

func TestAPIError_SurfacesAsIsErrorNotProtocolError(t *testing.T) {
	api := &fakeAPI{err: &novaapi.Error{Code: novaapi.CodeNotFound, Message: "cell 'missing' not found"}}
	session := connect(t, api, novamcp.AllToolsets(), false)

	result, err := session.CallTool(context.Background(), &sdk.CallToolParams{
		Name:      "list_controllers",
		Arguments: map[string]any{"cell": "missing"},
	})
	if err != nil {
		t.Fatalf("expected an API failure to surface as isError, not a protocol error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected result.IsError to be true")
	}
	text := result.Content[0].(*sdk.TextContent).Text
	if text != "NOT_FOUND: cell 'missing' not found" {
		t.Errorf("unexpected result line: %q", text)
	}
}

func TestRegister_DefaultToolsetsLeaveOptInOut(t *testing.T) {
	session := connect(t, &fakeAPI{}, novamcp.DefaultToolsets(), false)
	result, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	for _, name := range toolNames(t, result) {
		switch name {
		case "execute_trajectory", "plan_trajectory", "start_program", "put_store_item", "restore_backup", "update_nova_version":
			t.Errorf("opt-in tool %q is served by default", name)
		}
	}
}

func TestParseToolsets(t *testing.T) {
	got, err := novamcp.ParseToolsets([]string{"default", "motion", "cells"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(novamcp.DefaultToolsets())+1 || got[len(got)-1] != "motion" {
		t.Errorf("unexpected toolsets %v", got)
	}
	if all, _ := novamcp.ParseToolsets([]string{"all"}); len(all) != len(novamcp.AllToolsets()) {
		t.Errorf("all: got %v", all)
	}
	if _, err := novamcp.ParseToolsets([]string{"motoin"}); err == nil {
		t.Error("expected an error for an unknown toolset")
	}
}

func TestInstructionsWithinBudget(t *testing.T) {
	server := novamcp.NewServer(&fakeAPI{}, nil, novamcp.Options{})
	ct, st := sdk.NewInMemoryTransports()
	if _, err := server.Connect(context.Background(), st, nil); err != nil {
		t.Fatal(err)
	}
	session, err := sdk.NewClient(&sdk.Implementation{Name: "c", Version: "c"}, nil).Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	got := session.InitializeResult().Instructions
	if n := len([]rune(got)); n == 0 || n > 4096 {
		t.Errorf("instructions must be 1..4096 chars (ADR-0008), got %d", n)
	}
}
