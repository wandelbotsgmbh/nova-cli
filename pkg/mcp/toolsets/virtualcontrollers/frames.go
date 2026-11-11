package virtualcontrollers

import (
	"context"
	"fmt"
	"net/url"
	"strconv"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/novaapi"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/tool"
)

// --- coordinate systems ---

type coordinateSystem struct {
	CoordinateSystem          string    `json:"coordinate_system"`
	Name                      string    `json:"name,omitempty"`
	ReferenceCoordinateSystem string    `json:"reference_coordinate_system,omitempty"`
	Position                  []float64 `json:"position,omitempty"`
	Orientation               []float64 `json:"orientation,omitempty"`
	OrientationType           string    `json:"orientation_type,omitempty"`
}

type listCoordinateSystemsOutput struct {
	CoordinateSystems []coordinateSystem `json:"coordinate_systems"`
}

func listCoordinateSystemsTool(c *novaapi.Client) tool.Registration {
	t := &sdk.Tool{
		Name:        "list_virtual_coordinate_systems",
		Description: "List the coordinate systems defined on a virtual controller. Refused for physical controllers.",
		Annotations: tool.Read("List virtual coordinate systems"),
	}
	return add(c, true, t, func(ctx context.Context, _ *sdk.CallToolRequest, in controllerInput) (*sdk.CallToolResult, listCoordinateSystemsOutput, error) {
		systems := []coordinateSystem{}
		if err := c.Get(ctx, t.Name, basePath(in.Cell, in.Controller)+"/coordinate-systems",
			controllerNotFound(in.Cell, in.Controller), &systems); err != nil {
			return tool.Fail[listCoordinateSystemsOutput](err)
		}
		return tool.OK("coordinate systems listed", map[string]any{"controller": in.Controller, "count": len(systems)},
			listCoordinateSystemsOutput{CoordinateSystems: systems})
	})
}

type addCoordinateSystemInput struct {
	Cell                      string    `json:"cell" jsonschema:"cell name (see list_cells)"`
	Controller                string    `json:"controller" jsonschema:"virtual controller name (see list_controllers)"`
	CoordinateSystem          string    `json:"coordinate_system" jsonschema:"id of the coordinate system to create or replace"`
	Name                      string    `json:"name,omitempty" jsonschema:"display name"`
	ReferenceCoordinateSystem string    `json:"reference_coordinate_system,omitempty" jsonschema:"coordinate system the pose is given in; empty is the world"`
	Position                  []float64 `json:"position" jsonschema:"[x, y, z] in millimeters"`
	Orientation               []float64 `json:"orientation,omitempty" jsonschema:"rotation as given by orientation_type; default a rotation vector [rx, ry, rz] in radians"`
	OrientationType           string    `json:"orientation_type,omitempty" jsonschema:"ROTATION_VECTOR (default), QUATERNION or EULER_ANGLES_*"`
}

func (in addCoordinateSystemInput) target() (string, string) { return in.Cell, in.Controller }

func addCoordinateSystemTool(c *novaapi.Client) tool.Registration {
	t := &sdk.Tool{
		Name: "add_virtual_coordinate_system",
		Description: "Add a coordinate system to a virtual controller, or replace the one with that id. Refused " +
			"for physical controllers.",
		Annotations: tool.Write("Add virtual coordinate system", true),
	}
	return add(c, false, t, func(ctx context.Context, _ *sdk.CallToolRequest, in addCoordinateSystemInput) (*sdk.CallToolResult, writeOutput, error) {
		if err := tool.Required(t.Name, "coordinate_system", in.CoordinateSystem); err != nil {
			return tool.Fail[writeOutput](err)
		}
		if err := checkPose(t.Name, in.Position, in.Orientation); err != nil {
			return tool.Fail[writeOutput](err)
		}
		body := map[string]any{"position": in.Position}
		setOptional(body, "name", in.Name)
		setOptional(body, "reference_coordinate_system", in.ReferenceCoordinateSystem)
		setOrientation(body, in.Orientation, in.OrientationType)
		status, err := c.JSON(ctx, novaapi.Call{
			Tool: t.Name, Method: "PUT", Body: body,
			Path:     basePath(in.Cell, in.Controller) + "/coordinate-systems/" + url.PathEscape(in.CoordinateSystem),
			NotFound: controllerNotFound(in.Cell, in.Controller),
		}, nil)
		if err != nil {
			return tool.Fail[writeOutput](err)
		}
		out := writeOutput{Controller: in.Controller, Status: novaapi.DeployStatus(status)}
		return tool.OK("coordinate system added", map[string]any{"controller": in.Controller, "coordinate_system": in.CoordinateSystem, "status": out.Status}, out)
	})
}

type deleteCoordinateSystemInput struct {
	Cell             string `json:"cell" jsonschema:"cell name (see list_cells)"`
	Controller       string `json:"controller" jsonschema:"virtual controller name (see list_controllers)"`
	CoordinateSystem string `json:"coordinate_system" jsonschema:"coordinate system id (see list_virtual_coordinate_systems)"`
	DeleteDependent  bool   `json:"delete_dependent,omitempty" jsonschema:"also delete coordinate systems that reference this one (default false)"`
}

func (in deleteCoordinateSystemInput) target() (string, string) { return in.Cell, in.Controller }

func deleteCoordinateSystemTool(c *novaapi.Client) tool.Registration {
	t := &sdk.Tool{
		Name: "delete_virtual_coordinate_system",
		Description: "Delete a coordinate system of a virtual controller; with delete_dependent also the ones " +
			"referencing it. Refused for physical controllers.",
		Annotations: tool.Destructive("Delete virtual coordinate system"),
	}
	return add(c, false, t, func(ctx context.Context, _ *sdk.CallToolRequest, in deleteCoordinateSystemInput) (*sdk.CallToolResult, writeOutput, error) {
		if err := tool.Required(t.Name, "coordinate_system", in.CoordinateSystem); err != nil {
			return tool.Fail[writeOutput](err)
		}
		status, err := c.JSON(ctx, novaapi.Call{
			Tool: t.Name, Method: "DELETE",
			Path:     basePath(in.Cell, in.Controller) + "/coordinate-systems/" + url.PathEscape(in.CoordinateSystem),
			Query:    url.Values{"delete_dependent": {strconv.FormatBool(in.DeleteDependent)}},
			NotFound: fmt.Sprintf("coordinate system '%s' not found on virtual controller '%s'", in.CoordinateSystem, in.Controller),
		}, nil)
		if err != nil {
			return tool.Fail[writeOutput](err)
		}
		out := writeOutput{Controller: in.Controller, Status: novaapi.DeployStatus(status)}
		return tool.OK("coordinate system deleted", map[string]any{"controller": in.Controller, "coordinate_system": in.CoordinateSystem, "status": out.Status}, out)
	})
}

// --- safety zones ---

type safetyZone struct {
	ID         int       `json:"id"`
	Name       string    `json:"name"`
	Priority   int       `json:"priority"`
	Restricted bool      `json:"restricted" jsonschema:"true if the robot must not enter the zone"`
	Shape      string    `json:"shape" jsonschema:"sphere, capsule, lozenge, box, prism or plane"`
	Inverted   bool      `json:"inverted" jsonschema:"true if the zone is everything outside the shape"`
	Center     []float64 `json:"center,omitempty" jsonschema:"sphere center [x, y, z] in millimeters"`
	Top        []float64 `json:"top,omitempty" jsonschema:"capsule top [x, y, z] in millimeters"`
	Bottom     []float64 `json:"bottom,omitempty" jsonschema:"capsule bottom [x, y, z] in millimeters"`
	Radius     float64   `json:"radius,omitempty" jsonschema:"sphere or capsule radius in millimeters"`
}

// apiSafetyZone reads only the identity and the sphere/capsule parameters; other shapes
// are reported by name.
type apiSafetyZone struct {
	ID         int    `json:"id"`
	Name       string `json:"name"`
	Priority   int    `json:"priority"`
	Restricted bool   `json:"restricted"`
	Geometry   struct {
		Inverted bool `json:"inverted"`
		Sphere   *struct {
			Center []float64 `json:"center"`
			Radius float64   `json:"radius"`
		} `json:"sphere"`
		Capsule *struct {
			Top    []float64 `json:"top"`
			Bottom []float64 `json:"bottom"`
			Radius float64   `json:"radius"`
		} `json:"capsule"`
		Lozenge *struct{} `json:"lozenge"`
		Box     *struct{} `json:"box"`
		Prism   *struct{} `json:"prism"`
		Plane   *struct{} `json:"plane"`
	} `json:"geometry"`
}

func projectSafetyZone(z apiSafetyZone) safetyZone {
	out := safetyZone{ID: z.ID, Name: z.Name, Priority: z.Priority, Restricted: z.Restricted, Inverted: z.Geometry.Inverted}
	g := z.Geometry
	switch {
	case g.Sphere != nil:
		out.Shape, out.Center, out.Radius = "sphere", g.Sphere.Center, g.Sphere.Radius
	case g.Capsule != nil:
		out.Shape, out.Top, out.Bottom, out.Radius = "capsule", g.Capsule.Top, g.Capsule.Bottom, g.Capsule.Radius
	case g.Lozenge != nil:
		out.Shape = "lozenge"
	case g.Box != nil:
		out.Shape = "box"
	case g.Prism != nil:
		out.Shape = "prism"
	case g.Plane != nil:
		out.Shape = "plane"
	}
	return out
}

type listSafetyZonesOutput struct {
	SafetyZones []safetyZone `json:"safety_zones"`
}

func listSafetyZonesTool(c *novaapi.Client) tool.Registration {
	t := &sdk.Tool{
		Name:        "list_virtual_safety_zones",
		Description: "List the simulated safety zones of a virtual controller. Refused for physical controllers.",
		Annotations: tool.Read("List virtual safety zones"),
	}
	return add(c, true, t, func(ctx context.Context, _ *sdk.CallToolRequest, in controllerInput) (*sdk.CallToolResult, listSafetyZonesOutput, error) {
		var zones struct {
			SafetyZones []apiSafetyZone `json:"safety_zones"`
		}
		if err := c.Get(ctx, t.Name, basePath(in.Cell, in.Controller)+"/safety-zones",
			controllerNotFound(in.Cell, in.Controller), &zones); err != nil {
			return tool.Fail[listSafetyZonesOutput](err)
		}
		out := listSafetyZonesOutput{SafetyZones: []safetyZone{}}
		for _, z := range zones.SafetyZones {
			out.SafetyZones = append(out.SafetyZones, projectSafetyZone(z))
		}
		return tool.OK("safety zones listed", map[string]any{"controller": in.Controller, "count": len(out.SafetyZones)}, out)
	})
}

type addSafetyZoneInput struct {
	Cell       string    `json:"cell" jsonschema:"cell name (see list_cells)"`
	Controller string    `json:"controller" jsonschema:"virtual controller name (see list_controllers)"`
	ID         int       `json:"id" jsonschema:"numeric zone id, unique on the controller"`
	Name       string    `json:"name" jsonschema:"zone name"`
	Shape      string    `json:"shape" jsonschema:"sphere (center, radius) or capsule (top, bottom, radius)"`
	Center     []float64 `json:"center,omitempty" jsonschema:"sphere center [x, y, z] in millimeters"`
	Top        []float64 `json:"top,omitempty" jsonschema:"capsule top [x, y, z] in millimeters"`
	Bottom     []float64 `json:"bottom,omitempty" jsonschema:"capsule bottom [x, y, z] in millimeters"`
	Radius     float64   `json:"radius" jsonschema:"radius in millimeters"`
	Restricted bool      `json:"restricted,omitempty" jsonschema:"true if the robot must not enter the zone"`
	Inverted   bool      `json:"inverted,omitempty" jsonschema:"true makes the zone everything outside the shape"`
	Priority   int       `json:"priority,omitempty"`
}

func (in addSafetyZoneInput) target() (string, string) { return in.Cell, in.Controller }

func addSafetyZoneTool(c *novaapi.Client) tool.Registration {
	t := &sdk.Tool{
		Name: "add_virtual_safety_zone",
		Description: "Add a simulated safety zone (sphere or capsule) to a virtual controller, e.g. to test " +
			"planning around it. Refused for physical controllers.",
		Annotations: tool.Write("Add virtual safety zone", false),
	}
	return add(c, false, t, func(ctx context.Context, _ *sdk.CallToolRequest, in addSafetyZoneInput) (*sdk.CallToolResult, writeOutput, error) {
		if err := tool.Required(t.Name, "name", in.Name); err != nil {
			return tool.Fail[writeOutput](err)
		}
		if in.ID < 0 || in.Radius <= 0 {
			return tool.Invalid[writeOutput](t.Name, "'id' must be >= 0 and 'radius' > 0")
		}
		geometry := map[string]any{"id": in.Name, "inverted": in.Inverted}
		switch in.Shape {
		case "sphere":
			if len(in.Center) != 3 {
				return tool.Invalid[writeOutput](t.Name, "a sphere needs 'center' [x, y, z]")
			}
			geometry["sphere"] = map[string]any{"center": in.Center, "radius": in.Radius}
		case "capsule":
			if len(in.Top) != 3 || len(in.Bottom) != 3 {
				return tool.Invalid[writeOutput](t.Name, "a capsule needs 'top' and 'bottom' [x, y, z]")
			}
			geometry["capsule"] = map[string]any{"top": in.Top, "bottom": in.Bottom, "radius": in.Radius}
		default:
			return tool.Invalid[writeOutput](t.Name, "unknown shape '%s', use sphere or capsule", in.Shape)
		}
		status, err := c.JSON(ctx, novaapi.Call{
			Tool: t.Name, Method: "POST", Path: basePath(in.Cell, in.Controller) + "/safety-zones",
			Body:     map[string]any{"id": in.ID, "name": in.Name, "priority": in.Priority, "restricted": in.Restricted, "geometry": geometry},
			NotFound: controllerNotFound(in.Cell, in.Controller),
		}, nil)
		if err != nil {
			return tool.Fail[writeOutput](err)
		}
		out := writeOutput{Controller: in.Controller, Status: novaapi.DeployStatus(status)}
		return tool.OK("safety zone added", map[string]any{"controller": in.Controller, "id": in.ID, "status": out.Status}, out)
	})
}

type deleteSafetyZoneInput struct {
	Cell       string `json:"cell" jsonschema:"cell name (see list_cells)"`
	Controller string `json:"controller" jsonschema:"virtual controller name (see list_controllers)"`
	ID         int    `json:"id" jsonschema:"zone id (see list_virtual_safety_zones)"`
}

func (in deleteSafetyZoneInput) target() (string, string) { return in.Cell, in.Controller }

func deleteSafetyZoneTool(c *novaapi.Client) tool.Registration {
	t := &sdk.Tool{
		Name:        "delete_virtual_safety_zone",
		Description: "Delete a simulated safety zone of a virtual controller. Refused for physical controllers.",
		Annotations: tool.Destructive("Delete virtual safety zone"),
	}
	return add(c, false, t, func(ctx context.Context, _ *sdk.CallToolRequest, in deleteSafetyZoneInput) (*sdk.CallToolResult, writeOutput, error) {
		if in.ID < 0 {
			return tool.Invalid[writeOutput](t.Name, "'id' must be >= 0")
		}
		status, err := c.JSON(ctx, novaapi.Call{
			Tool: t.Name, Method: "DELETE", Path: basePath(in.Cell, in.Controller) + "/safety-zones/" + strconv.Itoa(in.ID),
			NotFound: fmt.Sprintf("safety zone %d not found on virtual controller '%s'", in.ID, in.Controller),
		}, nil)
		if err != nil {
			return tool.Fail[writeOutput](err)
		}
		out := writeOutput{Controller: in.Controller, Status: novaapi.DeployStatus(status)}
		return tool.OK("safety zone deleted", map[string]any{"controller": in.Controller, "id": in.ID, "status": out.Status}, out)
	})
}
