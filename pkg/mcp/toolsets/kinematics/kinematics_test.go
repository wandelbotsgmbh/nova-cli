package kinematics_test

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
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/toolsets/kinematics"
)

func connect(t *testing.T, c *novaapi.Client) *sdk.ClientSession {
	t.Helper()
	server := sdk.NewServer(&sdk.Implementation{Name: "test", Version: "test"}, nil)
	for _, reg := range kinematics.Tools(tool.Env{Client: c}) {
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

type fake struct {
	answers map[string]string
	status  map[string]int
	bodies  map[string]string
}

func serve(t *testing.T, f *fake) *novaapi.Client {
	f.bodies = map[string]string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.bodies[r.URL.Path] = string(body)
		answer, ok := f.answers[r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if s := f.status[r.URL.Path]; s != 0 {
			w.WriteHeader(s)
		}
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

const description = `{"motion_group_model":"UniversalRobots_UR5e","mounting":{"position":[100,0,0],"orientation":[0,0,0]},
"tcps":{"gripper":{"name":"Gripper","pose":{"position":[0,0,150],"orientation":[0,0,0]}}}}`

func TestForward_FromMotionGroup(t *testing.T) {
	f := &fake{answers: map[string]string{
		"/cells/cell/controllers/sim/motion-groups/0@sim/description": description,
		"/cells/cell/kinematic/forward":                               `{"tcp_poses":[{"position":[491.9004568,-133.2996,487.896],"orientation":[-3.14158903,-0.0000056,0.000017]}]}`,
	}}
	res, text, got := call(t, connect(t, serve(t, f)), "forward_kinematics", map[string]any{
		"cell": "cell", "controller": "sim", "motion_group": "0@sim", "tcp": "gripper",
		"joint_positions": [][]float64{{0, -1.57, -1.57, -1.57, 1.57, -1.57}},
	})
	if res.IsError {
		t.Fatalf("unexpected %q", text)
	}
	if !strings.Contains(got, `"position":[491.9,-133.3,487.9]`) || !strings.Contains(got, `"orientation":[-3.1416,0,0]`) {
		t.Errorf("unexpected %s", got)
	}
	body := f.bodies["/cells/cell/kinematic/forward"]
	for _, want := range []string{`"motion_group_model":"UniversalRobots_UR5e"`, `"tcp_offset":{"position":[0,0,150]`, `"mounting":{"position":[100,0,0]`} {
		if !strings.Contains(body, want) {
			t.Errorf("expected %s in request %s", want, body)
		}
	}
}

func TestForward_Validation(t *testing.T) {
	cs := connect(t, serve(t, &fake{answers: map[string]string{
		"/cells/cell/controllers/sim/motion-groups/0@sim/description": description,
	}}))
	for name, args := range map[string]map[string]any{
		"no target":     {"cell": "cell", "joint_positions": [][]float64{{0}}},
		"both targets":  {"cell": "cell", "motion_group_model": "X", "controller": "sim", "motion_group": "0@sim", "joint_positions": [][]float64{{0}}},
		"tcp w/o group": {"cell": "cell", "motion_group_model": "X", "tcp": "gripper", "joint_positions": [][]float64{{0}}},
		"unknown tcp":   {"cell": "cell", "controller": "sim", "motion_group": "0@sim", "tcp": "nope", "joint_positions": [][]float64{{0}}},
		"empty":         {"cell": "cell", "motion_group_model": "X", "joint_positions": [][]float64{}},
		"bad mounting":  {"cell": "cell", "motion_group_model": "X", "mounting": map[string]any{"position": []float64{1}, "orientation": []float64{0, 0, 0}}, "joint_positions": [][]float64{{0}}},
	} {
		if res, text, _ := call(t, cs, "forward_kinematics", args); !res.IsError || !strings.HasPrefix(text, "INVALID_ARGUMENT:") {
			t.Errorf("%s: expected INVALID_ARGUMENT, got %q", name, text)
		}
	}
}

func TestForward_422(t *testing.T) {
	f := &fake{
		answers: map[string]string{"/cells/cell/kinematic/forward": `{"detail":[{"msg":"invalid dimension"}]}`},
		status:  map[string]int{"/cells/cell/kinematic/forward": http.StatusUnprocessableEntity},
	}
	res, text, _ := call(t, connect(t, serve(t, f)), "forward_kinematics", map[string]any{
		"cell": "cell", "motion_group_model": "X", "joint_positions": [][]float64{{0, 0, 0}}})
	if !res.IsError || !strings.HasPrefix(text, "INVALID_ARGUMENT:") || !strings.Contains(text, "number of joints") {
		t.Errorf("expected INVALID_ARGUMENT with a hint, got %q", text)
	}
}

func TestInverse_CapsSolutions(t *testing.T) {
	f := &fake{answers: map[string]string{
		"/cells/cell/kinematic/inverse": `{"joints":[[[1.123456,2],[3,4],[5,6]],[]]}`,
	}}
	res, text, got := call(t, connect(t, serve(t, f)), "inverse_kinematics", map[string]any{
		"cell": "cell", "motion_group_model": "X", "max_solutions": 2,
		"tcp_poses": []any{
			map[string]any{"position": []float64{1, 2, 3}, "orientation": []float64{0, 0, 0}},
			map[string]any{"position": []float64{9000, 0, 0}, "orientation": []float64{0, 0, 0}},
		},
	})
	if res.IsError || !strings.Contains(text, "reachable=1") {
		t.Fatalf("unexpected %q", text)
	}
	want := `"poses":[{"solutions":[[1.1235,2],[3,4]],"total":3},{"solutions":[],"total":0}]`
	if !strings.Contains(got, want) {
		t.Errorf("expected %s in %s", want, got)
	}
}

func TestLive(t *testing.T) {
	cs := connect(t, livetest.Client(t))
	cell, ctrl := livetest.Cell(), livetest.VirtualController()
	home := []float64{0, -1.5708, -1.5708, -1.5708, 1.5708, -1.5708}
	res, text, got := call(t, cs, "forward_kinematics", map[string]any{
		"cell": cell, "controller": ctrl, "motion_group": "0@" + ctrl, "joint_positions": [][]float64{home}})
	t.Logf("%s %s", text, got)
	if res.IsError {
		t.Fatal(text)
	}
	var fk struct {
		TCPPoses []struct {
			Position    []float64 `json:"position"`
			Orientation []float64 `json:"orientation"`
		} `json:"tcp_poses"`
	}
	_ = json.Unmarshal([]byte(got), &fk)
	res, text, got = call(t, cs, "inverse_kinematics", map[string]any{
		"cell": cell, "motion_group_model": "UniversalRobots_UR5e", "reference_joint_position": home,
		"tcp_poses": []any{fk.TCPPoses[0], map[string]any{"position": []float64{5000, 0, 0}, "orientation": []float64{0, 0, 0}}}})
	t.Logf("%s %.400s", text, got)
	if res.IsError || !strings.Contains(text, "reachable=1") {
		t.Errorf("expected the FK pose to be reachable and the far pose not, got %q", text)
	}
	if !strings.Contains(got, "[0,-1.5708,-1.5708,-1.5708,1.5708,-1.5708]") {
		t.Errorf("expected the home position as closest solution: %s", got)
	}
	res, text, _ = call(t, cs, "forward_kinematics", map[string]any{
		"cell": cell, "motion_group_model": "UniversalRobots_UR5e", "joint_positions": [][]float64{{0, 0, 0}}})
	t.Log(text)
	if !res.IsError || !strings.HasPrefix(text, "INVALID_ARGUMENT:") {
		t.Errorf("expected INVALID_ARGUMENT for a wrong joint count, got %q", text)
	}
}
