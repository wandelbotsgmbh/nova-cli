package controllers_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/internal/livetest"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/novaapi"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/tool"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/toolsets/controllers"
)

func connect(t *testing.T, c *novaapi.Client) *sdk.ClientSession {
	t.Helper()
	server := sdk.NewServer(&sdk.Implementation{Name: "test", Version: "test"}, nil)
	for _, reg := range controllers.Tools(tool.Env{Client: c}) {
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

// fake serves canned answers per "METHOD path" and records requests.
type fake struct {
	answers  map[string]string
	requests []string
	bodies   map[string]string
}

func newFake(t *testing.T, answers map[string]string) (*fake, *novaapi.Client) {
	f := &fake{answers: answers, bodies: map[string]string{}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Method + " " + r.URL.Path
		f.requests = append(f.requests, key+"?"+r.URL.RawQuery)
		body, _ := io.ReadAll(r.Body)
		f.bodies[key] = string(body)
		answer, ok := f.answers[key]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(answer))
	}))
	t.Cleanup(server.Close)
	return f, novaapi.NewClient(server.URL, nil, nil, nil)
}

func call(t *testing.T, cs *sdk.ClientSession, name string, args map[string]any) (*sdk.CallToolResult, string) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	text := res.Content[0].(*sdk.TextContent).Text
	return res, text
}

func structured(t *testing.T, res *sdk.CallToolResult) string {
	t.Helper()
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

const virtualController = `{"name":"sim","configuration":{"kind":"VirtualController","manufacturer":"universalrobots","type":"universalrobots-ur5e"}}`
const physicalController = `{"name":"kuka","configuration":{"kind":"KukaController","controller_ip":"10.1.2.3","rsi_server":{"ip":"10.1.2.9","port":59152}}}`

const outputDescriptions = `[
 {"io":"digital_out[10]","name":"d10","group":"standard","direction":"IO_TYPE_OUTPUT","value_type":"IO_VALUE_BOOLEAN","unit":"UNIT_NONE"},
 {"io":"digital_out[2]","name":"d2","group":"standard","direction":"IO_TYPE_OUTPUT","value_type":"IO_VALUE_BOOLEAN","unit":"UNIT_NONE"},
 {"io":"analog_out[0]","name":"a0","group":"standard","direction":"IO_TYPE_OUTPUT","value_type":"IO_VALUE_ANALOG_FLOAT","unit":"UNIT_VOLT","min":{"value":0,"value_type":"float"},"max":{"value":10,"value_type":"float"}},
 {"io":"int_out[0]","name":"i0","group":"standard","direction":"IO_TYPE_OUTPUT","value_type":"IO_VALUE_ANALOG_INTEGER","unit":"UNIT_NONE"}
]`

func TestGetControllerState_Compact(t *testing.T) {
	_, c := newFake(t, map[string]string{
		"GET /cells/cell/controllers/sim/state": `{"mode":"MODE_MONITOR","last_error":[""],"controller":"sim","operation_mode":"OPERATION_MODE_AUTO",
		"safety_state":"SAFETY_STATE_NORMAL","velocity_override":100,"sequence_number":5,"motion_groups":[{"motion_group":"0@sim",
		"joint_position":[0,-1.5707999467849731],"joint_limit_reached":{"limit_reached":[false,false]},"joint_torque":[1,2],
		"tcp":"Flange","tcp_pose":{"position":[491.9004568265895,-133.29963944898824,487.896],"orientation":[-3.1415890335286,-0.0000056,0.000017]},
		"standstill":true}]}`,
	})
	res, text := call(t, connect(t, c), "get_controller_state", map[string]any{"cell": "cell", "controller": "sim"})
	if res.IsError || !strings.Contains(text, "mode=MODE_MONITOR") {
		t.Fatalf("unexpected result %q", text)
	}
	got := structured(t, res)
	for _, want := range []string{`"joint_position":[0,-1.5708]`, `"position":[491.9,-133.3,487.9]`, `"orientation":[-3.1416,0,0]`} {
		if !strings.Contains(got, want) {
			t.Errorf("expected %s in %s", want, got)
		}
	}
	for _, unwanted := range []string{"joint_torque", "sequence_number", "joint_limit_reached", `"errors"`} {
		if strings.Contains(got, unwanted) {
			t.Errorf("unexpected %s in %s", unwanted, got)
		}
	}
}

func TestDescribeMotionGroup_LeavesOutGeometry(t *testing.T) {
	_, c := newFake(t, map[string]string{
		"GET /cells/cell/controllers/sim/motion-groups/0@sim/description": `{"motion_group_model":"UniversalRobots_UR5e",
		"mounting":{"position":[0,0,0],"orientation":[0,0,0]},"tcps":{"Flange":{"name":"Default-Flange","pose":{"position":[0,0,0],"orientation":[0,0,0]}}},
		"payloads":{"FPay-0":{"name":"FPay-0","payload":1.5}},"safety_zones":{"zone1":{"shape":{"shape_type":"box","size_x":1}}},
		"cycle_time":8,"dh_parameters":[{"a":0},{"a":-425}],"operation_limits":{"auto_limits":{"joints":[
		{"position":{"lower_limit":-6.281439977927592,"upper_limit":6.281439977927592},"velocity":3.13215303}],"tcp":{"velocity":1000}}}}`,
	})
	res, text := call(t, connect(t, c), "describe_motion_group", map[string]any{"cell": "cell", "controller": "sim", "motion_group": "0@sim"})
	if res.IsError {
		t.Fatalf("unexpected error %q", text)
	}
	got := structured(t, res)
	for _, want := range []string{`"motion_group_model":"UniversalRobots_UR5e"`, `"joint_count":2`, `"safety_zones":1`, `"lower":-6.2814`, `"mass":1.5,"name":"FPay-0"`} {
		if !strings.Contains(got, want) {
			t.Errorf("expected %s in %s", want, got)
		}
	}
	if strings.Contains(got, "shape") || strings.Contains(got, "dh_parameters") {
		t.Errorf("geometry leaked: %s", got)
	}
}

func TestGetIOValues_AllFromDescriptions(t *testing.T) {
	f, c := newFake(t, map[string]string{
		"GET /cells/cell/controllers/sim/ios/description": outputDescriptions,
		"GET /cells/cell/controllers/sim/ios/values":      `[{"io":"digital_out[2]","value":true,"value_type":"boolean"}]`,
	})
	res, text := call(t, connect(t, c), "get_io_values", map[string]any{"cell": "cell", "controller": "sim", "direction": "output", "limit": 2})
	if res.IsError {
		t.Fatalf("unexpected error %q", text)
	}
	// natural order: digital_out[2] before digital_out[10]; analog_out first
	last := f.requests[len(f.requests)-1]
	if !strings.Contains(last, "ios=analog_out%5B0%5D&ios=digital_out%5B2%5D") {
		t.Errorf("expected the first page of ids in natural order, got %s", last)
	}
	if !strings.Contains(f.requests[0], "direction=IO_TYPE_OUTPUT") {
		t.Errorf("filter not passed on: %s", f.requests[0])
	}
	if !strings.Contains(text, "next_offset=2") {
		t.Errorf("expected a next page hint, got %q", text)
	}
}

func TestListIODescriptions_ValidatesFilter(t *testing.T) {
	_, c := newFake(t, nil)
	res, text := call(t, connect(t, c), "list_io_descriptions", map[string]any{"cell": "cell", "controller": "sim", "direction": "sideways"})
	if !res.IsError || !strings.HasPrefix(text, "INVALID_ARGUMENT:") {
		t.Fatalf("expected INVALID_ARGUMENT, got %q", text)
	}
}

func TestSetOutputValues_PhysicalIsRefused(t *testing.T) {
	f, c := newFake(t, map[string]string{
		"GET /cells/cell/controllers/kuka":                 physicalController,
		"GET /cells/cell/controllers/kuka/ios/description": outputDescriptions,
	})
	res, text := call(t, connect(t, c), "set_output_values", map[string]any{"cell": "cell", "controller": "kuka",
		"values": []any{map[string]any{"io": "digital_out[2]", "value": true}}})
	if !res.IsError || !strings.HasPrefix(text, "APPROVAL_REQUIRED:") {
		t.Fatalf("expected APPROVAL_REQUIRED, got %q", text)
	}
	if strings.Contains(text, "10.1.2.3") {
		t.Errorf("connection details leaked: %q", text)
	}
	for _, r := range f.requests {
		if strings.HasPrefix(r, "PUT") {
			t.Errorf("no write may reach a physical controller, got %s", r)
		}
	}
}

func TestSetOutputValues_TypedAndValidated(t *testing.T) {
	f, c := newFake(t, map[string]string{
		"GET /cells/cell/controllers/sim":                 virtualController,
		"GET /cells/cell/controllers/sim/ios/description": outputDescriptions,
		"PUT /cells/cell/controllers/sim/ios/values":      ``,
	})
	cs := connect(t, c)
	res, text := call(t, cs, "set_output_values", map[string]any{"cell": "cell", "controller": "sim", "values": []any{
		map[string]any{"io": "digital_out[2]", "value": true},
		map[string]any{"io": "analog_out[0]", "value": 2.5},
		map[string]any{"io": "int_out[0]", "value": 7},
	}})
	if res.IsError {
		t.Fatalf("unexpected error %q", text)
	}
	body := f.bodies["PUT /cells/cell/controllers/sim/ios/values"]
	for _, want := range []string{`{"io":"digital_out[2]","value_type":"boolean","value":true}`, `"value_type":"float","value":2.5`, `"value_type":"integer","value":"7"`} {
		if !strings.Contains(body, want) {
			t.Errorf("expected %s in %s", want, body)
		}
	}

	for _, bad := range []map[string]any{
		{"io": "digital_out[2]", "value": "yes"},
		{"io": "int_out[0]", "value": 1.5},
		{"io": "digital_in[0]", "value": true},
	} {
		res, text := call(t, cs, "set_output_values", map[string]any{"cell": "cell", "controller": "sim", "values": []any{bad}})
		if !res.IsError || !strings.HasPrefix(text, "INVALID_ARGUMENT:") {
			t.Errorf("%v: expected INVALID_ARGUMENT, got %q", bad, text)
		}
	}
}

func TestSetDefaultMode(t *testing.T) {
	f, c := newFake(t, map[string]string{
		"GET /cells/cell/controllers/sim":      virtualController,
		"PUT /cells/cell/controllers/sim/mode": ``,
		"GET /cells/cell/controllers/kuka":     physicalController,
	})
	cs := connect(t, c)
	if res, text := call(t, cs, "set_default_mode", map[string]any{"cell": "cell", "controller": "sim", "mode": "control"}); res.IsError {
		t.Fatalf("unexpected error %q", text)
	}
	if last := f.requests[len(f.requests)-1]; last != "PUT /cells/cell/controllers/sim/mode?mode=MODE_CONTROL" {
		t.Errorf("unexpected request %s", last)
	}
	if res, text := call(t, cs, "set_default_mode", map[string]any{"cell": "cell", "controller": "kuka", "mode": "control"}); !res.IsError || !strings.HasPrefix(text, "APPROVAL_REQUIRED:") {
		t.Errorf("expected APPROVAL_REQUIRED for a physical controller, got %q", text)
	}
	if res, text := call(t, cs, "set_default_mode", map[string]any{"cell": "cell", "controller": "sim", "mode": "fast"}); !res.IsError || !strings.HasPrefix(text, "INVALID_ARGUMENT:") {
		t.Errorf("expected INVALID_ARGUMENT, got %q", text)
	}
}

func TestDiscover_SortsAndRequiresArguments(t *testing.T) {
	_, c := newFake(t, map[string]string{
		"GET /system/network/arp-scan": `[{"ip":"192.168.1.20","mac":"b"},{"ip":"192.168.1.3","mac":"a","vendor":"KUKA"}]`,
	})
	cs := connect(t, c)
	res, text := call(t, cs, "discover_controllers_on_network", map[string]any{"interface": "eth0", "cidr": "192.168.1.0/24"})
	if res.IsError {
		t.Fatalf("unexpected error %q", text)
	}
	if got := structured(t, res); strings.Index(got, "192.168.1.3") > strings.Index(got, "192.168.1.20") {
		t.Errorf("expected numeric IP order: %s", got)
	}
	if res, text := call(t, cs, "discover_controllers_on_network", map[string]any{"interface": "eth0"}); !res.IsError || !strings.Contains(text, "cidr") {
		t.Errorf("expected a missing cidr error, got %q", text)
	}
}

func TestNotFound(t *testing.T) {
	_, c := newFake(t, nil)
	res, text := call(t, connect(t, c), "get_controller_state", map[string]any{"cell": "cell", "controller": "nope"})
	if !res.IsError || text != "NOT_FOUND: get_controller_state: controller 'nope' not found in cell 'cell'" {
		t.Errorf("unexpected %q", text)
	}
}

func TestLiveReads(t *testing.T) {
	c := livetest.Client(t)
	cs := connect(t, c)
	cell, ctrl := livetest.Cell(), livetest.VirtualController()
	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{"get_controller_state", map[string]any{"cell": cell, "controller": ctrl}},
		{"describe_motion_group", map[string]any{"cell": cell, "controller": ctrl, "motion_group": "0@" + ctrl}},
		{"list_coordinate_systems", map[string]any{"cell": cell, "controller": ctrl}},
		{"list_io_descriptions", map[string]any{"cell": cell, "controller": ctrl, "direction": "output", "value_type": "boolean"}},
		{"get_io_values", map[string]any{"cell": cell, "controller": ctrl, "group": "tool"}},
	} {
		res, text := call(t, cs, tc.name, tc.args)
		t.Logf("%s: %s %.300s", tc.name, text, structured(t, res))
		if res.IsError {
			t.Errorf("%s failed: %s", tc.name, text)
		}
	}
}

func TestLiveWritesOnVirtual(t *testing.T) {
	c := livetest.Client(t)
	cs := connect(t, c)
	cell, ctrl := livetest.Cell(), livetest.VirtualController()
	read := func() string {
		res, text := call(t, cs, "get_io_values", map[string]any{"cell": cell, "controller": ctrl, "ios": []string{"digital_out[0]"}})
		if res.IsError {
			t.Fatalf("get_io_values: %s", text)
		}
		return structured(t, res)
	}
	before := read()
	original := strings.Contains(before, `"value":true`)
	set := func(v bool) {
		res, text := call(t, cs, "set_output_values", map[string]any{"cell": cell, "controller": ctrl,
			"values": []any{map[string]any{"io": "digital_out[0]", "value": v}}})
		if res.IsError {
			t.Fatalf("set_output_values: %s", text)
		}
	}
	set(!original)
	if after := read(); strings.Contains(after, `"value":true`) == original {
		t.Errorf("output did not change: %s -> %s", before, after)
	}
	set(original)

	for _, mode := range []string{"control", "monitor"} {
		if res, text := call(t, cs, "set_default_mode", map[string]any{"cell": cell, "controller": ctrl, "mode": mode}); res.IsError {
			t.Errorf("set_default_mode %s: %s", mode, text)
		}
	}
}
