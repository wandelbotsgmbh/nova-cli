// Package planning implements the opt-in `planning` toolset: plan a trajectory for one
// motion group, store it in the controller's trajectory cache and refer to it by id.
// Results are summaries (duration, sample count, start and end joints), never the sample
// list: the model never needs it, and execute_trajectory (toolset `motion`) takes the id.
package planning

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/novaapi"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/tool"
)

// CodePlanningFailed marks a plan the planner rejected (collision, joint limit,
// singularity, unreachable pose, …). The message names the planner's feedback.
const CodePlanningFailed = "PLANNING_FAILED"

// maxCommands bounds the motion commands of one plan_trajectory call.
const maxCommands = 20

// Tools returns the tools of the `planning` toolset.
func Tools(env tool.Env) []tool.Registration {
	c := env.Client
	return []tool.Registration{
		{Toolset: tool.Planning, ReadOnly: false, Add: func(s *sdk.Server) { addPlanTrajectory(s, c) }},
		{Toolset: tool.Planning, ReadOnly: false, Add: func(s *sdk.Server) { addPlanCollisionFree(s, c) }},
		{Toolset: tool.Planning, ReadOnly: true, Add: func(s *sdk.Server) { addListTrajectories(s, c) }},
		{Toolset: tool.Planning, ReadOnly: true, Add: func(s *sdk.Server) { addDescribeTrajectory(s, c) }},
		{Toolset: tool.Planning, ReadOnly: false, Add: func(s *sdk.Server) { addDeleteTrajectory(s, c) }},
	}
}

// --- shared types ---

// Pose is a TCP pose in the world frame.
type Pose struct {
	Position    []float64 `json:"position" jsonschema:"[x, y, z] in mm"`
	Orientation []float64 `json:"orientation,omitempty" jsonschema:"rotation vector [rx, ry, rz] in rad (default [0, 0, 0])"`
}

// Command is one curated motion command.
type Command struct {
	Type             string    `json:"type" jsonschema:"one of: joint_ptp (target joints), cartesian_ptp (target pose, joint-space motion), line (straight TCP line to target pose), circle (arc through via_pose to target pose)"`
	Joints           []float64 `json:"joints,omitempty" jsonschema:"target joint position in rad, for joint_ptp"`
	Pose             *Pose     `json:"pose,omitempty" jsonschema:"target TCP pose, for cartesian_ptp, line and circle"`
	ViaPose          *Pose     `json:"via_pose,omitempty" jsonschema:"intermediate pose on the arc, for circle"`
	TCPVelocityLimit float64   `json:"tcp_velocity_limit,omitempty" jsonschema:"optional TCP velocity limit for this command in mm/s"`
}

type targetInput struct {
	Cell        string    `json:"cell" jsonschema:"cell name"`
	Controller  string    `json:"controller" jsonschema:"controller name (see list_controllers)"`
	MotionGroup string    `json:"motion_group,omitempty" jsonschema:"motion group id, e.g. 0@my-robot (default: the controller's only motion group)"`
	TCP         string    `json:"tcp,omitempty" jsonschema:"TCP name (default: the TCP the motion group currently uses)"`
	StartJoints []float64 `json:"start_joints,omitempty" jsonschema:"start joint position in rad (default: the current joint position). execute_trajectory needs the robot at the start, so plan from the current position unless you know better."`
}

// PlanOutput is the result of a successful plan: the stored trajectory.
type PlanOutput struct {
	Trajectory      string    `json:"trajectory" jsonschema:"trajectory id for execute_trajectory, describe_trajectory and delete_trajectory"`
	Controller      string    `json:"controller"`
	MotionGroup     string    `json:"motion_group"`
	TCP             string    `json:"tcp"`
	DurationSeconds float64   `json:"duration_s"`
	Samples         int       `json:"samples"`
	StartJoints     []float64 `json:"start_joints"`
	EndJoints       []float64 `json:"end_joints"`
	Executable      string    `json:"executable" jsonschema:"'full', or 'partial' when the controller only accepts the trajectory up to validation_error.location"`
	ValidationError string    `json:"validation_error,omitempty"`
}

// --- motion group setup ---

type setup struct {
	motionGroup string
	tcp         string
	startJoints []float64
	body        map[string]any // MotionGroupSetup
}

func mgPath(cell, controller, mg string) string {
	return "/cells/" + url.PathEscape(cell) + "/controllers/" + url.PathEscape(controller) +
		"/motion-groups/" + url.PathEscape(mg)
}

// resolveSetup fills in motion group, TCP and start joints and assembles the motion
// group setup from the motion group description: model, cycle time, mounting, TCP offset
// and the auto-mode limits.
func resolveSetup(ctx context.Context, c *novaapi.Client, name string, in targetInput) (*setup, *novaapi.Error) {
	if err := tool.Required(name, "cell", in.Cell, "controller", in.Controller); err != nil {
		return nil, err
	}
	ctrlPath := "/cells/" + url.PathEscape(in.Cell) + "/controllers/" + url.PathEscape(in.Controller)
	notFound := fmt.Sprintf("controller '%s' not found in cell '%s'", in.Controller, in.Cell)

	mg := in.MotionGroup
	if mg == "" {
		var desc struct {
			ConnectedMotionGroups []string `json:"connected_motion_groups"`
		}
		if err := c.Get(ctx, name, ctrlPath+"/description", notFound, &desc); err != nil {
			return nil, err
		}
		if len(desc.ConnectedMotionGroups) != 1 {
			return nil, &novaapi.Error{Code: novaapi.CodeInvalidArgument, Message: fmt.Sprintf(
				"%s: controller '%s' has %d motion groups (%s), pass motion_group", name, in.Controller,
				len(desc.ConnectedMotionGroups), strings.Join(desc.ConnectedMotionGroups, ", "))}
		}
		mg = desc.ConnectedMotionGroups[0]
	}
	mgNotFound := fmt.Sprintf("motion group '%s' not found on controller '%s' in cell '%s'", mg, in.Controller, in.Cell)

	var description struct {
		MotionGroupModel string                     `json:"motion_group_model"`
		CycleTime        int                        `json:"cycle_time"`
		Mounting         json.RawMessage            `json:"mounting"`
		TCPs             map[string]json.RawMessage `json:"tcps"`
		OperationLimits  struct {
			AutoLimits json.RawMessage `json:"auto_limits"`
		} `json:"operation_limits"`
	}
	if err := c.Get(ctx, name, mgPath(in.Cell, in.Controller, mg)+"/description", mgNotFound, &description); err != nil {
		return nil, err
	}

	tcp, start := in.TCP, in.StartJoints
	if tcp == "" || len(start) == 0 {
		var state struct {
			JointPosition []float64 `json:"joint_position"`
			TCP           string    `json:"tcp"`
		}
		if err := c.Get(ctx, name, mgPath(in.Cell, in.Controller, mg)+"/state", mgNotFound, &state); err != nil {
			return nil, err
		}
		if tcp == "" {
			tcp = state.TCP
		}
		if len(start) == 0 {
			start = state.JointPosition
		}
	}

	body := map[string]any{"motion_group_model": description.MotionGroupModel, "cycle_time": description.CycleTime}
	if len(description.Mounting) > 0 {
		body["mounting"] = description.Mounting
	}
	if len(description.OperationLimits.AutoLimits) > 0 {
		body["global_limits"] = description.OperationLimits.AutoLimits
	}
	if tcp != "" {
		raw, ok := description.TCPs[tcp]
		if !ok {
			names := make([]string, 0, len(description.TCPs))
			for n := range description.TCPs {
				names = append(names, n)
			}
			sort.Strings(names)
			return nil, &novaapi.Error{Code: novaapi.CodeInvalidArgument, Message: fmt.Sprintf(
				"%s: TCP '%s' not found on motion group '%s' (available: %s)", name, tcp, mg, strings.Join(names, ", "))}
		}
		var offset struct {
			Pose json.RawMessage `json:"pose"`
		}
		if json.Unmarshal(raw, &offset) == nil && len(offset.Pose) > 0 {
			body["tcp_offset"] = offset.Pose
		}
	}
	return &setup{motionGroup: mg, tcp: tcp, startJoints: start, body: body}, nil
}

// --- planner calls ---

// jointTrajectory is the planner's sample list; it is passed to addTrajectory and
// summarized, never returned.
type jointTrajectory struct {
	JointPositions [][]float64 `json:"joint_positions"`
	Times          []float64   `json:"times"`
	Locations      []float64   `json:"locations"`
}

type planResponse struct {
	Response struct {
		jointTrajectory
		ErrorFeedback     json.RawMessage  `json:"error_feedback"`
		ErrorLocation     *float64         `json:"error_location_on_trajectory"`
		PartialTrajectory *jointTrajectory `json:"joint_trajectory"`
	} `json:"response"`
}

// post sends a planner request. The planner answers 422 with validation details the
// generic error mapping drops, so this reads the body itself.
func post(ctx context.Context, c *novaapi.Client, name, path string, body any, out any) *novaapi.Error {
	raw, err := json.Marshal(body)
	if err != nil {
		return tool.AsAPIError(err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL(ctx)+path, bytes.NewReader(raw))
	if err != nil {
		return tool.AsAPIError(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if apiErr := c.Authorize(ctx, name, req); apiErr != nil {
		return apiErr
	}
	resp, err := c.HTTPClient().Do(req)
	if err != nil {
		return &novaapi.Error{Code: novaapi.CodeUnavailable, Message: fmt.Sprintf("%s: cannot reach the NOVA instance: %v", name, err)}
	}
	defer resp.Body.Close()
	answer, err := io.ReadAll(resp.Body)
	if err != nil {
		return &novaapi.Error{Code: novaapi.CodeUnavailable, Message: fmt.Sprintf("%s: reading the answer failed: %v", name, err)}
	}
	switch {
	case resp.StatusCode == http.StatusOK:
		if err := json.Unmarshal(answer, out); err != nil {
			return c.UnexpectedAnswer(ctx, name, resp.StatusCode, answer)
		}
		return nil
	case resp.StatusCode == http.StatusUnprocessableEntity:
		return &novaapi.Error{Code: novaapi.CodeInvalidArgument, Message: name + ": " + validationMessage(answer)}
	case resp.StatusCode == http.StatusNotFound:
		return &novaapi.Error{Code: novaapi.CodeNotFound, Message: name + ": cell or motion group model not found"}
	case resp.StatusCode == http.StatusUnauthorized:
		return &novaapi.Error{Code: novaapi.CodeUnauthenticated, Message: name + ": the instance rejected the login, run 'nova login'"}
	case resp.StatusCode == http.StatusForbidden:
		return &novaapi.Error{Code: novaapi.CodeForbidden, Message: name + ": planning is not allowed for this login"}
	case resp.StatusCode == http.StatusBadRequest:
		return &novaapi.Error{Code: novaapi.CodeInvalidArgument, Message: name + ": " + validationMessage(answer)}
	default:
		return c.UnexpectedAnswer(ctx, name, resp.StatusCode, answer)
	}
}

// validationMessage summarizes a Plan422Response ({detail: [{loc, msg, data}]}) or a
// NOVA error ({message}) in one line.
func validationMessage(body []byte) string {
	var v struct {
		Message string `json:"message"`
		Detail  []struct {
			Loc  []any  `json:"loc"`
			Msg  string `json:"msg"`
			Data struct {
				Name string `json:"error_feedback_name"`
			} `json:"data"`
		} `json:"detail"`
	}
	if json.Unmarshal(body, &v) != nil {
		return "the planner rejected the request"
	}
	var parts []string
	for i, d := range v.Detail {
		if i == 3 {
			parts = append(parts, fmt.Sprintf("and %d more", len(v.Detail)-3))
			break
		}
		loc := make([]string, len(d.Loc))
		for j, l := range d.Loc {
			loc[j] = fmt.Sprint(l)
		}
		part := d.Msg
		if d.Data.Name != "" {
			part = d.Data.Name + ": " + part
		}
		if len(loc) > 0 {
			part += " (at " + strings.Join(loc, ".") + ")"
		}
		parts = append(parts, part)
	}
	if len(parts) == 0 {
		if v.Message != "" {
			return v.Message
		}
		return "the planner rejected the request"
	}
	return strings.Join(parts, "; ")
}

// feedbackSummary turns the planner's error_feedback into a compact line: the feedback
// name plus the one or two facts that tell what to change.
func feedbackSummary(raw json.RawMessage) string {
	var f struct {
		Name           string  `json:"error_feedback_name"`
		JointIndex     *int    `json:"joint_index"`
		Singularity    string  `json:"singularity_type"`
		MaxIterations  int     `json:"max_iterations"`
		InvalidTCPPose *Pose   `json:"invalid_tcp_pose"`
		TorqueValue    float64 `json:"torque_value"`
		TorqueLimit    float64 `json:"torque_limit"`
		Collisions     []struct {
			A     string `json:"id_of_a"`
			B     string `json:"id_of_b"`
			Layer string `json:"id_of_layer"`
		} `json:"collisions"`
	}
	if json.Unmarshal(raw, &f) != nil || f.Name == "" {
		return "unknown planner feedback"
	}
	parts := []string{f.Name}
	if f.JointIndex != nil {
		parts = append(parts, fmt.Sprintf("joint_index=%d", *f.JointIndex))
	}
	if f.Singularity != "" {
		parts = append(parts, "singularity="+f.Singularity)
	}
	if f.MaxIterations > 0 {
		parts = append(parts, fmt.Sprintf("max_iterations=%d (increase it or choose another target)", f.MaxIterations))
	}
	if f.InvalidTCPPose != nil && len(f.InvalidTCPPose.Position) == 3 {
		p := f.InvalidTCPPose.Position
		parts = append(parts, fmt.Sprintf("pose=[%.1f, %.1f, %.1f]", p[0], p[1], p[2]))
	}
	if f.TorqueLimit > 0 {
		parts = append(parts, fmt.Sprintf("torque=%.1f/%.1f", f.TorqueValue, f.TorqueLimit))
	}
	for i, col := range f.Collisions {
		if i == 3 {
			parts = append(parts, fmt.Sprintf("and %d more collisions", len(f.Collisions)-3))
			break
		}
		parts = append(parts, fmt.Sprintf("collision %s<->%s (layer %s)", col.A, col.B, col.Layer))
	}
	return strings.Join(parts, ", ")
}

// store loads a planned trajectory into the controller's cache and summarizes it.
func store(ctx context.Context, c *novaapi.Client, name, cell, controller string, s *setup, traj jointTrajectory) (*sdk.CallToolResult, PlanOutput, error) {
	if len(traj.JointPositions) == 0 || len(traj.Times) != len(traj.JointPositions) {
		return tool.Fail[PlanOutput](&novaapi.Error{Code: CodePlanningFailed, Message: name + ": the planner returned an empty trajectory"})
	}
	body := map[string]any{"motion_group": s.motionGroup, "trajectory": traj}
	if s.tcp != "" {
		body["tcp"] = s.tcp
	}
	var added struct {
		Trajectory string `json:"trajectory"`
		Error      *struct {
			Message  string   `json:"message"`
			Location *float64 `json:"location"`
			Data     struct {
				Kind string `json:"kind"`
			} `json:"data"`
		} `json:"error"`
	}
	if _, err := c.JSON(ctx, novaapi.Call{Tool: name, Method: http.MethodPost,
		Path:     "/cells/" + url.PathEscape(cell) + "/controllers/" + url.PathEscape(controller) + "/trajectories",
		Body:     body,
		NotFound: fmt.Sprintf("controller '%s' not found in cell '%s'", controller, cell),
	}, &added); err != nil {
		return tool.Fail[PlanOutput](err)
	}

	out := PlanOutput{
		Trajectory: added.Trajectory, Controller: controller, MotionGroup: s.motionGroup, TCP: s.tcp,
		DurationSeconds: round(traj.Times[len(traj.Times)-1], 3), Samples: len(traj.JointPositions),
		StartJoints: roundAll(traj.JointPositions[0]), EndJoints: roundAll(traj.JointPositions[len(traj.JointPositions)-1]),
		Executable: "full",
	}
	if added.Error != nil {
		msg := added.Error.Data.Kind
		if added.Error.Message != "" {
			msg = strings.TrimSpace(msg + " " + added.Error.Message)
		}
		if added.Error.Location != nil {
			msg += fmt.Sprintf(" at location %.3f", *added.Error.Location)
		}
		out.ValidationError = msg
		if added.Trajectory == "" {
			return tool.Fail[PlanOutput](&novaapi.Error{Code: CodePlanningFailed, Message: name + ": the controller rejected the trajectory: " + msg})
		}
		out.Executable = "partial"
	}
	fields := map[string]any{"trajectory": out.Trajectory, "duration_s": out.DurationSeconds, "samples": out.Samples, "executable": out.Executable}
	return tool.OK("trajectory planned and stored", fields, out)
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

// --- plan_trajectory ---

type planTrajectoryInput struct {
	targetInput
	Commands []Command `json:"commands" jsonschema:"1 to 20 motion commands, executed in order"`
}

func poseBody(p *Pose) (map[string]any, error) {
	if p == nil || len(p.Position) != 3 {
		return nil, fmt.Errorf("pose.position needs [x, y, z]")
	}
	orientation := p.Orientation
	if len(orientation) == 0 {
		orientation = []float64{0, 0, 0}
	}
	if len(orientation) != 3 {
		return nil, fmt.Errorf("pose.orientation needs [rx, ry, rz]")
	}
	return map[string]any{"position": p.Position, "orientation": orientation}, nil
}

func commandBody(cmd Command, dof int) (map[string]any, error) {
	path := map[string]any{}
	switch cmd.Type {
	case "joint_ptp":
		if len(cmd.Joints) == 0 || dof > 0 && len(cmd.Joints) != dof {
			return nil, fmt.Errorf("joint_ptp needs 'joints' with %d values", dof)
		}
		path["path_definition_name"] = "PathJointPTP"
		path["target_joint_position"] = cmd.Joints
	case "cartesian_ptp", "line", "circle":
		target, err := poseBody(cmd.Pose)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", cmd.Type, err)
		}
		path["target_pose"] = target
		path["path_definition_name"] = map[string]string{"cartesian_ptp": "PathCartesianPTP", "line": "PathLine", "circle": "PathCircle"}[cmd.Type]
		if cmd.Type == "circle" {
			via, err := poseBody(cmd.ViaPose)
			if err != nil {
				return nil, fmt.Errorf("circle via_pose: %w", err)
			}
			path["via_pose"] = via
		}
	default:
		return nil, fmt.Errorf("unknown type %q (joint_ptp, cartesian_ptp, line, circle)", cmd.Type)
	}
	body := map[string]any{"path": path}
	if cmd.TCPVelocityLimit < 0 {
		return nil, fmt.Errorf("tcp_velocity_limit must be positive")
	}
	if cmd.TCPVelocityLimit > 0 {
		body["limits_override"] = map[string]any{"tcp_velocity_limit": cmd.TCPVelocityLimit}
	}
	return body, nil
}

func addPlanTrajectory(s *sdk.Server, c *novaapi.Client) {
	const name = "plan_trajectory"
	sdk.AddTool(s, &sdk.Tool{
		Name: name,
		Description: "Plan a trajectory for one motion group from a short list of motion commands (joint_ptp, " +
			"cartesian_ptp, line, circle) and store it on the controller. Returns a trajectory id with duration " +
			"and start/end joints; execute it with execute_trajectory (toolset motion). Poses are in the world " +
			"frame, mm and rotation vectors in rad. Plans from the current joint position unless start_joints is given.",
		Annotations: tool.Write("Plan trajectory", false),
	}, func(ctx context.Context, _ *sdk.CallToolRequest, in planTrajectoryInput) (*sdk.CallToolResult, PlanOutput, error) {
		if len(in.Commands) == 0 || len(in.Commands) > maxCommands {
			return tool.Invalid[PlanOutput](name, "pass 1 to %d commands", maxCommands)
		}
		st, err := resolveSetup(ctx, c, name, in.targetInput)
		if err != nil {
			return tool.Fail[PlanOutput](err)
		}
		commands := make([]map[string]any, len(in.Commands))
		for i, cmd := range in.Commands {
			body, cerr := commandBody(cmd, len(st.startJoints))
			if cerr != nil {
				return tool.Invalid[PlanOutput](name, "commands[%d]: %v", i, cerr)
			}
			commands[i] = body
		}
		var plan planResponse
		if err := post(ctx, c, name, "/cells/"+url.PathEscape(in.Cell)+"/trajectory-planning/plan-trajectory", map[string]any{
			"motion_group_setup":   st.body,
			"start_joint_position": st.startJoints,
			"motion_commands":      commands,
		}, &plan); err != nil {
			return tool.Fail[PlanOutput](err)
		}
		if len(plan.Response.ErrorFeedback) > 0 {
			msg := name + ": " + feedbackSummary(plan.Response.ErrorFeedback)
			if loc := plan.Response.ErrorLocation; loc != nil {
				msg += fmt.Sprintf(" at location %.3f (command %d)", *loc, int(*loc)+1)
			}
			return tool.Fail[PlanOutput](&novaapi.Error{Code: CodePlanningFailed, Message: msg})
		}
		return store(ctx, c, name, in.Cell, in.Controller, st, plan.Response.jointTrajectory)
	})
}

// --- plan_collision_free ---

type planCollisionFreeInput struct {
	targetInput
	TargetJoints   []float64 `json:"target_joints" jsonschema:"target joint position in rad (compute it from a pose with inverse_kinematics)"`
	CollisionSetup string    `json:"collision_setup,omitempty" jsonschema:"key of a stored collision setup to avoid (toolset stores); without it only the motion group's own collision setup is checked"`
	MaxIterations  int       `json:"max_iterations,omitempty" jsonschema:"RRT-Connect iteration limit (default 10000)"`
}

func addPlanCollisionFree(s *sdk.Server, c *novaapi.Client) {
	const name = "plan_collision_free"
	sdk.AddTool(s, &sdk.Tool{
		Name: name,
		Description: "Plan a collision-free point-to-point motion to target joints (RRT-Connect) and store it on " +
			"the controller. Pass collision_setup to avoid a stored collision setup. Returns a trajectory id for " +
			"execute_trajectory; fails with PLANNING_FAILED when no path is found.",
		Annotations: tool.Write("Plan collision-free motion", false),
	}, func(ctx context.Context, _ *sdk.CallToolRequest, in planCollisionFreeInput) (*sdk.CallToolResult, PlanOutput, error) {
		if len(in.TargetJoints) == 0 {
			return tool.Invalid[PlanOutput](name, "'target_joints' is required")
		}
		if in.MaxIterations < 0 || in.MaxIterations > 1_000_000 {
			return tool.Invalid[PlanOutput](name, "max_iterations must be between 1 and 1000000")
		}
		st, err := resolveSetup(ctx, c, name, in.targetInput)
		if err != nil {
			return tool.Fail[PlanOutput](err)
		}
		if len(in.TargetJoints) != len(st.startJoints) {
			return tool.Invalid[PlanOutput](name, "target_joints needs %d values", len(st.startJoints))
		}
		algorithm := map[string]any{"algorithm_name": "RRTConnectAlgorithm"}
		if in.MaxIterations > 0 {
			algorithm["max_iterations"] = in.MaxIterations
		}
		body := map[string]any{
			"motion_group_setup":   st.body,
			"start_joint_position": st.startJoints,
			"target":               in.TargetJoints,
			"algorithm":            algorithm,
		}
		if in.CollisionSetup != "" {
			body["collision_setups"] = map[string]any{in.CollisionSetup: map[string]any{"source": "key", "key": in.CollisionSetup}}
		}
		var plan planResponse
		if err := post(ctx, c, name, "/cells/"+url.PathEscape(in.Cell)+"/trajectory-planning/plan-collision-free", body, &plan); err != nil {
			return tool.Fail[PlanOutput](err)
		}
		if len(plan.Response.ErrorFeedback) > 0 {
			return tool.Fail[PlanOutput](&novaapi.Error{Code: CodePlanningFailed, Message: name + ": " + feedbackSummary(plan.Response.ErrorFeedback)})
		}
		return store(ctx, c, name, in.Cell, in.Controller, st, plan.Response.jointTrajectory)
	})
}

// --- list / describe / delete ---

type controllerInput struct {
	Cell       string `json:"cell" jsonschema:"cell name"`
	Controller string `json:"controller" jsonschema:"controller name"`
}

type listInput struct {
	controllerInput
	tool.PageInput
}

type listOutput struct {
	Trajectories []string `json:"trajectories"`
	tool.PageInfo
}

func trajectoriesPath(cell, controller string) string {
	return "/cells/" + url.PathEscape(cell) + "/controllers/" + url.PathEscape(controller) + "/trajectories"
}

func addListTrajectories(s *sdk.Server, c *novaapi.Client) {
	const name = "list_trajectories"
	sdk.AddTool(s, &sdk.Tool{
		Name:        name,
		Description: "List the ids of trajectories stored on a controller. The cache is cleared when the controller disconnects.",
		Annotations: tool.Read("List trajectories"),
	}, func(ctx context.Context, _ *sdk.CallToolRequest, in listInput) (*sdk.CallToolResult, listOutput, error) {
		if err := tool.Required(name, "cell", in.Cell, "controller", in.Controller); err != nil {
			return tool.Fail[listOutput](err)
		}
		var resp struct {
			Trajectories []string `json:"trajectories"`
		}
		if err := c.Get(ctx, name, trajectoriesPath(in.Cell, in.Controller),
			fmt.Sprintf("controller '%s' not found in cell '%s'", in.Controller, in.Cell), &resp); err != nil {
			return tool.Fail[listOutput](err)
		}
		sort.Strings(resp.Trajectories)
		page, info := tool.Page(resp.Trajectories, in.PageInput)
		return tool.OK("trajectories listed", info.Fields(map[string]any{"controller": in.Controller}),
			listOutput{Trajectories: page, PageInfo: info})
	})
}

type trajectoryInput struct {
	controllerInput
	Trajectory string `json:"trajectory" jsonschema:"trajectory id (see list_trajectories)"`
}

// TrajectorySummary describes a stored trajectory without its samples.
type TrajectorySummary struct {
	Trajectory      string    `json:"trajectory"`
	MotionGroup     string    `json:"motion_group"`
	TCP             string    `json:"tcp"`
	DurationSeconds float64   `json:"duration_s"`
	Samples         int       `json:"samples"`
	Commands        int       `json:"commands" jsonschema:"number of motion commands (segments) the trajectory spans"`
	StartJoints     []float64 `json:"start_joints"`
	EndJoints       []float64 `json:"end_joints"`
}

func addDescribeTrajectory(s *sdk.Server, c *novaapi.Client) {
	const name = "describe_trajectory"
	sdk.AddTool(s, &sdk.Tool{
		Name:        name,
		Description: "Summary of a stored trajectory: motion group, TCP, duration, sample count, start and end joints.",
		Annotations: tool.Read("Describe trajectory"),
	}, func(ctx context.Context, _ *sdk.CallToolRequest, in trajectoryInput) (*sdk.CallToolResult, TrajectorySummary, error) {
		if err := tool.Required(name, "cell", in.Cell, "controller", in.Controller, "trajectory", in.Trajectory); err != nil {
			return tool.Fail[TrajectorySummary](err)
		}
		var resp struct {
			MotionGroup string          `json:"motion_group"`
			TCP         string          `json:"tcp"`
			Trajectory  jointTrajectory `json:"trajectory"`
		}
		if err := c.Get(ctx, name, trajectoriesPath(in.Cell, in.Controller)+"/"+url.PathEscape(in.Trajectory),
			fmt.Sprintf("trajectory '%s' not found on controller '%s' in cell '%s'", in.Trajectory, in.Controller, in.Cell), &resp); err != nil {
			return tool.Fail[TrajectorySummary](err)
		}
		t := resp.Trajectory
		out := TrajectorySummary{Trajectory: in.Trajectory, MotionGroup: resp.MotionGroup, TCP: resp.TCP, Samples: len(t.JointPositions)}
		if len(t.Times) > 0 {
			out.DurationSeconds = round(t.Times[len(t.Times)-1], 3)
		}
		if len(t.Locations) > 0 {
			out.Commands = int(math.Ceil(t.Locations[len(t.Locations)-1]))
		}
		if len(t.JointPositions) > 0 {
			out.StartJoints = roundAll(t.JointPositions[0])
			out.EndJoints = roundAll(t.JointPositions[len(t.JointPositions)-1])
		}
		return tool.OK("trajectory described", map[string]any{
			"trajectory": in.Trajectory, "duration_s": out.DurationSeconds, "samples": out.Samples,
		}, out)
	})
}

type deleteOutput struct {
	Trajectory string `json:"trajectory"`
}

func addDeleteTrajectory(s *sdk.Server, c *novaapi.Client) {
	const name = "delete_trajectory"
	sdk.AddTool(s, &sdk.Tool{
		Name:        name,
		Description: "Delete a stored trajectory from a controller's cache. Only the cached plan is removed; the robot doesn't move.",
		Annotations: tool.Destructive("Delete trajectory"),
	}, func(ctx context.Context, _ *sdk.CallToolRequest, in trajectoryInput) (*sdk.CallToolResult, deleteOutput, error) {
		if err := tool.Required(name, "cell", in.Cell, "controller", in.Controller, "trajectory", in.Trajectory); err != nil {
			return tool.Fail[deleteOutput](err)
		}
		if _, err := c.Do(ctx, novaapi.Call{Tool: name, Method: http.MethodDelete,
			Path:     trajectoriesPath(in.Cell, in.Controller) + "/" + url.PathEscape(in.Trajectory),
			NotFound: fmt.Sprintf("trajectory '%s' not found on controller '%s' in cell '%s'", in.Trajectory, in.Controller, in.Cell),
		}); err != nil {
			return tool.Fail[deleteOutput](err)
		}
		return tool.OK("trajectory deleted", map[string]any{"trajectory": in.Trajectory}, deleteOutput{Trajectory: in.Trajectory})
	})
}
