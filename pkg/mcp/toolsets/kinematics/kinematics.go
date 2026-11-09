// Package kinematics serves forward and inverse kinematics. Both are pure computations
// on the instance; nothing moves.
package kinematics

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"net/url"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/novaapi"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/tool"
)

// maxBatch caps joint positions or poses per call, to keep results small.
const maxBatch = 50

// Tools returns the tools of this package.
func Tools(env tool.Env) []tool.Registration {
	return []tool.Registration{forwardKinematics(env.Client), inverseKinematics(env.Client)}
}

// Pose is a position [x, y, z] in mm and an orientation as rotation vector [rx, ry, rz]
// in rad.
type Pose struct {
	Position    []float64 `json:"position" jsonschema:"[x, y, z] in mm"`
	Orientation []float64 `json:"orientation" jsonschema:"rotation vector [rx, ry, rz] in rad"`
}

func (p *Pose) valid() bool {
	return p != nil && len(p.Position) == 3 && len(p.Orientation) == 3
}

func round(v float64, n int) float64 {
	p := math.Pow(10, float64(n))
	r := math.Round(v*p) / p
	if r == 0 {
		return 0
	}
	return r
}

func roundAll(vs []float64, n int) []float64 {
	out := make([]float64, len(vs))
	for i, v := range vs {
		out[i] = round(v, n)
	}
	return out
}

// target selects the robot: a model directly, or a motion group of a controller (model,
// mounting and TCP are then taken from it).
type target struct {
	Cell        string `json:"cell" jsonschema:"cell name (see list_cells); kinematics are computed by this cell"`
	Model       string `json:"motion_group_model,omitempty" jsonschema:"robot model id (see list_motion_group_models); or give controller and motion_group"`
	Controller  string `json:"controller,omitempty" jsonschema:"controller whose motion group model, mounting and TCP to use"`
	MotionGroup string `json:"motion_group,omitempty" jsonschema:"motion group id, e.g. 0@<controller> (with controller)"`
	TCP         string `json:"tcp,omitempty" jsonschema:"TCP name of the motion group (with controller); default: the flange"`
	TCPOffset   *Pose  `json:"tcp_offset,omitempty" jsonschema:"TCP offset from the flange; overrides tcp"`
	Mounting    *Pose  `json:"mounting,omitempty" jsonschema:"robot base in the world; overrides the motion group's mounting"`
}

type resolved struct {
	Model     string `json:"motion_group_model"`
	TCPOffset *Pose  `json:"tcp_offset,omitempty"`
	Mounting  *Pose  `json:"mounting,omitempty"`
}

func (t target) resolve(ctx context.Context, c *novaapi.Client, toolName string) (resolved, *novaapi.Error) {
	if err := tool.Required(toolName, "cell", t.Cell); err != nil {
		return resolved{}, err
	}
	invalid := func(format string, args ...any) *novaapi.Error {
		return &novaapi.Error{Code: novaapi.CodeInvalidArgument, Message: toolName + ": " + fmt.Sprintf(format, args...)}
	}
	for name, p := range map[string]*Pose{"tcp_offset": t.TCPOffset, "mounting": t.Mounting} {
		if p != nil && !p.valid() {
			return resolved{}, invalid("'%s' needs a 3-element position and orientation", name)
		}
	}
	r := resolved{Model: t.Model, TCPOffset: t.TCPOffset, Mounting: t.Mounting}
	switch {
	case t.Controller != "" || t.MotionGroup != "":
		if t.Model != "" {
			return resolved{}, invalid("give either motion_group_model or controller and motion_group, not both")
		}
		if err := tool.Required(toolName, "controller", t.Controller, "motion_group", t.MotionGroup); err != nil {
			return resolved{}, err
		}
		var desc struct {
			Model    string `json:"motion_group_model"`
			Mounting *Pose  `json:"mounting"`
			TCPs     map[string]struct {
				Pose *Pose `json:"pose"`
			} `json:"tcps"`
		}
		path := "/cells/" + url.PathEscape(t.Cell) + "/controllers/" + url.PathEscape(t.Controller) +
			"/motion-groups/" + url.PathEscape(t.MotionGroup) + "/description"
		if err := c.Get(ctx, toolName, path, fmt.Sprintf("motion group '%s' not found on controller '%s' in cell '%s'",
			t.MotionGroup, t.Controller, t.Cell), &desc); err != nil {
			return resolved{}, err
		}
		r.Model = desc.Model
		if r.Mounting == nil {
			r.Mounting = desc.Mounting
		}
		if r.TCPOffset == nil && t.TCP != "" {
			tcp, ok := desc.TCPs[t.TCP]
			if !ok {
				return resolved{}, invalid("TCP '%s' not found on motion group '%s' (see describe_motion_group)", t.TCP, t.MotionGroup)
			}
			r.TCPOffset = tcp.Pose
		}
	case t.Model == "":
		return resolved{}, invalid("give motion_group_model, or controller and motion_group")
	case t.TCP != "":
		return resolved{}, invalid("'tcp' needs controller and motion_group; with a model, give tcp_offset")
	}
	return r, nil
}

// --- forward_kinematics ---

type forwardInput struct {
	target
	JointPositions [][]float64 `json:"joint_positions" jsonschema:"joint positions in rad, one array per robot configuration (max 50)"`
}

type forwardOutput struct {
	Model    string `json:"motion_group_model"`
	TCPPoses []Pose `json:"tcp_poses" jsonschema:"TCP pose per joint position, in the world (mm, rotation vector in rad)"`
}

func forwardKinematics(c *novaapi.Client) tool.Registration {
	const name = "forward_kinematics"
	add := func(s *sdk.Server) {
		sdk.AddTool(s, &sdk.Tool{
			Name: name,
			Description: "Compute the TCP pose for joint positions (rad) of a robot model or a controller's motion group. " +
				"Pure computation, nothing moves.",
			Annotations: tool.Read("Forward kinematics"),
		}, func(ctx context.Context, _ *sdk.CallToolRequest, in forwardInput) (*sdk.CallToolResult, forwardOutput, error) {
			if len(in.JointPositions) == 0 || len(in.JointPositions) > maxBatch {
				return tool.Invalid[forwardOutput](name, "'joint_positions' needs 1 to %d entries", maxBatch)
			}
			r, err := in.resolve(ctx, c, name)
			if err != nil {
				return tool.Fail[forwardOutput](err)
			}
			body := map[string]any{"motion_group_model": r.Model, "joint_positions": in.JointPositions}
			if r.TCPOffset != nil {
				body["tcp_offset"] = r.TCPOffset
			}
			if r.Mounting != nil {
				body["mounting"] = r.Mounting
			}
			var raw struct {
				TCPPoses []Pose `json:"tcp_poses"`
			}
			if _, err := c.JSON(ctx, novaapi.Call{Tool: name, Method: http.MethodPost,
				Path: "/cells/" + url.PathEscape(in.Cell) + "/kinematic/forward", Body: body,
				NotFound: fmt.Sprintf("cell '%s' or model '%s' not found", in.Cell, r.Model)}, &raw); err != nil {
				return tool.Fail[forwardOutput](kinematicsHint(err))
			}
			out := forwardOutput{Model: r.Model, TCPPoses: []Pose{}}
			for _, p := range raw.TCPPoses {
				out.TCPPoses = append(out.TCPPoses, Pose{Position: roundAll(p.Position, 2), Orientation: roundAll(p.Orientation, 4)})
			}
			return tool.OK("forward kinematics computed", map[string]any{"model": r.Model, "poses": len(out.TCPPoses)}, out)
		})
	}
	return tool.Registration{Toolset: tool.Kinematics, ReadOnly: true, Add: add}
}

// --- inverse_kinematics ---

type inverseInput struct {
	target
	TCPPoses               []Pose    `json:"tcp_poses" jsonschema:"TCP poses in the world (mm, rotation vector in rad), max 50"`
	ReferenceJointPosition []float64 `json:"reference_joint_position,omitempty" jsonschema:"joint position in rad; solutions are shifted to its periodicity and sorted by distance to it"`
	MaxSolutions           int       `json:"max_solutions,omitempty" jsonschema:"maximum solutions per pose (default 8)"`
}

type poseSolutions struct {
	Solutions [][]float64 `json:"solutions" jsonschema:"joint positions in rad; empty if the pose is unreachable"`
	Total     int         `json:"total"`
}

type inverseOutput struct {
	Model string          `json:"motion_group_model"`
	Poses []poseSolutions `json:"poses" jsonschema:"solutions per requested pose, in request order"`
}

func inverseKinematics(c *novaapi.Client) tool.Registration {
	const name = "inverse_kinematics"
	add := func(s *sdk.Server) {
		sdk.AddTool(s, &sdk.Tool{
			Name: name,
			Description: "Compute joint positions (rad) that reach TCP poses for a robot model or a controller's motion " +
				"group. An empty solution list means the pose is unreachable. Pure computation, nothing moves.",
			Annotations: tool.Read("Inverse kinematics"),
		}, func(ctx context.Context, _ *sdk.CallToolRequest, in inverseInput) (*sdk.CallToolResult, inverseOutput, error) {
			if len(in.TCPPoses) == 0 || len(in.TCPPoses) > maxBatch {
				return tool.Invalid[inverseOutput](name, "'tcp_poses' needs 1 to %d entries", maxBatch)
			}
			for i := range in.TCPPoses {
				if !in.TCPPoses[i].valid() {
					return tool.Invalid[inverseOutput](name, "tcp_poses[%d] needs a 3-element position and orientation", i)
				}
			}
			maxSolutions := in.MaxSolutions
			if maxSolutions <= 0 {
				maxSolutions = 8
			}
			r, err := in.resolve(ctx, c, name)
			if err != nil {
				return tool.Fail[inverseOutput](err)
			}
			body := map[string]any{"motion_group_model": r.Model, "tcp_poses": in.TCPPoses}
			if r.TCPOffset != nil {
				body["tcp_offset"] = r.TCPOffset
			}
			if r.Mounting != nil {
				body["mounting"] = r.Mounting
			}
			if len(in.ReferenceJointPosition) > 0 {
				body["reference_joint_position"] = in.ReferenceJointPosition
			}
			var raw struct {
				Joints [][][]float64 `json:"joints"`
			}
			if _, err := c.JSON(ctx, novaapi.Call{Tool: name, Method: http.MethodPost,
				Path: "/cells/" + url.PathEscape(in.Cell) + "/kinematic/inverse", Body: body,
				NotFound: fmt.Sprintf("cell '%s' or model '%s' not found", in.Cell, r.Model)}, &raw); err != nil {
				return tool.Fail[inverseOutput](kinematicsHint(err))
			}
			out := inverseOutput{Model: r.Model, Poses: []poseSolutions{}}
			reachable := 0
			for _, solutions := range raw.Joints {
				ps := poseSolutions{Solutions: [][]float64{}, Total: len(solutions)}
				for i, joints := range solutions {
					if i == maxSolutions {
						break
					}
					ps.Solutions = append(ps.Solutions, roundAll(joints, 4))
				}
				if ps.Total > 0 {
					reachable++
				}
				out.Poses = append(out.Poses, ps)
			}
			return tool.OK("inverse kinematics computed", map[string]any{
				"model": r.Model, "poses": len(out.Poses), "reachable": reachable,
			}, out)
		})
	}
	return tool.Registration{Toolset: tool.Kinematics, ReadOnly: true, Add: add}
}

// kinematicsHint adds what usually causes a rejected kinematics request.
func kinematicsHint(err *novaapi.Error) *novaapi.Error {
	if err.Code != novaapi.CodeInvalidArgument {
		return err
	}
	return &novaapi.Error{Code: err.Code, Message: err.Message +
		" (check the number of joints per position against describe_motion_group_model, and that poses are in mm and rad)"}
}
