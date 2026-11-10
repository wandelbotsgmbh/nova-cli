// Package motion implements the opt-in `motion` toolset: execute a stored trajectory
// (planning toolset) on a VIRTUAL controller and stop it again.
//
// Physical controllers are always refused with APPROVAL_REQUIRED (ADR-0009, the
// roadmap's layer-1 default allowPhysicalMotion=false): nova mcp has no approval flow
// for real robots, a human runs those motions through an app or the NOVA UI.
//
// How execution works: execute_trajectory opens the executeTrajectory WebSocket, sends
// InitializeMovementRequest and StartMovementRequest, and watches the motion group state
// until the trajectory ends. The WebSocket's acknowledgements don't signal the end of a
// movement, only the state does (execute.details.state = END_OF_TRAJECTORY).
//
// How stopping works: the WebSocket that started a movement is the only way to pause it
// (PauseMovementRequest). Every execution started by this nova mcp process is kept in a
// registry per controller while it runs; stop_execution sends the pause over that
// connection. Cancelling the execute_trajectory call or hitting its timeout pauses as
// well. Movements started by other clients can't be stopped from here: use the NOVA UI
// or the controller's own stop.
package motion

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/novaapi"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/tool"
)

// CodeExecutionFailed marks a movement the controller refused or aborted, or one that
// didn't end in time (it is paused then).
const CodeExecutionFailed = "EXECUTION_FAILED"

const (
	defaultTimeout = 60 * time.Second
	maxTimeout     = 10 * time.Minute
	// startTolerance is how far (rad or mm per joint) the robot may be from the
	// trajectory's first sample.
	startTolerance = 1e-3
)

// pollInterval is how often the motion group state is read while a movement runs.
var pollInterval = 100 * time.Millisecond

// Tools returns the tools of the `motion` toolset.
func Tools(env tool.Env) []tool.Registration {
	c := env.Client
	return []tool.Registration{
		{Toolset: tool.Motion, ReadOnly: false, Add: func(s *sdk.Server) { addExecute(s, c) }},
		{Toolset: tool.Motion, ReadOnly: false, Add: func(s *sdk.Server) { addStop(s, c) }},
	}
}

// --- registry of running executions ---

type execution struct {
	trajectory string
	conn       *websocket.Conn
	mu         sync.Mutex // serializes writes to conn
	stopped    bool
}

func (e *execution) pause(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.stopped = true
	return wsjson.Write(ctx, e.conn, map[string]string{"message_type": "PauseMovementRequest"})
}

func (e *execution) wasStopped() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.stopped
}

var (
	registryMu sync.Mutex
	registry   = map[string]*execution{}
)

func key(scope, controller string) string { return scope + "/" + controller }

// instanceScope identifies a cell on the targeted instance, so executions on equally
// named cells of different instances (use_instance) don't mix.
func instanceScope(ctx context.Context, c *novaapi.Client, cell string) string {
	return c.BaseURL(ctx) + "#" + cell
}

func register(cell, controller string, e *execution) bool {
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, busy := registry[key(cell, controller)]; busy {
		return false
	}
	registry[key(cell, controller)] = e
	return true
}

func unregister(cell, controller string, e *execution) {
	registryMu.Lock()
	defer registryMu.Unlock()
	if registry[key(cell, controller)] == e {
		delete(registry, key(cell, controller))
	}
}

func running(cell, controller string) *execution {
	registryMu.Lock()
	defer registryMu.Unlock()
	return registry[key(cell, controller)]
}

// --- execute_trajectory ---

type executeInput struct {
	Cell           string `json:"cell" jsonschema:"cell name"`
	Controller     string `json:"controller" jsonschema:"name of a VIRTUAL controller"`
	Trajectory     string `json:"trajectory" jsonschema:"trajectory id from plan_trajectory or plan_collision_free"`
	PlaybackSpeed  int    `json:"playback_speed_percent,omitempty" jsonschema:"1 to 100 (default 100)"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty" jsonschema:"pause and fail if the movement hasn't ended after this many seconds (default 60, max 600)"`
	Wait           *bool  `json:"wait,omitempty" jsonschema:"wait until the movement ends (default true); with false the call returns once the movement started, stop it with stop_execution"`
}

// ExecuteOutput is the final (or, with wait=false, the starting) state of a movement.
type ExecuteOutput struct {
	Trajectory     string    `json:"trajectory"`
	Controller     string    `json:"controller"`
	MotionGroup    string    `json:"motion_group"`
	State          string    `json:"state" jsonschema:"END_OF_TRAJECTORY when done, RUNNING with wait=false, PAUSED_BY_USER after stop_execution"`
	Location       float64   `json:"location" jsonschema:"position along the trajectory: 0 to the number of motion commands"`
	JointPosition  []float64 `json:"joint_position"`
	ElapsedSeconds float64   `json:"elapsed_s"`
}

type trajectoryData struct {
	MotionGroup string `json:"motion_group"`
	Trajectory  struct {
		JointPositions [][]float64 `json:"joint_positions"`
		Times          []float64   `json:"times"`
		Locations      []float64   `json:"locations"`
	} `json:"trajectory"`
}

type motionGroupState struct {
	JointPosition []float64 `json:"joint_position"`
	Standstill    bool      `json:"standstill"`
	Execute       *struct {
		Details *struct {
			Kind       string  `json:"kind"`
			Trajectory string  `json:"trajectory"`
			Location   float64 `json:"location"`
			State      struct {
				Kind string `json:"kind"`
			} `json:"state"`
		} `json:"details"`
	} `json:"execute"`
}

// readMessage reads one answer of the executeTrajectory WebSocket. The instance wraps
// each answer in {"result": …} (unlike the spec); a bare answer is accepted too.
func readMessage(ctx context.Context, conn *websocket.Conn) (wsMessage, error) {
	var envelope struct {
		Result *wsMessage `json:"result"`
		wsMessage
	}
	if err := wsjson.Read(ctx, conn, &envelope); err != nil {
		return wsMessage{}, err
	}
	if envelope.Result != nil {
		return *envelope.Result, nil
	}
	return envelope.wsMessage, nil
}

// wsMessage is an acknowledgement or error from the executeTrajectory WebSocket.
type wsMessage struct {
	Kind               string `json:"kind"`
	Message            string `json:"message"`
	AddTrajectoryError *struct {
		Message string `json:"message"`
		Data    struct {
			Kind string `json:"kind"`
		} `json:"data"`
	} `json:"add_trajectory_error"`
}

func controllerPath(cell, controller string) string {
	return "/cells/" + url.PathEscape(cell) + "/controllers/" + url.PathEscape(controller)
}

func fail(name, format string, args ...any) *novaapi.Error {
	return &novaapi.Error{Code: CodeExecutionFailed, Message: name + ": " + fmt.Sprintf(format, args...)}
}

func addExecute(s *sdk.Server, c *novaapi.Client) {
	const name = "execute_trajectory"
	sdk.AddTool(s, &sdk.Tool{
		Name: name,
		Description: "Execute a stored trajectory on a VIRTUAL controller and wait until it ends. The robot must " +
			"be at the trajectory's start (plan from the current joint position). Physical controllers are " +
			"refused with APPROVAL_REQUIRED: a human runs real motion through an app or the NOVA UI.",
		Annotations: tool.Write("Execute trajectory", false),
	}, func(ctx context.Context, _ *sdk.CallToolRequest, in executeInput) (*sdk.CallToolResult, ExecuteOutput, error) {
		if err := tool.Required(name, "cell", in.Cell, "controller", in.Controller, "trajectory", in.Trajectory); err != nil {
			return tool.Fail[ExecuteOutput](err)
		}
		speed := in.PlaybackSpeed
		if speed == 0 {
			speed = 100
		}
		if speed < 1 || speed > 100 {
			return tool.Invalid[ExecuteOutput](name, "playback_speed_percent must be between 1 and 100")
		}
		timeout := defaultTimeout
		if in.TimeoutSeconds != 0 {
			timeout = time.Duration(in.TimeoutSeconds) * time.Second
		}
		if timeout <= 0 || timeout > maxTimeout {
			return tool.Invalid[ExecuteOutput](name, "timeout_seconds must be between 1 and %d", int(maxTimeout.Seconds()))
		}
		wait := in.Wait == nil || *in.Wait

		// the gate comes first: nothing below may run for a physical controller
		if err := c.RequireVirtual(ctx, name, in.Cell, in.Controller); err != nil {
			return tool.Fail[ExecuteOutput](err)
		}

		var traj trajectoryData
		if err := c.Get(ctx, name, controllerPath(in.Cell, in.Controller)+"/trajectories/"+url.PathEscape(in.Trajectory),
			fmt.Sprintf("trajectory '%s' not found on controller '%s' in cell '%s' (plan it first)", in.Trajectory, in.Controller, in.Cell), &traj); err != nil {
			return tool.Fail[ExecuteOutput](err)
		}
		if len(traj.Trajectory.JointPositions) == 0 {
			return tool.Fail[ExecuteOutput](fail(name, "trajectory '%s' has no samples", in.Trajectory))
		}
		statePath := controllerPath(in.Cell, in.Controller) + "/motion-groups/" + url.PathEscape(traj.MotionGroup) + "/state"
		var state motionGroupState
		if err := c.Get(ctx, name, statePath, fmt.Sprintf("motion group '%s' not found", traj.MotionGroup), &state); err != nil {
			return tool.Fail[ExecuteOutput](err)
		}
		if d := distance(state.JointPosition, traj.Trajectory.JointPositions[0]); d > startTolerance {
			return tool.Invalid[ExecuteOutput](name, "the robot is not at the start of trajectory '%s' (joint distance %.4f); plan a new trajectory from the current joint position", in.Trajectory, d)
		}

		exec := &execution{trajectory: in.Trajectory}
		scope := instanceScope(ctx, c, in.Cell)
		if !register(scope, in.Controller, exec) {
			return tool.Fail[ExecuteOutput](fail(name, "an execution started by this server is still running on controller '%s'; stop it with stop_execution or wait", in.Controller))
		}

		// The movement outlives a wait=false call, so it gets its own contexts: connCtx
		// keeps the WebSocket open (coder/websocket closes the connection when a read's
		// context ends, which would make the pause impossible), runCtx is the timeout.
		// Detach keeps the pinned instance: the watcher polls the instance it moves
		connCtx, connCancel := context.WithCancel(c.Detach(ctx))
		runCtx, runCancel := context.WithTimeout(connCtx, timeout)
		conn, apiErr := dial(runCtx, c, name, in.Cell, in.Controller)
		if apiErr != nil {
			runCancel()
			connCancel()
			unregister(scope, in.Controller, exec)
			return tool.Fail[ExecuteOutput](apiErr)
		}
		exec.conn = conn
		finish := func() {
			_ = conn.Close(websocket.StatusNormalClosure, "")
			runCancel()
			connCancel()
			unregister(scope, in.Controller, exec)
		}

		if apiErr := start(runCtx, exec, name, in.Trajectory, speed); apiErr != nil {
			finish()
			return tool.Fail[ExecuteOutput](apiErr)
		}

		// WebSocket messages after the start are errors or acknowledgements of a pause
		wsErr := make(chan string, 1)
		go func() {
			for {
				msg, err := readMessage(connCtx, conn)
				if err != nil {
					return
				}
				if msg.Kind == "MOTION_ERROR" {
					select {
					case wsErr <- msg.Message:
					default:
					}
					return
				}
			}
		}()

		begin := time.Now()
		out := ExecuteOutput{Trajectory: in.Trajectory, Controller: in.Controller, MotionGroup: traj.MotionGroup, State: "RUNNING"}
		if !wait {
			go func() {
				defer finish()
				_, _ = watch(runCtx, c, name, statePath, exec, traj, wsErr, nil)
			}()
			return tool.OK("movement started", map[string]any{"trajectory": in.Trajectory, "controller": in.Controller, "state": out.State}, out)
		}

		defer finish()
		final, apiErr := watch(runCtx, c, name, statePath, exec, traj, wsErr, ctx.Done())
		out.ElapsedSeconds = math.Round(time.Since(begin).Seconds()*1000) / 1000
		if final != nil {
			out.State, out.Location, out.JointPosition = final.state, final.location, roundAll(final.joints)
		}
		if apiErr != nil {
			return tool.Fail[ExecuteOutput](apiErr)
		}
		sentence := "movement ended"
		if out.State == "PAUSED_BY_USER" {
			sentence = "movement stopped"
		}
		return tool.OK(sentence, map[string]any{
			"trajectory": in.Trajectory, "controller": in.Controller, "state": out.State,
			"location": round(out.Location, 3), "elapsed_s": out.ElapsedSeconds,
		}, out)
	})
}

// dial opens the executeTrajectory WebSocket with the client's login.
func dial(ctx context.Context, c *novaapi.Client, name, cell, controller string) (*websocket.Conn, *novaapi.Error) {
	u := c.BaseURL(ctx) + controllerPath(cell, controller) + "/execution/trajectory"
	switch {
	case strings.HasPrefix(u, "https://"):
		u = "wss://" + strings.TrimPrefix(u, "https://")
	case strings.HasPrefix(u, "http://"):
		u = "ws://" + strings.TrimPrefix(u, "http://")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, tool.AsAPIError(err)
	}
	if apiErr := c.Authorize(ctx, name, req); apiErr != nil {
		return nil, apiErr
	}
	conn, resp, err := websocket.Dial(ctx, u, &websocket.DialOptions{HTTPClient: c.HTTPClient(), HTTPHeader: req.Header})
	if err != nil && strings.Contains(err.Error(), "ReadWriteCloser") {
		// a wrapping transport (debug logging) hides the upgraded connection
		conn, resp, err = websocket.Dial(ctx, u, &websocket.DialOptions{HTTPHeader: req.Header})
	}
	if err != nil {
		if resp != nil {
			switch resp.StatusCode {
			case http.StatusUnauthorized:
				return nil, &novaapi.Error{Code: novaapi.CodeUnauthenticated, Message: name + ": the instance rejected the login, run 'nova login'"}
			case http.StatusForbidden:
				return nil, &novaapi.Error{Code: novaapi.CodeForbidden, Message: name + ": executing trajectories is not allowed for this login"}
			case http.StatusNotFound:
				return nil, &novaapi.Error{Code: novaapi.CodeNotFound, Message: fmt.Sprintf("%s: controller '%s' not found in cell '%s'", name, controller, cell)}
			}
		}
		return nil, &novaapi.Error{Code: novaapi.CodeUnavailable, Message: fmt.Sprintf("%s: cannot open the execution WebSocket: %v", name, err)}
	}
	conn.SetReadLimit(1 << 20)
	return conn, nil
}

// start sends the initialize, playback speed and start requests and checks each
// acknowledgement.
func start(ctx context.Context, exec *execution, name, trajectory string, speed int) *novaapi.Error {
	steps := []struct {
		request map[string]any
		ack     string
	}{
		{map[string]any{"message_type": "InitializeMovementRequest", "initial_location": 0,
			"trajectory": map[string]any{"message_type": "TrajectoryId", "id": trajectory}}, "INITIALIZE_RECEIVED"},
	}
	if speed < 100 {
		steps = append(steps, struct {
			request map[string]any
			ack     string
		}{map[string]any{"message_type": "PlaybackSpeedRequest", "playback_speed_in_percent": speed}, "PLAYBACK_SPEED_RECEIVED"})
	}
	steps = append(steps, struct {
		request map[string]any
		ack     string
	}{map[string]any{"message_type": "StartMovementRequest", "direction": "DIRECTION_FORWARD"}, "START_RECEIVED"})

	for _, step := range steps {
		exec.mu.Lock()
		err := wsjson.Write(ctx, exec.conn, step.request)
		exec.mu.Unlock()
		if err != nil {
			return fail(name, "sending %s failed: %v", step.request["message_type"], err)
		}
		msg, err := readMessage(ctx, exec.conn)
		if err != nil {
			return fail(name, "no answer to %s: %v", step.request["message_type"], err)
		}
		if msg.AddTrajectoryError != nil {
			return fail(name, "the controller rejected the trajectory: %s %s", msg.AddTrajectoryError.Data.Kind, msg.AddTrajectoryError.Message)
		}
		if msg.Kind != step.ack {
			return fail(name, "%s was answered with %s: %s", step.request["message_type"], msg.Kind, msg.Message)
		}
		if msg.Message != "" {
			// acknowledgements carry a message only when the request failed
			return fail(name, "%s failed: %s", step.request["message_type"], msg.Message)
		}
	}
	return nil
}

type finalState struct {
	state    string
	location float64
	joints   []float64
}

// watch polls the motion group state until the trajectory ended, was paused, failed,
// or ctx ended (timeout: the movement is paused). cancel (the tool call's context)
// pauses the movement as well.
func watch(ctx context.Context, c *novaapi.Client, name, statePath string, exec *execution, traj trajectoryData,
	wsErr <-chan string, cancel <-chan struct{}) (*finalState, *novaapi.Error) {
	start := traj.Trajectory.JointPositions[0]
	end := traj.Trajectory.JointPositions[len(traj.Trajectory.JointPositions)-1]
	last := &finalState{state: "RUNNING"}
	seen := false
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		select {
		case msg := <-wsErr:
			return last, fail(name, "the controller aborted the movement: %s", msg)
		case <-cancel:
			_ = exec.pause(context.Background())
			return last, fail(name, "the call was cancelled; the movement was paused at location %.3f", last.location)
		case <-ctx.Done():
			_ = exec.pause(context.Background())
			return last, fail(name, "the movement didn't end in time and was paused at location %.3f; raise timeout_seconds for long trajectories", last.location)
		case <-ticker.C:
		}
		var state motionGroupState
		if err := c.Get(ctx, name, statePath, "motion group not found", &state); err != nil {
			if ctx.Err() != nil {
				continue // the timeout is handled by the select
			}
			_ = exec.pause(context.Background())
			return last, err
		}
		last.joints = state.JointPosition
		if state.Execute != nil && state.Execute.Details != nil && state.Execute.Details.Trajectory == exec.trajectory {
			seen = true
			d := state.Execute.Details
			last.state, last.location = d.State.Kind, d.Location
			switch d.State.Kind {
			case "END_OF_TRAJECTORY":
				if state.Standstill || distance(state.JointPosition, end) <= startTolerance {
					return last, nil
				}
			case "PAUSED_BY_USER":
				if exec.wasStopped() {
					return last, nil
				}
			}
			continue
		}
		// a short trajectory may end between two polls; then only the position tells
		if state.Standstill && distance(state.JointPosition, end) <= startTolerance && (seen || distance(start, end) > startTolerance) {
			last.state = "END_OF_TRAJECTORY"
			if n := len(traj.Trajectory.Locations); n > 0 {
				last.location = traj.Trajectory.Locations[n-1]
			}
			return last, nil
		}
		if exec.wasStopped() && state.Standstill {
			last.state = "PAUSED_BY_USER"
			return last, nil
		}
	}
}

// --- stop_execution ---

type stopInput struct {
	Cell       string `json:"cell" jsonschema:"cell name"`
	Controller string `json:"controller" jsonschema:"controller name"`
}

type stopOutput struct {
	Controller string `json:"controller"`
	Trajectory string `json:"trajectory"`
}

func addStop(s *sdk.Server, c *novaapi.Client) {
	const name = "stop_execution"
	sdk.AddTool(s, &sdk.Tool{
		Name: name,
		Description: "Stop (pause) a movement that execute_trajectory of this server started on a controller. Never " +
			"gated. Movements started elsewhere can't be stopped here: use the NOVA UI or the controller's stop.",
		Annotations: tool.Write("Stop execution", true),
	}, func(ctx context.Context, _ *sdk.CallToolRequest, in stopInput) (*sdk.CallToolResult, stopOutput, error) {
		if err := tool.Required(name, "cell", in.Cell, "controller", in.Controller); err != nil {
			return tool.Fail[stopOutput](err)
		}
		exec := running(instanceScope(ctx, c, in.Cell), in.Controller)
		if exec == nil {
			return tool.Fail[stopOutput](&novaapi.Error{Code: novaapi.CodeNotFound, Message: fmt.Sprintf(
				"%s: no movement started by this server is running on controller '%s' in cell '%s'", name, in.Controller, in.Cell)})
		}
		if err := exec.pause(ctx); err != nil {
			if errors.Is(err, context.Canceled) {
				return tool.Fail[stopOutput](tool.AsAPIError(err))
			}
			return tool.Fail[stopOutput](fail(name, "sending the pause failed: %v", err))
		}
		return tool.OK("movement paused", map[string]any{"controller": in.Controller, "trajectory": exec.trajectory},
			stopOutput{Controller: in.Controller, Trajectory: exec.trajectory})
	})
}

// --- helpers ---

func distance(a, b []float64) float64 {
	if len(a) != len(b) {
		return math.Inf(1)
	}
	d := 0.0
	for i := range a {
		d = math.Max(d, math.Abs(a[i]-b[i]))
	}
	return d
}

func round(v float64, digits int) float64 {
	p := math.Pow(10, float64(digits))
	return math.Round(v*p) / p
}

func roundAll(vs []float64) []float64 {
	out := make([]float64, len(vs))
	for i, v := range vs {
		out[i] = round(v, 4)
	}
	return out
}
