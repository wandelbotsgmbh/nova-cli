// Package controllers extends the ported controllers toolset (list/describe/delete)
// with state, motion groups, coordinate systems, I/O and network discovery.
package controllers

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"slices"
	"strconv"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/novaapi"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/tool"
)

// Tools returns the tools of this package.
func Tools(env tool.Env) []tool.Registration {
	c := env.Client
	return []tool.Registration{
		getControllerState(c),
		describeMotionGroup(c),
		listCoordinateSystems(c),
		listIODescriptions(c),
		getIOValues(c),
		discoverControllersOnNetwork(c),
		setOutputValues(c),
		setDefaultMode(c),
	}
}

func controllerPath(cell, controller string) string {
	return "/cells/" + url.PathEscape(cell) + "/controllers/" + url.PathEscape(controller)
}

func notFound(cell, controller string) string {
	return fmt.Sprintf("controller '%s' not found in cell '%s'", controller, cell)
}

// round rounds to n decimals, for compact results (mm to 0.01, rad to 1e-4).
func round(v float64, n int) float64 {
	p := math.Pow(10, float64(n))
	r := math.Round(v*p) / p
	if r == 0 {
		return 0 // no -0
	}
	return r
}

func roundAll(vs []float64, n int) []float64 {
	if vs == nil {
		return nil
	}
	out := make([]float64, len(vs))
	for i, v := range vs {
		out[i] = round(v, n)
	}
	return out
}

// Pose is a position [x, y, z] in mm and an orientation as rotation vector [rx, ry, rz]
// in rad.
type Pose struct {
	Position    []float64 `json:"position" jsonschema:"[x, y, z] in mm"`
	Orientation []float64 `json:"orientation" jsonschema:"rotation vector [rx, ry, rz] in rad"`
}

func (p *Pose) rounded() *Pose {
	if p == nil {
		return nil
	}
	return &Pose{Position: roundAll(p.Position, 2), Orientation: roundAll(p.Orientation, 4)}
}

type controllerInput struct {
	Cell       string `json:"cell" jsonschema:"cell name (see list_cells)"`
	Controller string `json:"controller" jsonschema:"controller name (see list_controllers)"`
}

// --- get_controller_state ---

type motionGroupState struct {
	MotionGroup      string    `json:"motion_group"`
	JointPosition    []float64 `json:"joint_position" jsonschema:"joint positions in rad"`
	JointLimitReach  []bool    `json:"joint_limit_reached,omitempty"`
	TCP              string    `json:"tcp,omitempty"`
	TCPPose          *Pose     `json:"tcp_pose,omitempty"`
	CoordinateSystem string    `json:"coordinate_system,omitempty"`
	Standstill       bool      `json:"standstill"`
}

type controllerState struct {
	Controller       string             `json:"controller"`
	Mode             string             `json:"mode"`
	OperationMode    string             `json:"operation_mode"`
	SafetyState      string             `json:"safety_state"`
	VelocityOverride int                `json:"velocity_override,omitempty" jsonschema:"velocity override in percent"`
	Errors           []string           `json:"errors,omitempty"`
	MotionGroups     []motionGroupState `json:"motion_groups"`
}

func getControllerState(c *novaapi.Client) tool.Registration {
	const name = "get_controller_state"
	add := func(s *sdk.Server) {
		sdk.AddTool(s, &sdk.Tool{
			Name: name,
			Description: "Current state of a controller: mode (monitor/control), operation mode, safety state, " +
				"velocity override, and per motion group the joint positions (rad), TCP pose (mm, rotation vector) " +
				"and whether it stands still.",
			Annotations: tool.Read("Get controller state"),
		}, func(ctx context.Context, _ *sdk.CallToolRequest, in controllerInput) (*sdk.CallToolResult, controllerState, error) {
			if err := tool.Required(name, "cell", in.Cell, "controller", in.Controller); err != nil {
				return tool.Fail[controllerState](err)
			}
			var raw struct {
				Mode             string   `json:"mode"`
				LastError        []string `json:"last_error"`
				Controller       string   `json:"controller"`
				OperationMode    string   `json:"operation_mode"`
				SafetyState      string   `json:"safety_state"`
				VelocityOverride int      `json:"velocity_override"`
				MotionGroups     []struct {
					MotionGroup       string    `json:"motion_group"`
					JointPosition     []float64 `json:"joint_position"`
					JointLimitReached struct {
						LimitReached []bool `json:"limit_reached"`
					} `json:"joint_limit_reached"`
					TCP              string `json:"tcp"`
					TCPPose          *Pose  `json:"tcp_pose"`
					CoordinateSystem string `json:"coordinate_system"`
					Standstill       bool   `json:"standstill"`
				} `json:"motion_groups"`
			}
			if err := c.Get(ctx, name, controllerPath(in.Cell, in.Controller)+"/state", notFound(in.Cell, in.Controller), &raw); err != nil {
				return tool.Fail[controllerState](err)
			}
			out := controllerState{
				Controller: raw.Controller, Mode: raw.Mode, OperationMode: raw.OperationMode,
				SafetyState: raw.SafetyState, VelocityOverride: raw.VelocityOverride, MotionGroups: []motionGroupState{},
			}
			if out.Controller == "" {
				out.Controller = in.Controller
			}
			for _, e := range raw.LastError {
				if e != "" {
					out.Errors = append(out.Errors, e)
				}
			}
			for _, mg := range raw.MotionGroups {
				state := motionGroupState{
					MotionGroup: mg.MotionGroup, JointPosition: roundAll(mg.JointPosition, 4),
					TCP: mg.TCP, TCPPose: mg.TCPPose.rounded(), CoordinateSystem: mg.CoordinateSystem, Standstill: mg.Standstill,
				}
				if slices.Contains(mg.JointLimitReached.LimitReached, true) {
					state.JointLimitReach = mg.JointLimitReached.LimitReached
				}
				out.MotionGroups = append(out.MotionGroups, state)
			}
			return tool.OK("controller state", map[string]any{
				"controller": out.Controller, "mode": out.Mode, "operation_mode": out.OperationMode,
				"safety_state": out.SafetyState, "motion_groups": len(out.MotionGroups),
			}, out)
		})
	}
	return tool.Registration{Toolset: tool.Controllers, ReadOnly: true, Add: add}
}

// --- describe_motion_group ---

type motionGroupInput struct {
	Cell        string `json:"cell" jsonschema:"cell name (see list_cells)"`
	Controller  string `json:"controller" jsonschema:"controller name (see list_controllers)"`
	MotionGroup string `json:"motion_group" jsonschema:"motion group id, e.g. 0@<controller> (see get_controller_state)"`
}

type jointLimit struct {
	Lower    *float64 `json:"lower,omitempty" jsonschema:"lower position limit in rad (mm for linear joints)"`
	Upper    *float64 `json:"upper,omitempty" jsonschema:"upper position limit in rad (mm for linear joints)"`
	Velocity *float64 `json:"velocity,omitempty" jsonschema:"max velocity in rad/s"`
}

type namedPose struct {
	Name string `json:"name"`
	Pose *Pose  `json:"pose,omitempty"`
}

type payloadInfo struct {
	Name string  `json:"name"`
	Mass float64 `json:"mass" jsonschema:"payload in kg"`
}

type motionGroupDescription struct {
	MotionGroup      string        `json:"motion_group"`
	Model            string        `json:"motion_group_model"`
	Mounting         *Pose         `json:"mounting,omitempty" jsonschema:"pose of the robot base in the world"`
	TCPs             []namedPose   `json:"tcps"`
	Payloads         []payloadInfo `json:"payloads"`
	CycleTimeMs      int           `json:"cycle_time_ms,omitempty"`
	JointCount       int           `json:"joint_count"`
	AutoJointLimits  []jointLimit  `json:"auto_joint_limits,omitempty" jsonschema:"joint limits in automatic mode"`
	TCPVelocityLimit *float64      `json:"tcp_velocity_limit,omitempty" jsonschema:"max TCP velocity in automatic mode, mm/s"`
	SafetyZones      int           `json:"safety_zones" jsonschema:"number of safety zones"`
}

type rawLimitSet struct {
	Joints []struct {
		Position *struct {
			Lower *float64 `json:"lower_limit"`
			Upper *float64 `json:"upper_limit"`
		} `json:"position"`
		Velocity *float64 `json:"velocity"`
	} `json:"joints"`
	TCP *struct {
		Velocity *float64 `json:"velocity"`
	} `json:"tcp"`
}

func roundPtr(v *float64, n int) *float64 {
	if v == nil {
		return nil
	}
	r := round(*v, n)
	return &r
}

func (l rawLimitSet) joints() []jointLimit {
	var out []jointLimit
	for _, j := range l.Joints {
		limit := jointLimit{Velocity: roundPtr(j.Velocity, 4)}
		if j.Position != nil {
			limit.Lower, limit.Upper = roundPtr(j.Position.Lower, 4), roundPtr(j.Position.Upper, 4)
		}
		out = append(out, limit)
	}
	return out
}

func describeMotionGroup(c *novaapi.Client) tool.Registration {
	const name = "describe_motion_group"
	add := func(s *sdk.Server) {
		sdk.AddTool(s, &sdk.Tool{
			Name: name,
			Description: "A motion group's model, mounting, TCPs, payloads, cycle time and joint limits. " +
				"Safety zones are only counted, collider geometry is not returned.",
			Annotations: tool.Read("Describe motion group"),
		}, func(ctx context.Context, _ *sdk.CallToolRequest, in motionGroupInput) (*sdk.CallToolResult, motionGroupDescription, error) {
			if err := tool.Required(name, "cell", in.Cell, "controller", in.Controller, "motion_group", in.MotionGroup); err != nil {
				return tool.Fail[motionGroupDescription](err)
			}
			var raw struct {
				Model    string `json:"motion_group_model"`
				Mounting *Pose  `json:"mounting"`
				TCPs     map[string]struct {
					Name string `json:"name"`
					Pose *Pose  `json:"pose"`
				} `json:"tcps"`
				Payloads map[string]struct {
					Name    string  `json:"name"`
					Payload float64 `json:"payload"`
				} `json:"payloads"`
				SafetyZones     map[string]any `json:"safety_zones"`
				OperationLimits struct {
					Auto rawLimitSet `json:"auto_limits"`
				} `json:"operation_limits"`
				CycleTime    int   `json:"cycle_time"`
				DHParameters []any `json:"dh_parameters"`
			}
			path := controllerPath(in.Cell, in.Controller) + "/motion-groups/" + url.PathEscape(in.MotionGroup) + "/description"
			if err := c.Get(ctx, name, path, fmt.Sprintf("motion group '%s' not found on controller '%s' in cell '%s'", in.MotionGroup, in.Controller, in.Cell), &raw); err != nil {
				return tool.Fail[motionGroupDescription](err)
			}
			out := motionGroupDescription{
				MotionGroup: in.MotionGroup, Model: raw.Model, Mounting: raw.Mounting.rounded(),
				TCPs: []namedPose{}, Payloads: []payloadInfo{}, CycleTimeMs: raw.CycleTime,
				AutoJointLimits: raw.OperationLimits.Auto.joints(), SafetyZones: len(raw.SafetyZones),
			}
			out.JointCount = len(raw.DHParameters)
			if out.JointCount == 0 {
				out.JointCount = len(out.AutoJointLimits)
			}
			if raw.OperationLimits.Auto.TCP != nil {
				out.TCPVelocityLimit = roundPtr(raw.OperationLimits.Auto.TCP.Velocity, 2)
			}
			for _, id := range sortedKeys(raw.TCPs) {
				out.TCPs = append(out.TCPs, namedPose{Name: id, Pose: raw.TCPs[id].Pose.rounded()})
			}
			for _, id := range sortedKeys(raw.Payloads) {
				out.Payloads = append(out.Payloads, payloadInfo{Name: id, Mass: raw.Payloads[id].Payload})
			}
			return tool.OK("motion group described", map[string]any{
				"motion_group": in.MotionGroup, "model": out.Model, "joints": out.JointCount, "tcps": len(out.TCPs),
			}, out)
		})
	}
	return tool.Registration{Toolset: tool.Controllers, ReadOnly: true, Add: add}
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// --- list_coordinate_systems ---

type coordinateSystemsInput struct {
	Cell       string `json:"cell" jsonschema:"cell name (see list_cells)"`
	Controller string `json:"controller" jsonschema:"controller name (see list_controllers)"`
	tool.PageInput
}

type coordinateSystem struct {
	ID        string    `json:"id" jsonschema:"coordinate system id ('' is the world)"`
	Name      string    `json:"name,omitempty"`
	Reference string    `json:"reference,omitempty" jsonschema:"id of the reference coordinate system ('' is the world)"`
	Position  []float64 `json:"position" jsonschema:"[x, y, z] in mm"`
	Rotation  []float64 `json:"orientation" jsonschema:"rotation vector [rx, ry, rz] in rad"`
}

type coordinateSystemsOutput struct {
	CoordinateSystems []coordinateSystem `json:"coordinate_systems"`
	tool.PageInfo
}

func listCoordinateSystems(c *novaapi.Client) tool.Registration {
	const name = "list_coordinate_systems"
	add := func(s *sdk.Server) {
		sdk.AddTool(s, &sdk.Tool{
			Name:        name,
			Description: "List the coordinate systems of a controller with their offsets (mm, rotation vector in rad).",
			Annotations: tool.Read("List coordinate systems"),
		}, func(ctx context.Context, _ *sdk.CallToolRequest, in coordinateSystemsInput) (*sdk.CallToolResult, coordinateSystemsOutput, error) {
			if err := tool.Required(name, "cell", in.Cell, "controller", in.Controller); err != nil {
				return tool.Fail[coordinateSystemsOutput](err)
			}
			var raw []struct {
				ID          string    `json:"coordinate_system"`
				Name        string    `json:"name"`
				Reference   string    `json:"reference_coordinate_system"`
				Position    []float64 `json:"position"`
				Orientation []float64 `json:"orientation"`
			}
			_, err := c.JSON(ctx, novaapi.Call{
				Tool: name, Method: http.MethodGet, Path: controllerPath(in.Cell, in.Controller) + "/coordinate-systems",
				Query: url.Values{"orientation_type": {"ROTATION_VECTOR"}}, NotFound: notFound(in.Cell, in.Controller),
			}, &raw)
			if err != nil {
				return tool.Fail[coordinateSystemsOutput](err)
			}
			all := make([]coordinateSystem, 0, len(raw))
			for _, cs := range raw {
				all = append(all, coordinateSystem{ID: cs.ID, Name: cs.Name, Reference: cs.Reference,
					Position: roundAll(cs.Position, 2), Rotation: roundAll(cs.Orientation, 4)})
			}
			page, info := tool.Page(all, in.PageInput)
			return tool.OK("coordinate systems listed", info.Fields(map[string]any{"controller": in.Controller}),
				coordinateSystemsOutput{CoordinateSystems: page, PageInfo: info})
		})
	}
	return tool.Registration{Toolset: tool.Controllers, ReadOnly: true, Add: add}
}

// --- I/O ---

var directions = map[string]string{"input": "IO_TYPE_INPUT", "output": "IO_TYPE_OUTPUT"}
var valueTypes = map[string]string{"boolean": "IO_VALUE_BOOLEAN", "float": "IO_VALUE_ANALOG_FLOAT", "integer": "IO_VALUE_ANALOG_INTEGER"}

func shortName(m map[string]string, apiValue string) string {
	for k, v := range m {
		if v == apiValue {
			return k
		}
	}
	return apiValue
}

type ioFilter struct {
	Cell       string `json:"cell" jsonschema:"cell name (see list_cells)"`
	Controller string `json:"controller" jsonschema:"controller name (see list_controllers)"`
	Direction  string `json:"direction,omitempty" jsonschema:"only 'input' or 'output'"`
	ValueType  string `json:"value_type,omitempty" jsonschema:"only 'boolean', 'integer' or 'float'"`
	Group      string `json:"group,omitempty" jsonschema:"only this I/O group, e.g. standard, tool, configurable"`
	tool.PageInput
}

type ioDescription struct {
	IO        string `json:"io" jsonschema:"I/O id, used by get_io_values and set_output_values"`
	Name      string `json:"name,omitempty"`
	Group     string `json:"group,omitempty"`
	Direction string `json:"direction" jsonschema:"input or output"`
	ValueType string `json:"value_type" jsonschema:"boolean, integer or float"`
	Unit      string `json:"unit,omitempty"`
	Min       any    `json:"min,omitempty"`
	Max       any    `json:"max,omitempty"`
}

type rawIODescription struct {
	IO        string `json:"io"`
	Name      string `json:"name"`
	Group     string `json:"group"`
	Direction string `json:"direction"`
	ValueType string `json:"value_type"`
	Unit      string `json:"unit"`
	Min       *struct {
		Value any `json:"value"`
	} `json:"min"`
	Max *struct {
		Value any `json:"value"`
	} `json:"max"`
}

func (f ioFilter) query(toolName string) (url.Values, *novaapi.Error) {
	q := url.Values{}
	if f.Direction != "" {
		d, ok := directions[f.Direction]
		if !ok {
			return nil, &novaapi.Error{Code: novaapi.CodeInvalidArgument, Message: toolName + ": direction must be 'input' or 'output'"}
		}
		q.Set("direction", d)
	}
	if f.ValueType != "" {
		v, ok := valueTypes[f.ValueType]
		if !ok {
			return nil, &novaapi.Error{Code: novaapi.CodeInvalidArgument, Message: toolName + ": value_type must be 'boolean', 'integer' or 'float'"}
		}
		q.Set("value_type", v)
	}
	if f.Group != "" {
		q.Set("group", f.Group)
	}
	return q, nil
}

func fetchIODescriptions(ctx context.Context, c *novaapi.Client, toolName, cell, controller string, q url.Values) ([]rawIODescription, *novaapi.Error) {
	var raw []rawIODescription
	_, err := c.JSON(ctx, novaapi.Call{Tool: toolName, Method: http.MethodGet,
		Path: controllerPath(cell, controller) + "/ios/description", Query: q, NotFound: notFound(cell, controller)}, &raw)
	slices.SortFunc(raw, func(a, b rawIODescription) int { return compareIO(a.IO, b.IO) })
	return raw, err
}

// compareIO sorts "digital_out[10]" after "digital_out[2]".
func compareIO(a, b string) int {
	pa, na := splitIndex(a)
	pb, nb := splitIndex(b)
	if pa != pb {
		if pa < pb {
			return -1
		}
		return 1
	}
	return na - nb
}

func splitIndex(io string) (string, int) {
	if l := len(io); l > 2 && io[l-1] == ']' {
		for i := l - 2; i >= 0; i-- {
			if io[i] == '[' {
				if n, err := strconv.Atoi(io[i+1 : l-1]); err == nil {
					return io[:i], n
				}
				break
			}
		}
	}
	return io, -1
}

type ioDescriptionsOutput struct {
	IOs []ioDescription `json:"ios"`
	tool.PageInfo
}

func listIODescriptions(c *novaapi.Client) tool.Registration {
	const name = "list_io_descriptions"
	add := func(s *sdk.Server) {
		sdk.AddTool(s, &sdk.Tool{
			Name:        name,
			Description: "List the inputs and outputs of a controller: id, direction, value type, unit and range. Filter by direction, value type or group.",
			Annotations: tool.Read("List I/O descriptions"),
		}, func(ctx context.Context, _ *sdk.CallToolRequest, in ioFilter) (*sdk.CallToolResult, ioDescriptionsOutput, error) {
			if err := tool.Required(name, "cell", in.Cell, "controller", in.Controller); err != nil {
				return tool.Fail[ioDescriptionsOutput](err)
			}
			q, err := in.query(name)
			if err != nil {
				return tool.Fail[ioDescriptionsOutput](err)
			}
			raw, err := fetchIODescriptions(ctx, c, name, in.Cell, in.Controller, q)
			if err != nil {
				return tool.Fail[ioDescriptionsOutput](err)
			}
			all := make([]ioDescription, 0, len(raw))
			for _, d := range raw {
				desc := ioDescription{IO: d.IO, Name: d.Name, Group: d.Group, Direction: shortName(directions, d.Direction),
					ValueType: shortName(valueTypes, d.ValueType)}
				if d.Unit != "UNIT_NONE" {
					desc.Unit = d.Unit
				}
				if d.Min != nil {
					desc.Min = d.Min.Value
				}
				if d.Max != nil {
					desc.Max = d.Max.Value
				}
				all = append(all, desc)
			}
			page, info := tool.Page(all, in.PageInput)
			return tool.OK("I/O descriptions listed", info.Fields(map[string]any{"controller": in.Controller}),
				ioDescriptionsOutput{IOs: page, PageInfo: info})
		})
	}
	return tool.Registration{Toolset: tool.Controllers, ReadOnly: true, Add: add}
}

type ioValuesInput struct {
	ioFilter
	IOs []string `json:"ios,omitempty" jsonschema:"I/O ids to read; empty reads all that match the filters"`
}

type ioValue struct {
	IO        string `json:"io"`
	ValueType string `json:"value_type" jsonschema:"boolean, integer or float"`
	Value     any    `json:"value" jsonschema:"boolean, number, or integer as string"`
}

type ioValuesOutput struct {
	Values []ioValue `json:"values"`
	tool.PageInfo
}

func getIOValues(c *novaapi.Client) tool.Registration {
	const name = "get_io_values"
	add := func(s *sdk.Server) {
		sdk.AddTool(s, &sdk.Tool{
			Name: name,
			Description: "Read current I/O values of a controller, by id (see list_io_descriptions) or all matching " +
				"the filters. Integer values are strings.",
			Annotations: tool.Read("Get I/O values"),
		}, func(ctx context.Context, _ *sdk.CallToolRequest, in ioValuesInput) (*sdk.CallToolResult, ioValuesOutput, error) {
			if err := tool.Required(name, "cell", in.Cell, "controller", in.Controller); err != nil {
				return tool.Fail[ioValuesOutput](err)
			}
			ids := slices.Clone(in.IOs)
			if len(ids) == 0 {
				// the API returns nothing without ids
				q, err := in.query(name)
				if err != nil {
					return tool.Fail[ioValuesOutput](err)
				}
				raw, err := fetchIODescriptions(ctx, c, name, in.Cell, in.Controller, q)
				if err != nil {
					return tool.Fail[ioValuesOutput](err)
				}
				for _, d := range raw {
					ids = append(ids, d.IO)
				}
			}
			page, info := tool.Page(ids, in.PageInput)
			out := ioValuesOutput{Values: []ioValue{}, PageInfo: info}
			if len(page) > 0 {
				var raw []ioValue
				_, err := c.JSON(ctx, novaapi.Call{Tool: name, Method: http.MethodGet,
					Path: controllerPath(in.Cell, in.Controller) + "/ios/values", Query: url.Values{"ios": page},
					NotFound: notFound(in.Cell, in.Controller)}, &raw)
				if err != nil {
					return tool.Fail[ioValuesOutput](err)
				}
				out.Values = raw
			}
			return tool.OK("I/O values read", info.Fields(map[string]any{"controller": in.Controller}), out)
		})
	}
	return tool.Registration{Toolset: tool.Controllers, ReadOnly: true, Add: add}
}

// --- set_output_values ---

type outputValue struct {
	IO    string `json:"io" jsonschema:"output id (see list_io_descriptions with direction=output)"`
	Value any    `json:"value" jsonschema:"true/false for boolean outputs, a number for float and integer outputs"`
}

type setOutputsInput struct {
	Cell       string        `json:"cell" jsonschema:"cell name (see list_cells)"`
	Controller string        `json:"controller" jsonschema:"controller name; must be a virtual controller"`
	Values     []outputValue `json:"values" jsonschema:"outputs to set"`
}

type setOutputsOutput struct {
	Values []ioValue `json:"values" jsonschema:"the values that were set"`
}

func setOutputValues(c *novaapi.Client) tool.Registration {
	const name = "set_output_values"
	add := func(s *sdk.Server) {
		sdk.AddTool(s, &sdk.Tool{
			Name: name,
			Description: "Set outputs of a VIRTUAL controller. Physical controllers are refused with APPROVAL_REQUIRED. " +
				"Each output must exist and the value must match its type (see list_io_descriptions).",
			Annotations: tool.Write("Set output values", true),
		}, func(ctx context.Context, _ *sdk.CallToolRequest, in setOutputsInput) (*sdk.CallToolResult, setOutputsOutput, error) {
			if err := tool.Required(name, "cell", in.Cell, "controller", in.Controller); err != nil {
				return tool.Fail[setOutputsOutput](err)
			}
			if len(in.Values) == 0 {
				return tool.Invalid[setOutputsOutput](name, "'values' must name at least one output")
			}
			if err := c.RequireVirtual(ctx, name, in.Cell, in.Controller); err != nil {
				return tool.Fail[setOutputsOutput](err)
			}
			raw, err := fetchIODescriptions(ctx, c, name, in.Cell, in.Controller, url.Values{"direction": {"IO_TYPE_OUTPUT"}})
			if err != nil {
				return tool.Fail[setOutputsOutput](err)
			}
			outputs := map[string]rawIODescription{}
			for _, d := range raw {
				outputs[d.IO] = d
			}
			body := make([]ioValue, 0, len(in.Values))
			for _, v := range in.Values {
				d, ok := outputs[v.IO]
				if !ok {
					return tool.Invalid[setOutputsOutput](name, "'%s' is not an output of controller '%s' (see list_io_descriptions with direction=output)", v.IO, in.Controller)
				}
				value, ok := convertValue(d.ValueType, v.Value)
				if !ok {
					return tool.Invalid[setOutputsOutput](name, "output '%s' takes a %s value, got %v", v.IO, shortName(valueTypes, d.ValueType), v.Value)
				}
				body = append(body, ioValue{IO: v.IO, ValueType: shortName(valueTypes, d.ValueType), Value: value})
			}
			if _, err := c.JSON(ctx, novaapi.Call{Tool: name, Method: http.MethodPut,
				Path: controllerPath(in.Cell, in.Controller) + "/ios/values", Body: body, NotFound: notFound(in.Cell, in.Controller)}, nil); err != nil {
				return tool.Fail[setOutputsOutput](err)
			}
			return tool.OK("outputs set", map[string]any{"controller": in.Controller, "count": len(body)}, setOutputsOutput{Values: body})
		})
	}
	return tool.Registration{Toolset: tool.Controllers, ReadOnly: false, Add: add}
}

// convertValue checks v against an I/O value type and returns it in the wire format
// (integers are strings).
func convertValue(valueType string, v any) (any, bool) {
	switch valueType {
	case "IO_VALUE_BOOLEAN":
		b, ok := v.(bool)
		return b, ok
	case "IO_VALUE_ANALOG_FLOAT":
		switch n := v.(type) {
		case float64:
			return n, true
		case string:
			f, err := strconv.ParseFloat(n, 64)
			return f, err == nil
		}
	case "IO_VALUE_ANALOG_INTEGER":
		switch n := v.(type) {
		case float64:
			if n != math.Trunc(n) {
				return nil, false
			}
			return strconv.FormatInt(int64(n), 10), true
		case string:
			if _, err := strconv.ParseInt(n, 10, 64); err == nil {
				return n, true
			}
		}
	}
	return nil, false
}

// --- set_default_mode ---

type setModeInput struct {
	Cell       string `json:"cell" jsonschema:"cell name (see list_cells)"`
	Controller string `json:"controller" jsonschema:"controller name; must be a virtual controller"`
	Mode       string `json:"mode" jsonschema:"'monitor' (read only) or 'control' (commands allowed)"`
}

type setModeOutput struct {
	Controller string `json:"controller"`
	Mode       string `json:"mode"`
}

func setDefaultMode(c *novaapi.Client) tool.Registration {
	const name = "set_default_mode"
	add := func(s *sdk.Server) {
		sdk.AddTool(s, &sdk.Tool{
			Name: name,
			Description: "Switch the default mode of a VIRTUAL controller between 'monitor' and 'control'. Physical " +
				"controllers are refused with APPROVAL_REQUIRED. Verify with get_controller_state.",
			Annotations: tool.Write("Set default mode", true),
		}, func(ctx context.Context, _ *sdk.CallToolRequest, in setModeInput) (*sdk.CallToolResult, setModeOutput, error) {
			if err := tool.Required(name, "cell", in.Cell, "controller", in.Controller, "mode", in.Mode); err != nil {
				return tool.Fail[setModeOutput](err)
			}
			modes := map[string]string{"monitor": "MODE_MONITOR", "control": "MODE_CONTROL"}
			mode, ok := modes[in.Mode]
			if !ok {
				return tool.Invalid[setModeOutput](name, "mode must be 'monitor' or 'control'")
			}
			if err := c.RequireVirtual(ctx, name, in.Cell, in.Controller); err != nil {
				return tool.Fail[setModeOutput](err)
			}
			if _, err := c.JSON(ctx, novaapi.Call{Tool: name, Method: http.MethodPut,
				Path: controllerPath(in.Cell, in.Controller) + "/mode", Query: url.Values{"mode": {mode}},
				NotFound: notFound(in.Cell, in.Controller)}, nil); err != nil {
				return tool.Fail[setModeOutput](err)
			}
			return tool.OK("default mode set", map[string]any{"controller": in.Controller, "mode": in.Mode},
				setModeOutput{Controller: in.Controller, Mode: in.Mode})
		})
	}
	return tool.Registration{Toolset: tool.Controllers, ReadOnly: false, Add: add}
}

// --- discover_controllers_on_network ---

type discoverInput struct {
	Interface string `json:"interface" jsonschema:"network interface id of the instance (see get_network_interfaces)"`
	CIDR      string `json:"cidr" jsonschema:"IP range to scan, e.g. 192.168.1.0/24"`
	TimeoutMs int    `json:"timeout_ms,omitempty" jsonschema:"scan timeout in ms (default: the instance's)"`
	tool.PageInput
}

type networkDevice struct {
	IP     string `json:"ip"`
	MAC    string `json:"mac"`
	Vendor string `json:"vendor,omitempty"`
}

type discoverOutput struct {
	Devices []networkDevice `json:"devices"`
	tool.PageInfo
}

func discoverControllersOnNetwork(c *novaapi.Client) tool.Registration {
	const name = "discover_controllers_on_network"
	add := func(s *sdk.Server) {
		sdk.AddTool(s, &sdk.Tool{
			Name: name,
			Description: "ARP-scan an IP range from the instance and list the devices found (IP, MAC, vendor), to find " +
				"robot controllers before a human commissions them. Only for on-prem instances with network access.",
			Annotations: tool.Annotations("Discover controllers on the network", true, false, false, true),
		}, func(ctx context.Context, _ *sdk.CallToolRequest, in discoverInput) (*sdk.CallToolResult, discoverOutput, error) {
			if err := tool.Required(name, "interface", in.Interface, "cidr", in.CIDR); err != nil {
				return tool.Fail[discoverOutput](err)
			}
			q := url.Values{"interface": {in.Interface}, "cidr": {in.CIDR}}
			if in.TimeoutMs > 0 {
				q.Set("timeout", strconv.Itoa(in.TimeoutMs))
			}
			var raw []networkDevice
			if _, err := c.JSON(ctx, novaapi.Call{Tool: name, Method: http.MethodGet, Path: "/system/network/arp-scan", Query: q,
				NotFound: fmt.Sprintf("network interface '%s' not found", in.Interface)}, &raw); err != nil {
				return tool.Fail[discoverOutput](err)
			}
			slices.SortFunc(raw, func(a, b networkDevice) int { return compareIP(a.IP, b.IP) })
			page, info := tool.Page(raw, in.PageInput)
			return tool.OK("network scanned", info.Fields(map[string]any{"cidr": in.CIDR}), discoverOutput{Devices: page, PageInfo: info})
		})
	}
	return tool.Registration{Toolset: tool.Controllers, ReadOnly: true, Add: add}
}

func compareIP(a, b string) int {
	pa, pb := parseIPv4(a), parseIPv4(b)
	if pa != pb {
		if pa < pb {
			return -1
		}
		return 1
	}
	if a < b {
		return -1
	} else if a > b {
		return 1
	}
	return 0
}

func parseIPv4(s string) uint64 {
	var n uint64
	var part uint64
	parts := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == '.' {
			n = n<<8 | part
			part = 0
			parts++
			continue
		}
		if s[i] < '0' || s[i] > '9' {
			return math.MaxUint64
		}
		part = part*10 + uint64(s[i]-'0')
	}
	if parts != 4 {
		return math.MaxUint64
	}
	return n
}
