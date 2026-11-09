package virtualcontrollers_test

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
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/novaapi"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/tool"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/toolsets/virtualcontrollers"
)

// fakeInstance answers the virtual-controller API for one controller of the given kind
// and records every write.
type fakeInstance struct {
	kind   string
	mu     sync.Mutex
	writes []string
	bodies []string
}

func (f *fakeInstance) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	path := r.URL.Path
	if r.Method != http.MethodGet {
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.writes = append(f.writes, r.Method+" "+path+"?"+r.URL.RawQuery)
		f.bodies = append(f.bodies, string(body))
		f.mu.Unlock()
		return
	}
	base := "/cells/cell/virtual-controllers/vc"
	switch path {
	case "/cells/cell/controllers/vc":
		_, _ = w.Write([]byte(`{"name":"vc","configuration":{"kind":"` + f.kind + `","controller_ip":"10.1.2.3"}}`))
	case base + "/emergency-stop":
		_, _ = w.Write([]byte(`{"active":true}`))
	case base + "/operationmode":
		_, _ = w.Write([]byte(`{"mode":"OPERATION_MODE_AUTO"}`))
	case base + "/behavior":
		_, _ = w.Write([]byte(`"BEHAVIOR_AUTOMATIC"`))
	case base + "/cycle-time":
		_, _ = w.Write([]byte(`{"cycle_time_ms":8}`))
	case base + "/motion-groups":
		_, _ = w.Write([]byte(`[{"motion_group":"0@vc","name":"UR5e","dof":6}]`))
	case base + "/motion-groups/0@vc/state":
		_, _ = w.Write([]byte(`{"positions":[0,1,2,3,4,5],"torques":[9,9,9,9,9,9]}`))
	case base + "/motion-groups/0@vc/mounting":
		_, _ = w.Write([]byte(`{"coordinate_system":"base","position":[0,0,0],"orientation":[0,0,0]}`))
	case base + "/motion-groups/0@vc/tcps":
		_, _ = w.Write([]byte(`[{"id":"flange","name":"Flange","position":[0,0,0],"payload":{"mass":3}}]`))
	case base + "/coordinate-systems":
		_, _ = w.Write([]byte(`[{"coordinate_system":"world"},{"coordinate_system":"table","reference_coordinate_system":"world","position":[500,0,0]}]`))
	case base + "/safety-zones":
		_, _ = w.Write([]byte(`{"safety_zones":[{"id":1,"name":"wall","geometry":{"id":"g","inverted":false,"plane":{"x":1}}},{"id":2,"name":"ball","restricted":true,"geometry":{"id":"s","inverted":false,"sphere":{"center":[1,2,3],"radius":50}}}]}`))
	case base + "/ios/description":
		all := []map[string]string{
			{"io": "DI0", "name": "digital in 0", "direction": "IO_TYPE_INPUT", "value_type": "IO_VALUE_BOOLEAN"},
			{"io": "DO0", "name": "digital out 0", "direction": "IO_TYPE_OUTPUT", "value_type": "IO_VALUE_BOOLEAN"},
			{"io": "AO0", "name": "analog out 0", "direction": "IO_TYPE_OUTPUT", "value_type": "IO_VALUE_ANALOG_FLOAT"},
			{"io": "IO0", "name": "int out", "direction": "IO_TYPE_OUTPUT", "value_type": "IO_VALUE_ANALOG_INTEGER"},
		}
		if wanted := r.URL.Query()["ios"]; len(wanted) > 0 {
			var filtered []map[string]string
			for _, d := range all {
				for _, w := range wanted {
					if d["io"] == w {
						filtered = append(filtered, d)
					}
				}
			}
			all = filtered
		}
		_ = json.NewEncoder(w).Encode(all)
	case base + "/ios/values":
		_, _ = w.Write([]byte(`[{"io":"DO0","value":true,"value_type":"boolean"},{"io":"AO0","value":1.5,"value_type":"float"}]`))
	default:
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{}`))
	}
}

func connect(t *testing.T, c *novaapi.Client, readOnly bool) *sdk.ClientSession {
	t.Helper()
	server := sdk.NewServer(&sdk.Implementation{Name: "test", Version: "test"}, nil)
	for _, reg := range virtualcontrollers.Tools(tool.Env{Client: c}) {
		if reg.Toolset != tool.VirtualControllers {
			t.Fatalf("tool registered in toolset %q", reg.Toolset)
		}
		if readOnly && !reg.ReadOnly {
			continue
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

func setup(t *testing.T, kind string) (*fakeInstance, *sdk.ClientSession) {
	t.Helper()
	f := &fakeInstance{kind: kind}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return f, connect(t, novaapi.NewClient(srv.URL, nil, nil, nil), false)
}

func call(t *testing.T, cs *sdk.ClientSession, name string, args map[string]any) (string, *sdk.CallToolResult) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return res.Content[0].(*sdk.TextContent).Text, res
}

func structured(t *testing.T, res *sdk.CallToolResult) string {
	t.Helper()
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

var vc = map[string]any{"cell": "cell", "controller": "vc"}

func args(extra map[string]any) map[string]any {
	out := map[string]any{"cell": "cell", "controller": "vc"}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

// every call of every tool, valid for the fake instance
var calls = map[string]map[string]any{
	"get_virtual_controller_status":    vc,
	"set_virtual_emergency_stop":       args(map[string]any{"active": true}),
	"set_virtual_operation_mode":       args(map[string]any{"mode": "manual_t1"}),
	"set_virtual_behavior":             args(map[string]any{"behavior": "automatic"}),
	"get_virtual_motion_group":         args(map[string]any{"motion_group": "0@vc"}),
	"set_virtual_joint_position":       args(map[string]any{"motion_group": "0@vc", "joint_positions": []float64{0, 0, 0, 0, 0, 0}}),
	"set_virtual_mounting":             args(map[string]any{"motion_group": "0@vc", "coordinate_system": "base", "position": []float64{0, 0, 100}}),
	"add_virtual_tcp":                  args(map[string]any{"motion_group": "0@vc", "tcp": "gripper", "position": []float64{0, 0, 150}}),
	"delete_virtual_tcp":               args(map[string]any{"motion_group": "0@vc", "tcp": "gripper"}),
	"add_virtual_motion_group":         args(map[string]any{"motion_group": "1@vc", "motion_group_model": "UniversalRobots_UR5e"}),
	"delete_virtual_motion_group":      args(map[string]any{"motion_group": "1@vc"}),
	"list_virtual_coordinate_systems":  vc,
	"add_virtual_coordinate_system":    args(map[string]any{"coordinate_system": "table", "position": []float64{500, 0, 0}}),
	"delete_virtual_coordinate_system": args(map[string]any{"coordinate_system": "table"}),
	"list_virtual_safety_zones":        vc,
	"add_virtual_safety_zone":          args(map[string]any{"id": 3, "name": "ball", "shape": "sphere", "center": []float64{0, 0, 0}, "radius": 10.0}),
	"delete_virtual_safety_zone":       args(map[string]any{"id": 3}),
	"list_virtual_ios":                 vc,
	"set_virtual_io_values":            args(map[string]any{"values": []map[string]any{{"io": "DO0", "value": "false"}}}),
}

func TestEveryToolRefusesPhysicalControllers(t *testing.T) {
	f, cs := setup(t, "KukaController")
	tools, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) != len(calls) {
		t.Fatalf("test covers %d tools, package has %d", len(calls), len(tools.Tools))
	}
	for _, tl := range tools.Tools {
		text, res := call(t, cs, tl.Name, calls[tl.Name])
		if !res.IsError || !strings.HasPrefix(text, novaapi.CodeApprovalRequired+":") {
			t.Errorf("%s on a physical controller: expected APPROVAL_REQUIRED, got %q", tl.Name, text)
		}
		if strings.Contains(text, "10.1.2.3") {
			t.Errorf("%s leaked the controller address: %q", tl.Name, text)
		}
	}
	if len(f.writes) != 0 {
		t.Errorf("no write may reach a physical controller, got %v", f.writes)
	}
}

func TestEveryToolWorksOnVirtualControllers(t *testing.T) {
	_, cs := setup(t, "VirtualController")
	for name, a := range calls {
		if text, res := call(t, cs, name, a); res.IsError || !strings.HasPrefix(text, "OK:") {
			t.Errorf("%s: expected OK, got %q", name, text)
		}
	}
}

func TestAnnotationsAndReadOnly(t *testing.T) {
	f := &fakeInstance{kind: "VirtualController"}
	srv := httptest.NewServer(f)
	defer srv.Close()
	ro := connect(t, novaapi.NewClient(srv.URL, nil, nil, nil), true)
	res, err := ro.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tl := range res.Tools {
		names = append(names, tl.Name)
		if !tl.Annotations.ReadOnlyHint || *tl.Annotations.DestructiveHint {
			t.Errorf("%s survived --read-only but is not annotated read-only", tl.Name)
		}
	}
	want := "get_virtual_controller_status get_virtual_motion_group list_virtual_coordinate_systems list_virtual_ios list_virtual_safety_zones"
	if strings.Join(names, " ") != want {
		t.Errorf("read-only tools: got %v, want %s", names, want)
	}
	all := connect(t, novaapi.NewClient(srv.URL, nil, nil, nil), false)
	res, _ = all.ListTools(context.Background(), nil)
	for _, tl := range res.Tools {
		destructive := strings.HasPrefix(tl.Name, "delete_")
		if *tl.Annotations.DestructiveHint != destructive {
			t.Errorf("%s: destructiveHint=%v", tl.Name, *tl.Annotations.DestructiveHint)
		}
	}
}

func TestStatusAndMotionGroupProjection(t *testing.T) {
	_, cs := setup(t, "VirtualController")
	text, res := call(t, cs, "get_virtual_controller_status", vc)
	if !strings.Contains(text, "emergency_stop=true") || !strings.Contains(structured(t, res), `"motion_group":"0@vc"`) {
		t.Errorf("status: %q %s", text, structured(t, res))
	}
	_, res = call(t, cs, "get_virtual_motion_group", calls["get_virtual_motion_group"])
	out := structured(t, res)
	if !strings.Contains(out, `"joint_positions":[0,1,2,3,4,5]`) || !strings.Contains(out, `"tcp":"flange"`) {
		t.Errorf("motion group: %s", out)
	}
	if strings.Contains(out, "torques") || strings.Contains(out, "payload") {
		t.Errorf("motion group leaked unprojected fields: %s", out)
	}
}

func TestWritesSendWhatTheAPIExpects(t *testing.T) {
	f, cs := setup(t, "VirtualController")
	call(t, cs, "set_virtual_operation_mode", calls["set_virtual_operation_mode"])
	call(t, cs, "set_virtual_behavior", calls["set_virtual_behavior"])
	call(t, cs, "set_virtual_io_values", args(map[string]any{"values": []map[string]any{
		{"io": "DO0", "value": "true"}, {"io": "AO0", "value": "2.5"}, {"io": "IO0", "value": "7"},
	}}))
	call(t, cs, "add_virtual_motion_group", args(map[string]any{"motion_group": "1@vc", "motion_group_model": "M", "initial_joint_position": []float64{0, 1}}))
	got := strings.Join(f.writes, "\n") + "\n" + strings.Join(f.bodies, "\n")
	for _, want := range []string{
		"mode=OPERATION_MODE_MANUAL_T1", "behavior=BEHAVIOR_AUTOMATIC",
		`{"io":"DO0","value":true,"value_type":"boolean"}`, `{"io":"AO0","value":2.5,"value_type":"float"}`,
		`{"io":"IO0","value":"7","value_type":"integer"}`, `"initial_joint_position":"[0,1]"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("expected %s in the requests:\n%s", want, got)
		}
	}
}

func TestArgumentValidation(t *testing.T) {
	f, cs := setup(t, "VirtualController")
	for name, a := range map[string]map[string]any{
		"set_virtual_operation_mode": args(map[string]any{"mode": "turbo"}),
		"set_virtual_joint_position": args(map[string]any{"motion_group": "0@vc", "joint_positions": []float64{1, 2}}),
		"add_virtual_tcp":            args(map[string]any{"motion_group": "0@vc", "tcp": "x", "position": []float64{1}}),
		"add_virtual_safety_zone":    args(map[string]any{"id": 1, "name": "z", "shape": "box", "radius": 1.0}),
		"set_virtual_io_values":      args(map[string]any{"values": []map[string]any{{"io": "NOPE", "value": "true"}}}),
		"get_virtual_motion_group":   args(map[string]any{"motion_group": " "}),
	} {
		if text, res := call(t, cs, name, a); !res.IsError || !strings.HasPrefix(text, novaapi.CodeInvalidArgument+":") {
			t.Errorf("%s: expected INVALID_ARGUMENT, got %q", name, text)
		}
	}
	if text, res := call(t, cs, "set_virtual_io_values", args(map[string]any{"values": []map[string]any{{"io": "DO0", "value": "maybe"}}})); !res.IsError {
		t.Errorf("a non-boolean value for a boolean I/O must fail, got %q", text)
	}
	if text, res := call(t, cs, "get_virtual_controller_status", map[string]any{"cell": "cell", "controller": ""}); !res.IsError || !strings.Contains(text, "'controller' is required") {
		t.Errorf("missing controller: %q", text)
	}
	if len(f.writes) != 0 {
		t.Errorf("invalid arguments must not write, got %v", f.writes)
	}
}

func TestListsAreFilteredAndPaginated(t *testing.T) {
	_, cs := setup(t, "VirtualController")
	text, res := call(t, cs, "list_virtual_ios", args(map[string]any{"direction": "output", "limit": 2}))
	out := structured(t, res)
	if !strings.Contains(text, "total=3") || !strings.Contains(text, "next_offset=2") || strings.Contains(out, "DI0") {
		t.Errorf("ios page: %q %s", text, out)
	}
	if !strings.Contains(out, `"io":"AO0"`) || !strings.Contains(out, `"value":1.5`) {
		t.Errorf("ios values: %s", out)
	}
	_, res = call(t, cs, "list_virtual_safety_zones", vc)
	out = structured(t, res)
	if !strings.Contains(out, `"shape":"plane"`) || !strings.Contains(out, `"radius":50`) {
		t.Errorf("safety zones: %s", out)
	}
}

func TestNotFound(t *testing.T) {
	_, cs := setup(t, "VirtualController")
	text, _ := call(t, cs, "get_virtual_motion_group", args(map[string]any{"motion_group": "9@vc"}))
	if text != "NOT_FOUND: get_virtual_motion_group: motion group '9@vc' not found on virtual controller 'vc' in cell 'cell'" {
		t.Errorf("unexpected: %q", text)
	}
	text, _ = call(t, cs, "get_virtual_controller_status", map[string]any{"cell": "cell", "controller": "other"})
	if !strings.HasPrefix(text, "NOT_FOUND:") {
		t.Errorf("unknown controller: %q", text)
	}
}
