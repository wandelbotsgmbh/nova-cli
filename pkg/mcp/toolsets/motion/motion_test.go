package motion_test

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

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/internal/livetest"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/novaapi"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/tool"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/toolsets/motion"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/toolsets/planning"
)

func connect(t *testing.T, c *novaapi.Client, extra ...tool.Registration) *sdk.ClientSession {
	t.Helper()
	server := sdk.NewServer(&sdk.Implementation{Name: "test", Version: "test"}, nil)
	for _, reg := range append(motion.Tools(tool.Env{Client: c}), extra...) {
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

// fakeController simulates a virtual controller: the trajectory runs for runPolls state
// reads after the start, then ends at the last sample.
type fakeController struct {
	mu         sync.Mutex
	kind       string
	joints     []float64
	initAnswer string // message of INITIALIZE_RECEIVED
	motionErr  bool   // send MOTION_ERROR after the start
	runPolls   int    // state reads while RUNNING; <0 never ends
	started    bool
	paused     bool
	polls      int
	wsOpened   bool
	requests   []string
}

var (
	startJoints = []float64{0, -1.57, -1.57, -1.57, 1.57, -1.57}
	endJoints   = []float64{0.2, -1.57, -1.57, -1.57, 1.57, -1.57}
)

func (f *fakeController) server(t *testing.T) *novaapi.Client {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/cells/cell/controllers/sim", func(w http.ResponseWriter, r *http.Request) {
		f.log(r)
		_, _ = io.WriteString(w, `{"name":"sim","configuration":{"kind":"`+f.kind+`","controller_ip":"10.0.0.1"}}`)
	})
	mux.HandleFunc("/cells/cell/controllers/sim/trajectories/t1", func(w http.ResponseWriter, r *http.Request) {
		f.log(r)
		_, _ = io.WriteString(w, `{"motion_group":"0@sim","tcp":"Flange","trajectory":{"joint_positions":[
			[0,-1.57,-1.57,-1.57,1.57,-1.57],[0.1,-1.57,-1.57,-1.57,1.57,-1.57],[0.2,-1.57,-1.57,-1.57,1.57,-1.57]],
			"times":[0,0.1,0.2],"locations":[0,0.5,1]}}`)
	})
	mux.HandleFunc("/cells/cell/controllers/sim/motion-groups/0@sim/state", func(w http.ResponseWriter, r *http.Request) {
		f.log(r)
		f.mu.Lock()
		defer f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if !f.started {
			_ = jsonWrite(w, map[string]any{"joint_position": f.joints, "standstill": true})
			return
		}
		f.polls++
		state, standstill := "RUNNING", false
		switch {
		case f.paused:
			state, standstill = "PAUSED_BY_USER", true
		case f.runPolls >= 0 && f.polls > f.runPolls:
			state, standstill, f.joints = "END_OF_TRAJECTORY", true, endJoints
		}
		_ = jsonWrite(w, map[string]any{"joint_position": f.joints, "standstill": standstill,
			"execute": map[string]any{"joint_position": f.joints, "details": map[string]any{
				"kind": "TRAJECTORY", "trajectory": "t1", "location": 0.5, "state": map[string]any{"kind": state}}}})
	})
	mux.HandleFunc("/cells/cell/controllers/sim/execution/trajectory", func(w http.ResponseWriter, r *http.Request) {
		f.log(r)
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		f.mu.Lock()
		f.wsOpened = true
		f.mu.Unlock()
		ctx := r.Context()
		for {
			var msg map[string]any
			if err := wsjson.Read(ctx, conn, &msg); err != nil {
				return
			}
			f.mu.Lock()
			var answer map[string]any
			switch msg["message_type"] {
			case "InitializeMovementRequest":
				answer = map[string]any{"kind": "INITIALIZE_RECEIVED"}
				if f.initAnswer != "" {
					answer["message"] = f.initAnswer
				}
			case "PlaybackSpeedRequest":
				answer = map[string]any{"kind": "PLAYBACK_SPEED_RECEIVED"}
			case "StartMovementRequest":
				f.started = true
				answer = map[string]any{"kind": "START_RECEIVED"}
			case "PauseMovementRequest":
				f.paused = true
				answer = map[string]any{"kind": "PAUSE_RECEIVED"}
			}
			motionErr := f.motionErr && msg["message_type"] == "StartMovementRequest"
			f.mu.Unlock()
			// the instance wraps every answer in {"result": …}
			_ = wsjson.Write(ctx, conn, map[string]any{"result": answer})
			if motionErr {
				_ = wsjson.Write(ctx, conn, map[string]any{"result": map[string]any{"kind": "MOTION_ERROR", "message": "controller disconnected"}})
			}
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return novaapi.NewClient(srv.URL, nil, nil, nil)
}

func jsonWrite(w io.Writer, v any) error {
	return json.NewEncoder(w).Encode(v)
}

func (f *fakeController) log(r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, r.URL.Path)
}

func newVirtual() *fakeController {
	return &fakeController{kind: "VirtualController", joints: append([]float64(nil), startJoints...), runPolls: 2}
}

var execArgs = map[string]any{"cell": "cell", "controller": "sim", "trajectory": "t1"}

func TestExecute_PhysicalControllerIsRefusedBeforeAnyMotionRequest(t *testing.T) {
	for _, kind := range []string{"KukaController", "UniversalrobotsController", "SomeFutureController", ""} {
		t.Run(kind, func(t *testing.T) {
			f := newVirtual()
			f.kind = kind
			cs := connect(t, f.server(t))
			res, text := call(t, cs, "execute_trajectory", execArgs)
			if !res.IsError || !strings.HasPrefix(text, "APPROVAL_REQUIRED:") {
				t.Fatalf("expected APPROVAL_REQUIRED, got %q", text)
			}
			if strings.Contains(text, "10.0.0.1") {
				t.Errorf("connection details leaked: %q", text)
			}
			if f.wsOpened || len(f.requests) != 1 {
				t.Errorf("only the kind lookup may be sent, got %v (ws opened: %v)", f.requests, f.wsOpened)
			}
		})
	}
}

func TestExecute_RunsToTheEnd(t *testing.T) {
	f := newVirtual()
	cs := connect(t, f.server(t))
	args := map[string]any{"cell": "cell", "controller": "sim", "trajectory": "t1", "playback_speed_percent": 50}
	res, text := call(t, cs, "execute_trajectory", args)
	if res.IsError || !strings.Contains(text, "state=END_OF_TRAJECTORY") {
		t.Fatalf("unexpected result %q", text)
	}
	out := res.StructuredContent.(map[string]any)
	if out["joint_position"].([]any)[0].(float64) != 0.2 {
		t.Errorf("expected the final joints, got %v", out)
	}
}

func TestExecute_RobotNotAtStart(t *testing.T) {
	f := newVirtual()
	f.joints = endJoints
	cs := connect(t, f.server(t))
	_, text := call(t, cs, "execute_trajectory", execArgs)
	if !strings.HasPrefix(text, "INVALID_ARGUMENT:") || !strings.Contains(text, "not at the start") || f.wsOpened {
		t.Fatalf("unexpected result %q (ws opened: %v)", text, f.wsOpened)
	}
}

func TestExecute_InitializeRejected(t *testing.T) {
	f := newVirtual()
	f.initAnswer = "control mode is claimed by another connection"
	cs := connect(t, f.server(t))
	_, text := call(t, cs, "execute_trajectory", execArgs)
	if !strings.HasPrefix(text, "EXECUTION_FAILED:") || !strings.Contains(text, "claimed") || f.started {
		t.Fatalf("unexpected result %q (started: %v)", text, f.started)
	}
}

func TestExecute_MotionError(t *testing.T) {
	f := newVirtual()
	f.motionErr = true
	f.runPolls = -1
	cs := connect(t, f.server(t))
	_, text := call(t, cs, "execute_trajectory", execArgs)
	if !strings.HasPrefix(text, "EXECUTION_FAILED:") || !strings.Contains(text, "controller disconnected") {
		t.Fatalf("unexpected result %q", text)
	}
}

func TestExecute_TimeoutPauses(t *testing.T) {
	f := newVirtual()
	f.runPolls = -1
	cs := connect(t, f.server(t))
	_, text := call(t, cs, "execute_trajectory", map[string]any{"cell": "cell", "controller": "sim", "trajectory": "t1", "timeout_seconds": 1})
	if !strings.HasPrefix(text, "EXECUTION_FAILED:") || !strings.Contains(text, "paused") {
		t.Fatalf("unexpected result %q", text)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		paused := f.paused
		f.mu.Unlock()
		if paused {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Error("a timed-out movement must be paused")
}

func TestExecute_NoWaitThenStop(t *testing.T) {
	f := newVirtual()
	f.runPolls = -1
	cs := connect(t, f.server(t))
	res, text := call(t, cs, "execute_trajectory", map[string]any{"cell": "cell", "controller": "sim", "trajectory": "t1", "wait": false})
	if res.IsError || !strings.Contains(text, "state=RUNNING") {
		t.Fatalf("unexpected result %q", text)
	}
	_, text = call(t, cs, "execute_trajectory", execArgs)
	if !strings.HasPrefix(text, "EXECUTION_FAILED:") || !strings.Contains(text, "still running") {
		t.Errorf("a second execution on the same controller must be refused, got %q", text)
	}
	_, text = call(t, cs, "stop_execution", map[string]any{"cell": "cell", "controller": "sim"})
	if !strings.HasPrefix(text, "OK: movement paused") {
		t.Fatalf("unexpected stop result %q", text)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		_, text = call(t, cs, "stop_execution", map[string]any{"cell": "cell", "controller": "sim"})
		if strings.HasPrefix(text, "NOT_FOUND:") {
			return // the background watcher saw the pause and released the controller
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Errorf("the execution should end after the pause, last stop result %q", text)
}

func TestStop_NothingRunning(t *testing.T) {
	cs := connect(t, newVirtual().server(t))
	res, text := call(t, cs, "stop_execution", map[string]any{"cell": "cell", "controller": "other"})
	if !res.IsError || !strings.HasPrefix(text, "NOT_FOUND:") {
		t.Fatalf("unexpected result %q", text)
	}
}

func TestExecute_ArgumentValidation(t *testing.T) {
	f := newVirtual()
	cs := connect(t, f.server(t))
	for name, args := range map[string]map[string]any{
		"speed":   {"cell": "cell", "controller": "sim", "trajectory": "t1", "playback_speed_percent": 150},
		"timeout": {"cell": "cell", "controller": "sim", "trajectory": "t1", "timeout_seconds": 100000},
		"empty":   {"cell": "cell", "controller": "sim", "trajectory": " "},
	} {
		t.Run(name, func(t *testing.T) {
			_, text := call(t, cs, "execute_trajectory", args)
			if !strings.HasPrefix(text, "INVALID_ARGUMENT:") {
				t.Fatalf("expected INVALID_ARGUMENT, got %q", text)
			}
		})
	}
	if len(f.requests) != 0 {
		t.Errorf("invalid arguments must not reach the instance: %v", f.requests)
	}
}

// TestLiveExecute plans a small joint move and back on the virtual test controller,
// executes both and checks that the robot moved.
func TestLiveExecute(t *testing.T) {
	c := livetest.Client(t)
	cell, ctrl := livetest.Cell(), livetest.VirtualController()
	cs := connect(t, c, planning.Tools(tool.Env{Client: c})...)

	for i, target := range [][]float64{
		{0.3, -1.5708, -1.5708, -1.5708, 1.5708, -1.5708},
		{0, -1.5708, -1.5708, -1.5708, 1.5708, -1.5708},
	} {
		res, text := call(t, cs, "plan_trajectory", map[string]any{"cell": cell, "controller": ctrl,
			"commands": []any{map[string]any{"type": "joint_ptp", "joints": target}}})
		if res.IsError {
			t.Fatalf("plan %d: %s", i, text)
		}
		id := res.StructuredContent.(map[string]any)["trajectory"].(string)
		t.Cleanup(func() {
			call(t, cs, "delete_trajectory", map[string]any{"cell": cell, "controller": ctrl, "trajectory": id})
		})
		res, text = call(t, cs, "execute_trajectory", map[string]any{"cell": cell, "controller": ctrl, "trajectory": id})
		t.Log(text)
		if res.IsError {
			t.Fatalf("execute %d: %s", i, text)
		}
		joints := res.StructuredContent.(map[string]any)["joint_position"].([]any)
		if got := joints[0].(float64); got < target[0]-0.01 || got > target[0]+0.01 {
			t.Errorf("execute %d: expected joint 0 at %.2f, got %.4f", i, target[0], got)
		}
	}

	// a slow move started without waiting, then stopped halfway
	res, text := call(t, cs, "plan_trajectory", map[string]any{"cell": cell, "controller": ctrl,
		"commands": []any{map[string]any{"type": "joint_ptp", "joints": []float64{1.5, -1.5708, -1.5708, -1.5708, 1.5708, -1.5708}}}})
	if res.IsError {
		t.Fatalf("plan: %s", text)
	}
	id := res.StructuredContent.(map[string]any)["trajectory"].(string)
	t.Cleanup(func() {
		call(t, cs, "delete_trajectory", map[string]any{"cell": cell, "controller": ctrl, "trajectory": id})
	})
	_, text = call(t, cs, "execute_trajectory", map[string]any{"cell": cell, "controller": ctrl, "trajectory": id,
		"playback_speed_percent": 5, "wait": false})
	t.Log(text)
	time.Sleep(500 * time.Millisecond)
	_, text = call(t, cs, "stop_execution", map[string]any{"cell": cell, "controller": ctrl})
	t.Log(text)
	if !strings.HasPrefix(text, "OK:") {
		t.Fatalf("stop: %s", text)
	}

	// back home, so the shared test controller stays in a known state
	time.Sleep(300 * time.Millisecond)
	res, text = call(t, cs, "plan_trajectory", map[string]any{"cell": cell, "controller": ctrl,
		"commands": []any{map[string]any{"type": "joint_ptp", "joints": []float64{0, -1.5708, -1.5708, -1.5708, 1.5708, -1.5708}}}})
	if res.IsError {
		t.Fatalf("plan home: %s", text)
	}
	home := res.StructuredContent.(map[string]any)["trajectory"].(string)
	t.Cleanup(func() {
		call(t, cs, "delete_trajectory", map[string]any{"cell": cell, "controller": ctrl, "trajectory": home})
	})
	if res, text = call(t, cs, "execute_trajectory", map[string]any{"cell": cell, "controller": ctrl, "trajectory": home}); res.IsError {
		t.Fatalf("execute home: %s", text)
	}
	t.Log(text)
}
