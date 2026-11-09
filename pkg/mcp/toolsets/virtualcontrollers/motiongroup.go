package virtualcontrollers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/novaapi"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/tool"
)

// checkPose validates the pose arguments of a write.
func checkPose(name string, position, orientation []float64) *novaapi.Error {
	if len(position) != 3 {
		return &novaapi.Error{Code: novaapi.CodeInvalidArgument, Message: name + ": 'position' needs exactly 3 values [x, y, z] in millimeters"}
	}
	if len(orientation) != 0 && len(orientation) != 3 && len(orientation) != 4 {
		return &novaapi.Error{Code: novaapi.CodeInvalidArgument, Message: name + ": 'orientation' needs 3 values (rotation vector or Euler angles) or 4 (quaternion)"}
	}
	return nil
}

// --- motion group ---

type tcpInfo struct {
	TCP             string    `json:"tcp" jsonschema:"TCP id"`
	Name            string    `json:"name,omitempty"`
	Position        []float64 `json:"position"`
	Orientation     []float64 `json:"orientation,omitempty"`
	OrientationType string    `json:"orientation_type,omitempty"`
}

type mountingInfo struct {
	CoordinateSystem          string    `json:"coordinate_system"`
	ReferenceCoordinateSystem string    `json:"reference_coordinate_system,omitempty"`
	Position                  []float64 `json:"position,omitempty"`
	Orientation               []float64 `json:"orientation,omitempty"`
	OrientationType           string    `json:"orientation_type,omitempty"`
}

type motionGroupOutput struct {
	MotionGroup    string       `json:"motion_group"`
	JointPositions []float64    `json:"joint_positions" jsonschema:"current joint positions in radians (rotary) or millimeters (linear)"`
	Mounting       mountingInfo `json:"mounting" jsonschema:"where the motion group is mounted"`
	TCPs           []tcpInfo    `json:"tcps"`
}

func getMotionGroupTool(c *novaapi.Client) tool.Registration {
	t := &sdk.Tool{
		Name: "get_virtual_motion_group",
		Description: "A motion group of a virtual controller: current joint positions, mounting and TCPs. " +
			"Refused for physical controllers.",
		Annotations: tool.Read("Get virtual motion group"),
	}
	return add(c, true, t, func(ctx context.Context, _ *sdk.CallToolRequest, in motionGroupInput) (*sdk.CallToolResult, motionGroupOutput, error) {
		if err := tool.Required(t.Name, "motion_group", in.MotionGroup); err != nil {
			return tool.Fail[motionGroupOutput](err)
		}
		notFound := motionGroupNotFound(in)
		var joints struct {
			Positions []float64 `json:"positions"`
		}
		var mounting mountingInfo
		var tcps []struct {
			ID              string    `json:"id"`
			Name            string    `json:"name"`
			Position        []float64 `json:"position"`
			Orientation     []float64 `json:"orientation"`
			OrientationType string    `json:"orientation_type"`
		}
		base := motionGroupPath(in)
		for _, step := range []struct {
			path string
			out  any
		}{{"/state", &joints}, {"/mounting", &mounting}, {"/tcps", &tcps}} {
			if err := c.Get(ctx, t.Name, base+step.path, notFound, step.out); err != nil {
				return tool.Fail[motionGroupOutput](err)
			}
		}
		out := motionGroupOutput{MotionGroup: in.MotionGroup, JointPositions: joints.Positions, Mounting: mounting, TCPs: []tcpInfo{}}
		if out.JointPositions == nil {
			out.JointPositions = []float64{}
		}
		for _, tcp := range tcps {
			out.TCPs = append(out.TCPs, tcpInfo{TCP: tcp.ID, Name: tcp.Name, Position: tcp.Position,
				Orientation: tcp.Orientation, OrientationType: tcp.OrientationType})
		}
		return tool.OK("virtual motion group described", map[string]any{
			"motion_group": in.MotionGroup, "joints": len(out.JointPositions), "tcps": len(out.TCPs),
		}, out)
	})
}

type setJointPositionInput struct {
	Cell           string    `json:"cell" jsonschema:"cell name (see list_cells)"`
	Controller     string    `json:"controller" jsonschema:"virtual controller name (see list_controllers)"`
	MotionGroup    string    `json:"motion_group" jsonschema:"motion group id, e.g. 0@<controller> (see get_virtual_controller_status)"`
	JointPositions []float64 `json:"joint_positions" jsonschema:"one value per joint, radians (rotary) or millimeters (linear)"`
}

func (in setJointPositionInput) target() (string, string) { return in.Cell, in.Controller }

func setJointPositionTool(c *novaapi.Client) tool.Registration {
	t := &sdk.Tool{
		Name: "set_virtual_joint_position",
		Description: "Teleport the joints of a virtual motion group to the given positions, without a planned " +
			"motion (no collision check). Only while nothing is moving it. Refused for physical controllers.",
		Annotations: tool.Write("Set virtual joint position", true),
	}
	return add(c, false, t, func(ctx context.Context, _ *sdk.CallToolRequest, in setJointPositionInput) (*sdk.CallToolResult, writeOutput, error) {
		if err := tool.Required(t.Name, "motion_group", in.MotionGroup); err != nil {
			return tool.Fail[writeOutput](err)
		}
		mg := motionGroupInput{Cell: in.Cell, Controller: in.Controller, MotionGroup: in.MotionGroup}
		// the joint count is checked here: the API accepts a wrong count silently
		var current struct {
			Positions []float64 `json:"positions"`
		}
		if err := c.Get(ctx, t.Name, motionGroupPath(mg)+"/state", motionGroupNotFound(mg), &current); err != nil {
			return tool.Fail[writeOutput](err)
		}
		if len(in.JointPositions) == 0 || len(current.Positions) > 0 && len(in.JointPositions) != len(current.Positions) {
			return tool.Invalid[writeOutput](t.Name, "'joint_positions' needs %d values, one per joint", len(current.Positions))
		}
		status, err := c.JSON(ctx, novaapi.Call{
			Tool: t.Name, Method: "PUT", Path: motionGroupPath(mg) + "/state",
			Body: map[string]any{"positions": in.JointPositions}, NotFound: motionGroupNotFound(mg),
		}, nil)
		if err != nil {
			return tool.Fail[writeOutput](err)
		}
		out := writeOutput{Controller: in.Controller, Status: novaapi.DeployStatus(status)}
		return tool.OK("joint position set", map[string]any{"motion_group": in.MotionGroup, "joints": len(in.JointPositions), "status": out.Status}, out)
	})
}

type setMountingInput struct {
	Cell                      string    `json:"cell" jsonschema:"cell name (see list_cells)"`
	Controller                string    `json:"controller" jsonschema:"virtual controller name (see list_controllers)"`
	MotionGroup               string    `json:"motion_group" jsonschema:"motion group id, e.g. 0@<controller> (see get_virtual_controller_status)"`
	CoordinateSystem          string    `json:"coordinate_system" jsonschema:"id of the mounting frame, keep the one from get_virtual_motion_group unless told otherwise"`
	ReferenceCoordinateSystem string    `json:"reference_coordinate_system,omitempty" jsonschema:"coordinate system the pose is given in; empty is the world"`
	Position                  []float64 `json:"position" jsonschema:"[x, y, z] in millimeters"`
	Orientation               []float64 `json:"orientation,omitempty" jsonschema:"rotation as given by orientation_type; default a rotation vector [rx, ry, rz] in radians"`
	OrientationType           string    `json:"orientation_type,omitempty" jsonschema:"ROTATION_VECTOR (default), QUATERNION or EULER_ANGLES_*"`
}

func (in setMountingInput) target() (string, string) { return in.Cell, in.Controller }

func setMountingTool(c *novaapi.Client) tool.Registration {
	t := &sdk.Tool{
		Name:        "set_virtual_mounting",
		Description: "Set where a virtual motion group is mounted (its base pose). Refused for physical controllers.",
		Annotations: tool.Write("Set virtual mounting", true),
	}
	return add(c, false, t, func(ctx context.Context, _ *sdk.CallToolRequest, in setMountingInput) (*sdk.CallToolResult, writeOutput, error) {
		if err := tool.Required(t.Name, "motion_group", in.MotionGroup, "coordinate_system", in.CoordinateSystem); err != nil {
			return tool.Fail[writeOutput](err)
		}
		if err := checkPose(t.Name, in.Position, in.Orientation); err != nil {
			return tool.Fail[writeOutput](err)
		}
		mg := motionGroupInput{Cell: in.Cell, Controller: in.Controller, MotionGroup: in.MotionGroup}
		body := map[string]any{"coordinate_system": in.CoordinateSystem, "position": in.Position}
		setOptional(body, "reference_coordinate_system", in.ReferenceCoordinateSystem)
		setOrientation(body, in.Orientation, in.OrientationType)
		status, err := c.JSON(ctx, novaapi.Call{
			Tool: t.Name, Method: "PUT", Path: motionGroupPath(mg) + "/mounting", Body: body, NotFound: motionGroupNotFound(mg),
		}, nil)
		if err != nil {
			return tool.Fail[writeOutput](err)
		}
		out := writeOutput{Controller: in.Controller, Status: novaapi.DeployStatus(status)}
		return tool.OK("mounting set", map[string]any{"motion_group": in.MotionGroup, "status": out.Status}, out)
	})
}

func setOptional(body map[string]any, key, value string) {
	if value != "" {
		body[key] = value
	}
}

func setOrientation(body map[string]any, orientation []float64, orientationType string) {
	if len(orientation) > 0 {
		body["orientation"] = orientation
	}
	if orientationType != "" {
		body["orientation_type"] = strings.ToUpper(orientationType)
	}
}

// --- TCPs ---

type addTCPInput struct {
	Cell            string    `json:"cell" jsonschema:"cell name (see list_cells)"`
	Controller      string    `json:"controller" jsonschema:"virtual controller name (see list_controllers)"`
	MotionGroup     string    `json:"motion_group" jsonschema:"motion group id, e.g. 0@<controller> (see get_virtual_controller_status)"`
	TCP             string    `json:"tcp" jsonschema:"TCP id to create or replace"`
	Name            string    `json:"name,omitempty" jsonschema:"display name"`
	Position        []float64 `json:"position" jsonschema:"[x, y, z] offset from the flange in millimeters"`
	Orientation     []float64 `json:"orientation,omitempty" jsonschema:"rotation as given by orientation_type; default a rotation vector [rx, ry, rz] in radians"`
	OrientationType string    `json:"orientation_type,omitempty" jsonschema:"ROTATION_VECTOR (default), QUATERNION or EULER_ANGLES_*"`
}

func (in addTCPInput) target() (string, string) { return in.Cell, in.Controller }

func addTCPTool(c *novaapi.Client) tool.Registration {
	t := &sdk.Tool{
		Name: "add_virtual_tcp",
		Description: "Add a TCP (tool center point) to a virtual motion group, or replace the TCP with that id. " +
			"Refused for physical controllers.",
		Annotations: tool.Write("Add virtual TCP", true),
	}
	return add(c, false, t, func(ctx context.Context, _ *sdk.CallToolRequest, in addTCPInput) (*sdk.CallToolResult, writeOutput, error) {
		if err := tool.Required(t.Name, "motion_group", in.MotionGroup, "tcp", in.TCP); err != nil {
			return tool.Fail[writeOutput](err)
		}
		if err := checkPose(t.Name, in.Position, in.Orientation); err != nil {
			return tool.Fail[writeOutput](err)
		}
		mg := motionGroupInput{Cell: in.Cell, Controller: in.Controller, MotionGroup: in.MotionGroup}
		body := map[string]any{"position": in.Position}
		setOptional(body, "name", in.Name)
		setOrientation(body, in.Orientation, in.OrientationType)
		status, err := c.JSON(ctx, novaapi.Call{
			Tool: t.Name, Method: "PUT", Path: motionGroupPath(mg) + "/tcps/" + url.PathEscape(in.TCP), Body: body,
			NotFound: motionGroupNotFound(mg),
		}, nil)
		if err != nil {
			return tool.Fail[writeOutput](err)
		}
		out := writeOutput{Controller: in.Controller, Status: novaapi.DeployStatus(status)}
		return tool.OK("TCP added", map[string]any{"motion_group": in.MotionGroup, "tcp": in.TCP, "status": out.Status}, out)
	})
}

type deleteTCPInput struct {
	Cell        string `json:"cell" jsonschema:"cell name (see list_cells)"`
	Controller  string `json:"controller" jsonschema:"virtual controller name (see list_controllers)"`
	MotionGroup string `json:"motion_group" jsonschema:"motion group id, e.g. 0@<controller> (see get_virtual_controller_status)"`
	TCP         string `json:"tcp" jsonschema:"TCP id (see get_virtual_motion_group)"`
}

func (in deleteTCPInput) target() (string, string) { return in.Cell, in.Controller }

func deleteTCPTool(c *novaapi.Client) tool.Registration {
	t := &sdk.Tool{
		Name:        "delete_virtual_tcp",
		Description: "Delete a TCP from a virtual motion group. Refused for physical controllers.",
		Annotations: tool.Destructive("Delete virtual TCP"),
	}
	return add(c, false, t, func(ctx context.Context, _ *sdk.CallToolRequest, in deleteTCPInput) (*sdk.CallToolResult, writeOutput, error) {
		if err := tool.Required(t.Name, "motion_group", in.MotionGroup, "tcp", in.TCP); err != nil {
			return tool.Fail[writeOutput](err)
		}
		mg := motionGroupInput{Cell: in.Cell, Controller: in.Controller, MotionGroup: in.MotionGroup}
		status, err := c.JSON(ctx, novaapi.Call{
			Tool: t.Name, Method: "DELETE", Path: motionGroupPath(mg) + "/tcps/" + url.PathEscape(in.TCP),
			NotFound: fmt.Sprintf("TCP '%s' not found on motion group '%s'", in.TCP, in.MotionGroup),
		}, nil)
		if err != nil {
			return tool.Fail[writeOutput](err)
		}
		out := writeOutput{Controller: in.Controller, Status: novaapi.DeployStatus(status)}
		return tool.OK("TCP deleted", map[string]any{"motion_group": in.MotionGroup, "tcp": in.TCP, "status": out.Status}, out)
	})
}

// --- motion groups ---

type addMotionGroupInput struct {
	Cell                 string    `json:"cell" jsonschema:"cell name (see list_cells)"`
	Controller           string    `json:"controller" jsonschema:"virtual controller name (see list_controllers)"`
	MotionGroup          string    `json:"motion_group" jsonschema:"id of the new motion group, e.g. 1@<controller>"`
	MotionGroupModel     string    `json:"motion_group_model" jsonschema:"motion group model (see list_motion_group_models), e.g. UniversalRobots_UR5e"`
	InitialJointPosition []float64 `json:"initial_joint_position,omitempty" jsonschema:"start joint positions in radians, one per joint"`
}

func (in addMotionGroupInput) target() (string, string) { return in.Cell, in.Controller }

func addMotionGroupTool(c *novaapi.Client) tool.Registration {
	t := &sdk.Tool{
		Name: "add_virtual_motion_group",
		Description: "Add a motion group (e.g. a second robot or a linear axis) of a given model to a virtual " +
			"controller. Refused for physical controllers.",
		Annotations: tool.Write("Add virtual motion group", false),
	}
	return add(c, false, t, func(ctx context.Context, _ *sdk.CallToolRequest, in addMotionGroupInput) (*sdk.CallToolResult, writeOutput, error) {
		if err := tool.Required(t.Name, "motion_group", in.MotionGroup, "motion_group_model", in.MotionGroupModel); err != nil {
			return tool.Fail[writeOutput](err)
		}
		body := map[string]any{"motion_group": in.MotionGroup, "motion_group_model": in.MotionGroupModel}
		if len(in.InitialJointPosition) > 0 {
			raw, _ := json.Marshal(in.InitialJointPosition)
			body["initial_joint_position"] = string(raw)
		}
		status, err := c.JSON(ctx, novaapi.Call{
			Tool: t.Name, Method: "POST", Path: basePath(in.Cell, in.Controller) + "/motion-groups", Body: body,
			NotFound: controllerNotFound(in.Cell, in.Controller),
		}, nil)
		if err != nil {
			return tool.Fail[writeOutput](err)
		}
		out := writeOutput{Controller: in.Controller, Status: novaapi.DeployStatus(status)}
		return tool.OK("motion group added", map[string]any{"controller": in.Controller, "motion_group": in.MotionGroup, "status": out.Status}, out)
	})
}

func deleteMotionGroupTool(c *novaapi.Client) tool.Registration {
	t := &sdk.Tool{
		Name: "delete_virtual_motion_group",
		Description: "Delete a motion group from a virtual controller. Confirm with the user first. Refused for " +
			"physical controllers.",
		Annotations: tool.Destructive("Delete virtual motion group"),
	}
	return add(c, false, t, func(ctx context.Context, _ *sdk.CallToolRequest, in motionGroupInput) (*sdk.CallToolResult, writeOutput, error) {
		if err := tool.Required(t.Name, "motion_group", in.MotionGroup); err != nil {
			return tool.Fail[writeOutput](err)
		}
		status, err := c.JSON(ctx, novaapi.Call{
			Tool: t.Name, Method: "DELETE", Path: motionGroupPath(in), NotFound: motionGroupNotFound(in),
		}, nil)
		if err != nil {
			return tool.Fail[writeOutput](err)
		}
		out := writeOutput{Controller: in.Controller, Status: novaapi.DeployStatus(status)}
		return tool.OK("motion group deleted", map[string]any{"controller": in.Controller, "motion_group": in.MotionGroup, "status": out.Status}, out)
	})
}
