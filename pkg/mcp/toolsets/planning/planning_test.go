package planning_test

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
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/toolsets/planning"
)

func connect(t *testing.T, c *novaapi.Client) *sdk.ClientSession {
	t.Helper()
	server := sdk.NewServer(&sdk.Implementation{Name: "test", Version: "test"}, nil)
	for _, reg := range planning.Tools(tool.Env{Client: c}) {
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

// fakeInstance serves the endpoints the planning tools call.
type fakeInstance struct {
	mu        sync.Mutex
	plan      string // planner answer body
	planCode  int
	planBody  map[string]any
	added     map[string]any
	addAnswer string
	requests  []string
}

func (f *fakeInstance) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.requests = append(f.requests, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/cells/cell/controllers/ur/description":
			_, _ = io.WriteString(w, `{"connected_motion_groups":["0@ur"]}`)
		case r.URL.Path == "/cells/cell/controllers/ur/motion-groups/0@ur/description":
			_, _ = io.WriteString(w, `{"motion_group_model":"UniversalRobots_UR5e","cycle_time":8,
				"mounting":{"position":[0,0,0],"orientation":[0,0,0]},
				"tcps":{"Flange":{"name":"Default-Flange","pose":{"position":[0,0,0],"orientation":[0,0,0]}}},
				"operation_limits":{"auto_limits":{"joints":[]}},
				"safety_zones":{"secret-zone":{}}}`)
		case r.URL.Path == "/cells/cell/controllers/ur/motion-groups/0@ur/state":
			_, _ = io.WriteString(w, `{"joint_position":[0,-1.57,-1.57,-1.57,1.57,-1.57],"tcp":"Flange"}`)
		case strings.HasSuffix(r.URL.Path, "/trajectory-planning/plan-trajectory"),
			strings.HasSuffix(r.URL.Path, "/trajectory-planning/plan-collision-free"):
			_ = json.NewDecoder(r.Body).Decode(&f.planBody)
			if f.planCode != 0 {
				w.WriteHeader(f.planCode)
			}
			_, _ = io.WriteString(w, f.plan)
		case r.Method == http.MethodPost && r.URL.Path == "/cells/cell/controllers/ur/trajectories":
			_ = json.NewDecoder(r.Body).Decode(&f.added)
			answer := f.addAnswer
			if answer == "" {
				answer = `{"trajectory":"traj-1"}`
			}
			_, _ = io.WriteString(w, answer)
		case r.URL.Path == "/cells/cell/controllers/ur/trajectories":
			_, _ = io.WriteString(w, `{"trajectories":["b","a"]}`)
		case r.Method == http.MethodGet && r.URL.Path == "/cells/cell/controllers/ur/trajectories/traj-1":
			_, _ = io.WriteString(w, `{"motion_group":"0@ur","tcp":"Flange","trajectory":{
				"joint_positions":[[0,0,0,0,0,0],[0.5,0,0,0,0,0],[1,0,0,0,0,0]],"times":[0,0.5,1.25],"locations":[0,0.5,1]}}`)
		case r.Method == http.MethodDelete && r.URL.Path == "/cells/cell/controllers/ur/trajectories/traj-1":
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{}`)
		}
	}
}

func newFake(t *testing.T, f *fakeInstance) *sdk.ClientSession {
	t.Helper()
	srv := httptest.NewServer(f.handler(t))
	t.Cleanup(srv.Close)
	return connect(t, novaapi.NewClient(srv.URL, nil, nil, nil))
}

const okPlan = `{"response":{"joint_positions":[[0,-1.57,-1.57,-1.57,1.57,-1.57],[0.1,-1.57,-1.57,-1.57,1.57,-1.57]],"times":[0,0.75],"locations":[0,1]}}`

func TestPlanTrajectory_StoresAndSummarizes(t *testing.T) {
	f := &fakeInstance{plan: okPlan}
	cs := newFake(t, f)
	res, text := call(t, cs, "plan_trajectory", map[string]any{
		"cell": "cell", "controller": "ur",
		"commands": []any{map[string]any{"type": "line", "pose": map[string]any{"position": []float64{400, 0, 300}}, "tcp_velocity_limit": 100}},
	})
	if res.IsError || !strings.Contains(text, "trajectory=traj-1") || !strings.Contains(text, "duration_s=0.75") {
		t.Fatalf("unexpected result %q", text)
	}
	setup := f.planBody["motion_group_setup"].(map[string]any)
	if setup["motion_group_model"] != "UniversalRobots_UR5e" || setup["cycle_time"].(float64) != 8 || setup["tcp_offset"] == nil {
		t.Errorf("motion group setup not assembled from the description: %v", setup)
	}
	cmd := f.planBody["motion_commands"].([]any)[0].(map[string]any)
	path := cmd["path"].(map[string]any)
	if path["path_definition_name"] != "PathLine" || cmd["limits_override"] == nil {
		t.Errorf("unexpected command: %v", cmd)
	}
	if f.added["motion_group"] != "0@ur" || f.added["tcp"] != "Flange" {
		t.Errorf("unexpected addTrajectory body: %v", f.added)
	}
	raw, _ := json.Marshal(res.StructuredContent)
	if strings.Contains(string(raw), "joint_positions") || strings.Contains(string(raw), "times") {
		t.Errorf("the sample list must not be returned: %s", raw)
	}
}

func TestPlanTrajectory_PlannerFeedback(t *testing.T) {
	f := &fakeInstance{plan: `{"response":{"error_feedback":{"error_feedback_name":"FeedbackCollision",
		"collisions":[{"id_of_a":"link3","id_of_b":"table","id_of_layer":"safety"}]},"error_location_on_trajectory":1.4,
		"joint_trajectory":{"joint_positions":[[0,0,0,0,0,0]],"times":[0],"locations":[0]}}}`}
	cs := newFake(t, f)
	res, text := call(t, cs, "plan_trajectory", map[string]any{
		"cell": "cell", "controller": "ur",
		"commands": []any{map[string]any{"type": "joint_ptp", "joints": []float64{1, 0, 0, 0, 0, 0}}},
	})
	if !res.IsError || !strings.HasPrefix(text, "PLANNING_FAILED:") || !strings.Contains(text, "link3<->table") || !strings.Contains(text, "command 2") {
		t.Fatalf("unexpected result %q", text)
	}
	if f.added != nil {
		t.Error("a failed plan must not be stored")
	}
}

func TestPlanTrajectory_Validation422(t *testing.T) {
	f := &fakeInstance{planCode: 422, plan: `{"detail":[{"loc":["body","start_joint_position"],"msg":"wrong count","type":"x","input":{},
		"data":{"error_feedback_name":"ErrorInvalidJointCount","expected_joint_count":6,"provided_joint_count":5}}]}`}
	cs := newFake(t, f)
	_, text := call(t, cs, "plan_trajectory", map[string]any{
		"cell": "cell", "controller": "ur", "start_joints": []float64{0, 0, 0, 0, 0},
		"commands": []any{map[string]any{"type": "cartesian_ptp", "pose": map[string]any{"position": []float64{1, 2, 3}}}},
	})
	if !strings.HasPrefix(text, "INVALID_ARGUMENT:") || !strings.Contains(text, "ErrorInvalidJointCount: wrong count") {
		t.Fatalf("unexpected result %q", text)
	}
}

func TestPlanTrajectory_ArgumentValidation(t *testing.T) {
	f := &fakeInstance{plan: okPlan}
	cs := newFake(t, f)
	for name, args := range map[string]map[string]any{
		"no commands":   {"cell": "cell", "controller": "ur", "commands": []any{}},
		"no cell":       {"controller": "ur", "commands": []any{map[string]any{"type": "joint_ptp", "joints": []float64{0, 0, 0, 0, 0, 0}}}},
		"unknown type":  {"cell": "cell", "controller": "ur", "commands": []any{map[string]any{"type": "spline"}}},
		"joints count":  {"cell": "cell", "controller": "ur", "commands": []any{map[string]any{"type": "joint_ptp", "joints": []float64{0, 0}}}},
		"circle no via": {"cell": "cell", "controller": "ur", "commands": []any{map[string]any{"type": "circle", "pose": map[string]any{"position": []float64{1, 2, 3}}}}},
		"bad tcp":       {"cell": "cell", "controller": "ur", "tcp": "gripper", "commands": []any{map[string]any{"type": "joint_ptp", "joints": []float64{0, 0, 0, 0, 0, 0}}}},
		"pose no xyz":   {"cell": "cell", "controller": "ur", "commands": []any{map[string]any{"type": "line", "pose": map[string]any{"position": []float64{1}}}}},
	} {
		t.Run(name, func(t *testing.T) {
			res, text := call(t, cs, "plan_trajectory", args)
			// a missing required argument is already refused by the SDK's schema check
			if !res.IsError || !strings.HasPrefix(text, "INVALID_ARGUMENT:") && !strings.Contains(text, "missing properties") {
				t.Fatalf("expected INVALID_ARGUMENT, got %q", text)
			}
		})
	}
	if f.planBody != nil {
		t.Error("invalid arguments must not reach the planner")
	}
}

func TestPlanTrajectory_PartiallyExecutable(t *testing.T) {
	f := &fakeInstance{plan: okPlan, addAnswer: `{"trajectory":"traj-1","error":{"message":"joint 2","location":0.6,"data":{"kind":"JointLimitExceededError"}}}`}
	cs := newFake(t, f)
	res, text := call(t, cs, "plan_trajectory", map[string]any{"cell": "cell", "controller": "ur",
		"commands": []any{map[string]any{"type": "joint_ptp", "joints": []float64{0.1, -1.57, -1.57, -1.57, 1.57, -1.57}}}})
	if res.IsError || !strings.Contains(text, "executable=partial") {
		t.Fatalf("unexpected result %q", text)
	}
}

func TestPlanCollisionFree(t *testing.T) {
	f := &fakeInstance{plan: okPlan}
	cs := newFake(t, f)
	res, text := call(t, cs, "plan_collision_free", map[string]any{"cell": "cell", "controller": "ur",
		"target_joints": []float64{0.1, -1.57, -1.57, -1.57, 1.57, -1.57}, "collision_setup": "workcell", "max_iterations": 500})
	if res.IsError {
		t.Fatalf("unexpected result %q", text)
	}
	alg := f.planBody["algorithm"].(map[string]any)
	setups := f.planBody["collision_setups"].(map[string]any)["workcell"].(map[string]any)
	if alg["algorithm_name"] != "RRTConnectAlgorithm" || alg["max_iterations"].(float64) != 500 || setups["source"] != "key" {
		t.Errorf("unexpected body: %v", f.planBody)
	}

	f.plan = `{"response":{"error_feedback":{"error_feedback_name":"ErrorMaxIterationsExceeded","max_iterations":500}}}`
	_, text = call(t, cs, "plan_collision_free", map[string]any{"cell": "cell", "controller": "ur", "target_joints": []float64{0.1, -1.57, -1.57, -1.57, 1.57, -1.57}})
	if !strings.HasPrefix(text, "PLANNING_FAILED: plan_collision_free: ErrorMaxIterationsExceeded") {
		t.Fatalf("unexpected result %q", text)
	}
}

func TestListDescribeDelete(t *testing.T) {
	f := &fakeInstance{}
	cs := newFake(t, f)
	_, text := call(t, cs, "list_trajectories", map[string]any{"cell": "cell", "controller": "ur"})
	if !strings.Contains(text, "total=2") {
		t.Errorf("unexpected list result %q", text)
	}
	res, text := call(t, cs, "describe_trajectory", map[string]any{"cell": "cell", "controller": "ur", "trajectory": "traj-1"})
	out := res.StructuredContent.(map[string]any)
	if res.IsError || out["samples"].(float64) != 3 || out["duration_s"].(float64) != 1.25 || out["commands"].(float64) != 1 {
		t.Errorf("unexpected describe result %q %v", text, out)
	}
	_, text = call(t, cs, "describe_trajectory", map[string]any{"cell": "cell", "controller": "ur", "trajectory": "nope"})
	if !strings.HasPrefix(text, "NOT_FOUND:") {
		t.Errorf("unexpected describe result %q", text)
	}
	_, text = call(t, cs, "delete_trajectory", map[string]any{"cell": "cell", "controller": "ur", "trajectory": "traj-1"})
	if !strings.HasPrefix(text, "OK:") {
		t.Errorf("unexpected delete result %q", text)
	}
}

func TestAnnotations(t *testing.T) {
	cs := newFake(t, &fakeInstance{})
	tools, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"list_trajectories": true, "describe_trajectory": true}
	for _, tl := range tools.Tools {
		if tl.Annotations.ReadOnlyHint != want[tl.Name] {
			t.Errorf("%s: readOnlyHint=%v", tl.Name, tl.Annotations.ReadOnlyHint)
		}
	}
	if len(tools.Tools) != 5 {
		t.Errorf("expected 5 tools, got %d", len(tools.Tools))
	}
}

// TestLivePlanning plans a small joint move on the virtual test controller, describes
// and deletes it again.
func TestLivePlanning(t *testing.T) {
	c := livetest.Client(t)
	cs := connect(t, c)
	cell, ctrl := livetest.Cell(), livetest.VirtualController()

	res, text := call(t, cs, "plan_trajectory", map[string]any{"cell": cell, "controller": ctrl,
		"commands": []any{map[string]any{"type": "joint_ptp", "joints": []float64{0.2, -1.5708, -1.5708, -1.5708, 1.5708, -1.5708}}}})
	if res.IsError {
		t.Fatalf("plan_trajectory: %s", text)
	}
	t.Log(text)
	id := res.StructuredContent.(map[string]any)["trajectory"].(string)
	defer call(t, cs, "delete_trajectory", map[string]any{"cell": cell, "controller": ctrl, "trajectory": id})

	_, text = call(t, cs, "describe_trajectory", map[string]any{"cell": cell, "controller": ctrl, "trajectory": id})
	t.Log(text)
	_, text = call(t, cs, "list_trajectories", map[string]any{"cell": cell, "controller": ctrl})
	t.Log(text)

	res, text = call(t, cs, "plan_trajectory", map[string]any{"cell": cell, "controller": ctrl,
		"commands": []any{map[string]any{"type": "line", "pose": map[string]any{"position": []float64{5000, 0, 0}, "orientation": []float64{3.1416, 0, 0}}}}})
	t.Log(text)
	if !res.IsError {
		t.Errorf("an unreachable pose must fail")
	}

	res, text = call(t, cs, "plan_collision_free", map[string]any{"cell": cell, "controller": ctrl,
		"target_joints": []float64{-0.3, -1.4, -1.6, -1.5708, 1.5708, -1.5708}})
	t.Log(text)
	if res.IsError {
		t.Fatalf("plan_collision_free: %s", text)
	}
	id2 := res.StructuredContent.(map[string]any)["trajectory"].(string)
	call(t, cs, "delete_trajectory", map[string]any{"cell": cell, "controller": ctrl, "trajectory": id2})
}
