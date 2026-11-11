// Package virtualcontrollers implements the virtual-controllers toolset beyond the
// ported list_robot_configurations and create_virtual_controller: status, joint
// positions, emergency stop, operation mode, behavior, mounting, TCPs, coordinate
// systems, motion groups, safety zones and I/O of simulated controllers.
//
// Every tool is virtual-only (V in the roadmap): it checks the controller's kind on the
// instance with novaapi.Client.RequireVirtual before doing anything, even though the
// endpoints themselves only exist for virtual controllers.
package virtualcontrollers

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/novaapi"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/tool"
)

// Tools returns the tools of this package.
func Tools(env tool.Env) []tool.Registration {
	c := env.Client
	return []tool.Registration{
		statusTool(c),
		setEmergencyStopTool(c),
		setOperationModeTool(c),
		setBehaviorTool(c),
		getMotionGroupTool(c),
		setJointPositionTool(c),
		setMountingTool(c),
		addTCPTool(c),
		deleteTCPTool(c),
		addMotionGroupTool(c),
		deleteMotionGroupTool(c),
		listCoordinateSystemsTool(c),
		addCoordinateSystemTool(c),
		deleteCoordinateSystemTool(c),
		listSafetyZonesTool(c),
		addSafetyZoneTool(c),
		deleteSafetyZoneTool(c),
		listIOsTool(c),
		setIOValuesTool(c),
	}
}

// targeted is implemented by every input: the virtual controller the call acts on.
type targeted interface {
	target() (cell, controller string)
}

// controllerInput addresses a virtual controller.
type controllerInput struct {
	Cell       string `json:"cell" jsonschema:"cell name (see list_cells)"`
	Controller string `json:"controller" jsonschema:"virtual controller name (see list_controllers)"`
}

func (in controllerInput) target() (string, string) { return in.Cell, in.Controller }

// motionGroupInput addresses one motion group of a virtual controller.
type motionGroupInput struct {
	Cell        string `json:"cell" jsonschema:"cell name (see list_cells)"`
	Controller  string `json:"controller" jsonschema:"virtual controller name (see list_controllers)"`
	MotionGroup string `json:"motion_group" jsonschema:"motion group id, e.g. 0@<controller> (see get_virtual_controller_status)"`
}

func (in motionGroupInput) target() (string, string) { return in.Cell, in.Controller }

// add registers a virtual-only tool: the handler runs only after the cell and
// controller arguments are set and the controller is confirmed virtual.
func add[In targeted, Out any](c *novaapi.Client, readOnly bool, t *sdk.Tool,
	h func(ctx context.Context, req *sdk.CallToolRequest, in In) (*sdk.CallToolResult, Out, error)) tool.Registration {
	gated := func(ctx context.Context, req *sdk.CallToolRequest, in In) (*sdk.CallToolResult, Out, error) {
		cell, controller := in.target()
		if err := tool.Required(t.Name, "cell", cell, "controller", controller); err != nil {
			return tool.Fail[Out](err)
		}
		if err := c.RequireVirtual(ctx, t.Name, cell, controller); err != nil {
			return tool.Fail[Out](err)
		}
		return h(ctx, req, in)
	}
	return tool.Registration{
		Toolset:  tool.VirtualControllers,
		ReadOnly: readOnly,
		Add:      func(s *sdk.Server) { sdk.AddTool(s, t, gated) },
	}
}

// basePath is the virtual-controller API root of a controller.
func basePath(cell, controller string) string {
	return "/cells/" + url.PathEscape(cell) + "/virtual-controllers/" + url.PathEscape(controller)
}

func motionGroupPath(in motionGroupInput) string {
	return basePath(in.Cell, in.Controller) + "/motion-groups/" + url.PathEscape(in.MotionGroup)
}

func controllerNotFound(cell, controller string) string {
	return fmt.Sprintf("virtual controller '%s' not found in cell '%s'", controller, cell)
}

func motionGroupNotFound(in motionGroupInput) string {
	return fmt.Sprintf("motion group '%s' not found on virtual controller '%s' in cell '%s'", in.MotionGroup, in.Controller, in.Cell)
}

// enumValue normalizes a user-given enum value: case-insensitive, prefix optional
// ("auto" → "OPERATION_MODE_AUTO"). It returns "" if the value is not in allowed.
func enumValue(value, prefix string, allowed []string) string {
	v := strings.ToUpper(strings.TrimSpace(value))
	v = strings.ReplaceAll(v, "-", "_")
	if !strings.HasPrefix(v, prefix) {
		v = prefix + v
	}
	for _, a := range allowed {
		if a == v {
			return v
		}
	}
	return ""
}

// --- status ---

var operationModes = []string{"OPERATION_MODE_MANUAL", "OPERATION_MODE_MANUAL_T1", "OPERATION_MODE_MANUAL_T2", "OPERATION_MODE_AUTO"}

var behaviors = []string{"BEHAVIOR_AUTOMATIC", "BEHAVIOR_AUTOMATIC_NOT_COMPLY_WITH_CYCLETIME", "BEHAVIOR_EXTERNAL_SOURCE"}

type motionGroupInfo struct {
	MotionGroup string `json:"motion_group" jsonschema:"motion group id, used as motion_group by the other tools"`
	Name        string `json:"name"`
	DOF         int    `json:"dof" jsonschema:"degrees of freedom (number of joints)"`
}

type statusOutput struct {
	Controller    string            `json:"controller"`
	EmergencyStop bool              `json:"emergency_stop" jsonschema:"true while the simulated emergency stop is pressed"`
	OperationMode string            `json:"operation_mode"`
	Behavior      string            `json:"behavior"`
	CycleTimeMs   int64             `json:"cycle_time_ms"`
	MotionGroups  []motionGroupInfo `json:"motion_groups"`
}

func statusTool(c *novaapi.Client) tool.Registration {
	t := &sdk.Tool{
		Name: "get_virtual_controller_status",
		Description: "Status of a virtual (simulated) controller: emergency stop, operation mode, behavior, cycle time " +
			"and its motion groups with their ids. Refused for physical controllers.",
		Annotations: tool.Read("Get virtual controller status"),
	}
	return add(c, true, t, func(ctx context.Context, _ *sdk.CallToolRequest, in controllerInput) (*sdk.CallToolResult, statusOutput, error) {
		base := basePath(in.Cell, in.Controller)
		notFound := controllerNotFound(in.Cell, in.Controller)
		var estop struct {
			Active bool `json:"active"`
		}
		var mode struct {
			Mode string `json:"mode"`
		}
		var behavior string
		var cycle struct {
			CycleTimeMs int64 `json:"cycle_time_ms"`
		}
		groups := []motionGroupInfo{}
		for _, step := range []struct {
			path string
			out  any
		}{
			{"/emergency-stop", &estop}, {"/operationmode", &mode}, {"/behavior", &behavior},
			{"/cycle-time", &cycle}, {"/motion-groups", &groups},
		} {
			if err := c.Get(ctx, t.Name, base+step.path, notFound, step.out); err != nil {
				return tool.Fail[statusOutput](err)
			}
		}
		out := statusOutput{
			Controller: in.Controller, EmergencyStop: estop.Active, OperationMode: mode.Mode,
			Behavior: behavior, CycleTimeMs: cycle.CycleTimeMs, MotionGroups: groups,
		}
		return tool.OK("virtual controller status", map[string]any{
			"controller": in.Controller, "emergency_stop": estop.Active, "operation_mode": mode.Mode,
			"behavior": behavior, "motion_groups": len(groups),
		}, out)
	})
}

type writeOutput struct {
	Controller string `json:"controller"`
	Status     string `json:"status" jsonschema:"'completed' or 'pending'; verify with the matching get/list tool when pending"`
}

type setEmergencyStopInput struct {
	Cell       string `json:"cell" jsonschema:"cell name (see list_cells)"`
	Controller string `json:"controller" jsonschema:"virtual controller name (see list_controllers)"`
	Active     bool   `json:"active" jsonschema:"true presses the simulated emergency stop, false releases it"`
}

func (in setEmergencyStopInput) target() (string, string) { return in.Cell, in.Controller }

func setEmergencyStopTool(c *novaapi.Client) tool.Registration {
	t := &sdk.Tool{
		Name: "set_virtual_emergency_stop",
		Description: "Press (active=true) or release (active=false) the simulated emergency stop of a virtual " +
			"controller, e.g. to test how an app reacts. Refused for physical controllers.",
		Annotations: tool.Write("Set virtual emergency stop", true),
	}
	return add(c, false, t, func(ctx context.Context, _ *sdk.CallToolRequest, in setEmergencyStopInput) (*sdk.CallToolResult, writeOutput, error) {
		status, err := c.JSON(ctx, novaapi.Call{
			Tool: t.Name, Method: "PUT", Path: basePath(in.Cell, in.Controller) + "/emergency-stop",
			Query: url.Values{"active": {fmt.Sprint(in.Active)}}, NotFound: controllerNotFound(in.Cell, in.Controller),
		}, nil)
		if err != nil {
			return tool.Fail[writeOutput](err)
		}
		out := writeOutput{Controller: in.Controller, Status: novaapi.DeployStatus(status)}
		return tool.OK("emergency stop set", map[string]any{"controller": in.Controller, "active": in.Active, "status": out.Status}, out)
	})
}

type setOperationModeInput struct {
	Cell       string `json:"cell" jsonschema:"cell name (see list_cells)"`
	Controller string `json:"controller" jsonschema:"virtual controller name (see list_controllers)"`
	Mode       string `json:"mode" jsonschema:"OPERATION_MODE_AUTO, OPERATION_MODE_MANUAL, OPERATION_MODE_MANUAL_T1 or OPERATION_MODE_MANUAL_T2 (prefix and case optional, e.g. auto)"`
}

func (in setOperationModeInput) target() (string, string) { return in.Cell, in.Controller }

func setOperationModeTool(c *novaapi.Client) tool.Registration {
	t := &sdk.Tool{
		Name: "set_virtual_operation_mode",
		Description: "Switch the simulated operation mode of a virtual controller (auto, manual, manual_t1, " +
			"manual_t2). Refused for physical controllers.",
		Annotations: tool.Write("Set virtual operation mode", true),
	}
	return add(c, false, t, func(ctx context.Context, _ *sdk.CallToolRequest, in setOperationModeInput) (*sdk.CallToolResult, writeOutput, error) {
		mode := enumValue(in.Mode, "OPERATION_MODE_", operationModes)
		if mode == "" {
			return tool.Invalid[writeOutput](t.Name, "unknown mode '%s', use one of %s", in.Mode, strings.Join(operationModes, ", "))
		}
		status, err := c.JSON(ctx, novaapi.Call{
			Tool: t.Name, Method: "PUT", Path: basePath(in.Cell, in.Controller) + "/operationmode",
			Query: url.Values{"mode": {mode}}, NotFound: controllerNotFound(in.Cell, in.Controller),
		}, nil)
		if err != nil {
			return tool.Fail[writeOutput](err)
		}
		out := writeOutput{Controller: in.Controller, Status: novaapi.DeployStatus(status)}
		return tool.OK("operation mode set", map[string]any{"controller": in.Controller, "mode": mode, "status": out.Status}, out)
	})
}

type setBehaviorInput struct {
	Cell       string `json:"cell" jsonschema:"cell name (see list_cells)"`
	Controller string `json:"controller" jsonschema:"virtual controller name (see list_controllers)"`
	Behavior   string `json:"behavior" jsonschema:"BEHAVIOR_AUTOMATIC (default, real-time), BEHAVIOR_AUTOMATIC_NOT_COMPLY_WITH_CYCLETIME (as fast as possible) or BEHAVIOR_EXTERNAL_SOURCE (joints driven by an external stream)"`
}

func (in setBehaviorInput) target() (string, string) { return in.Cell, in.Controller }

func setBehaviorTool(c *novaapi.Client) tool.Registration {
	t := &sdk.Tool{
		Name: "set_virtual_behavior",
		Description: "Set how a virtual controller simulates motion: BEHAVIOR_AUTOMATIC (real time), " +
			"BEHAVIOR_AUTOMATIC_NOT_COMPLY_WITH_CYCLETIME (as fast as possible) or BEHAVIOR_EXTERNAL_SOURCE " +
			"(joints driven by an external stream). Refused for physical controllers.",
		Annotations: tool.Write("Set virtual behavior", true),
	}
	return add(c, false, t, func(ctx context.Context, _ *sdk.CallToolRequest, in setBehaviorInput) (*sdk.CallToolResult, writeOutput, error) {
		behavior := enumValue(in.Behavior, "BEHAVIOR_", behaviors)
		if behavior == "" {
			return tool.Invalid[writeOutput](t.Name, "unknown behavior '%s', use one of %s", in.Behavior, strings.Join(behaviors, ", "))
		}
		status, err := c.JSON(ctx, novaapi.Call{
			Tool: t.Name, Method: "PUT", Path: basePath(in.Cell, in.Controller) + "/behavior",
			Query: url.Values{"behavior": {behavior}}, NotFound: controllerNotFound(in.Cell, in.Controller),
		}, nil)
		if err != nil {
			return tool.Fail[writeOutput](err)
		}
		out := writeOutput{Controller: in.Controller, Status: novaapi.DeployStatus(status)}
		return tool.OK("behavior set", map[string]any{"controller": in.Controller, "behavior": behavior, "status": out.Status}, out)
	})
}
