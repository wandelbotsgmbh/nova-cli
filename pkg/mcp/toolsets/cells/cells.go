// Package cells extends the `cells` toolset (list_cells and describe_cell are ported in
// pkg/mcp) with status, updates and the cell lifecycle.
package cells

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/novaapi"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/tool"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/toolsets/system"
)

// Tools returns the tools of this package.
func Tools(env tool.Env) []tool.Registration {
	c := env.Client
	return []tool.Registration{
		getCellStatus(c),
		checkCellUpdate(c),
		deployCell(c),
		setCellStatus(c),
		updateCellVersion(c),
		deleteCell(c),
	}
}

// cellNamePattern is CellName of the API (RFC 1123 label, at most 32 characters).
var cellNamePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,30}[a-z0-9]$`)

func cellPath(cell string) string { return "/cells/" + url.PathEscape(cell) }

func notFound(cell string) string { return fmt.Sprintf("cell '%s' not found", cell) }

type cellInput struct {
	Cell string `json:"cell" jsonschema:"cell name (see list_cells)"`
}

type deployOutput struct {
	Cell   string `json:"cell"`
	Status string `json:"status" jsonschema:"'completed' or 'pending': verify with get_cell_status when pending"`
}

// --- get_cell_status ---

type cellStatusInput struct {
	Cell         string `json:"cell" jsonschema:"cell name (see list_cells)"`
	ProblemsOnly bool   `json:"problems_only,omitempty" jsonschema:"only return services whose severity is not INFO"`
}

type cellStatusOutput struct {
	Cell           string           `json:"cell"`
	OperatingState string           `json:"operating_state" jsonschema:"ACTIVE or INACTIVE"`
	Services       []system.Service `json:"services" jsonschema:"cell services, robot controllers and apps with their phase"`
	Problems       int              `json:"problems"`
}

func getCellStatus(c *novaapi.Client) tool.Registration {
	const name = "get_cell_status"
	add := func(s *sdk.Server) {
		sdk.AddTool(s, &sdk.Tool{
			Name: name,
			Description: "Operating state of a cell (ACTIVE/INACTIVE) and the status of its services, robot controllers and apps. " +
				"Use problems_only to see only what isn't healthy.",
			Annotations: tool.Read("Get cell status"),
		}, func(ctx context.Context, _ *sdk.CallToolRequest, in cellStatusInput) (*sdk.CallToolResult, cellStatusOutput, error) {
			if err := tool.Required(name, "cell", in.Cell); err != nil {
				return tool.Fail[cellStatusOutput](err)
			}
			status, err := system.FetchCellStatus(ctx, c, name, in.Cell)
			if err != nil {
				return tool.Fail[cellStatusOutput](err)
			}
			problems := system.Problems(status.Services)
			services := status.Services
			if in.ProblemsOnly {
				services = problems
			}
			out := cellStatusOutput{Cell: in.Cell, OperatingState: status.OperatingState, Services: services, Problems: len(problems)}
			return tool.OK("cell status", map[string]any{
				"cell": in.Cell, "operating_state": status.OperatingState, "services": len(services), "problems": len(problems),
			}, out)
		})
	}
	return tool.Registration{Toolset: tool.Cells, ReadOnly: true, Add: add}
}

// --- check_cell_update ---

type cellChannelInput struct {
	Cell    string `json:"cell" jsonschema:"cell name (see list_cells)"`
	Channel string `json:"channel,omitempty" jsonschema:"release channel: 'stable' (newest patch of the current version, default) or 'next' (latest version)"`
}

type cellUpdateOutput struct {
	Cell             string `json:"cell"`
	UpdateAvailable  bool   `json:"update_available"`
	AvailableVersion string `json:"available_version,omitempty" jsonschema:"never higher than the system version"`
	Channel          string `json:"channel"`
}

func checkCellUpdate(c *novaapi.Client) tool.Registration {
	const name = "check_cell_update"
	add := func(s *sdk.Server) {
		sdk.AddTool(s, &sdk.Tool{
			Name:        name,
			Description: "Check whether a newer NOVA version is available for a cell (up to the system version). Only checks; update_cell_version applies it.",
			Annotations: tool.Read("Check cell update"),
		}, func(ctx context.Context, _ *sdk.CallToolRequest, in cellChannelInput) (*sdk.CallToolResult, cellUpdateOutput, error) {
			if err := tool.Required(name, "cell", in.Cell); err != nil {
				return tool.Fail[cellUpdateOutput](err)
			}
			channel, err := system.ValidChannel(name, in.Channel)
			if err != nil {
				return tool.Fail[cellUpdateOutput](err)
			}
			available, err := system.CheckUpdate(ctx, c, name, cellPath(in.Cell)+"/update", channel, notFound(in.Cell))
			if err != nil {
				return tool.Fail[cellUpdateOutput](err)
			}
			out := cellUpdateOutput{Cell: in.Cell, Channel: channel, AvailableVersion: available, UpdateAvailable: available != ""}
			sentence := "no update available"
			if out.UpdateAvailable {
				sentence = "update available"
			}
			return tool.OK(sentence, map[string]any{"cell": in.Cell, "available": available, "channel": channel}, out)
		})
	}
	return tool.Registration{Toolset: tool.Cells, ReadOnly: true, Add: add}
}

// --- deploy_cell ---

type deployCellInput struct {
	Name        string `json:"name" jsonschema:"new cell name: lowercase letters, digits and '-', starting with a letter, 2-32 characters"`
	Description string `json:"description,omitempty" jsonschema:"optional description"`
	Version     string `json:"version,omitempty" jsonschema:"NOVA version of the cell; default is the system version, never higher"`
}

func deployCell(c *novaapi.Client) tool.Registration {
	const name = "deploy_cell"
	add := func(s *sdk.Server) {
		sdk.AddTool(s, &sdk.Tool{
			Name: name,
			Description: "Create a new, empty robot cell. Add controllers (create_virtual_controller) and apps afterwards. " +
				"Returns status 'pending' while the instance rolls it out: verify with get_cell_status.",
			Annotations: tool.Write("Deploy cell", false),
		}, func(ctx context.Context, _ *sdk.CallToolRequest, in deployCellInput) (*sdk.CallToolResult, deployOutput, error) {
			if err := tool.Required(name, "name", in.Name); err != nil {
				return tool.Fail[deployOutput](err)
			}
			if !cellNamePattern.MatchString(in.Name) {
				return tool.Invalid[deployOutput](name, "cell name '%s' is invalid: lowercase letters, digits and '-', starting with a letter, 2-32 characters", in.Name)
			}
			body := map[string]any{"name": in.Name}
			if in.Description != "" {
				body["description"] = in.Description
			}
			if in.Version != "" {
				body["version"] = in.Version
			}
			status, err := c.JSON(ctx, novaapi.Call{Tool: name, Method: http.MethodPost, Path: "/cells", Body: body}, nil)
			if err != nil {
				if err.Code == novaapi.CodeAlreadyExists {
					err.Message = fmt.Sprintf("%s: a cell named '%s' already exists", name, in.Name)
				}
				return tool.Fail[deployOutput](err)
			}
			out := deployOutput{Cell: in.Name, Status: novaapi.DeployStatus(status)}
			return tool.OK("cell deployment "+out.Status, map[string]any{"cell": in.Name, "status": out.Status}, out)
		})
	}
	return tool.Registration{Toolset: tool.Cells, ReadOnly: false, Add: add}
}

// --- set_cell_status ---

type setCellStatusInput struct {
	Cell           string `json:"cell" jsonschema:"cell name (see list_cells)"`
	OperatingState string `json:"operating_state" jsonschema:"ACTIVE starts the cell's services, INACTIVE stops them (controllers disconnect, apps stop)"`
}

type setCellStatusOutput struct {
	Cell           string `json:"cell"`
	OperatingState string `json:"operating_state"`
	Status         string `json:"status" jsonschema:"'pending' until the change is rolled out: verify with get_cell_status"`
}

func setCellStatus(c *novaapi.Client) tool.Registration {
	const name = "set_cell_status"
	add := func(s *sdk.Server) {
		sdk.AddTool(s, &sdk.Tool{
			Name: name,
			Description: "Activate or deactivate a cell. INACTIVE stops all its services: controllers disconnect and apps stop. " +
				"Confirm with the user before deactivating, naming the cell.",
			Annotations: tool.Write("Set cell status", true),
		}, func(ctx context.Context, _ *sdk.CallToolRequest, in setCellStatusInput) (*sdk.CallToolResult, setCellStatusOutput, error) {
			if err := tool.Required(name, "cell", in.Cell, "operating_state", in.OperatingState); err != nil {
				return tool.Fail[setCellStatusOutput](err)
			}
			if in.OperatingState != "ACTIVE" && in.OperatingState != "INACTIVE" {
				return tool.Invalid[setCellStatusOutput](name, "operating_state must be ACTIVE or INACTIVE")
			}
			status, err := c.JSON(ctx, novaapi.Call{Tool: name, Method: http.MethodPut, Path: cellPath(in.Cell) + "/status",
				Query: url.Values{"operating_state": {in.OperatingState}}, NotFound: notFound(in.Cell)}, nil)
			if err != nil {
				return tool.Fail[setCellStatusOutput](err)
			}
			out := setCellStatusOutput{Cell: in.Cell, OperatingState: in.OperatingState, Status: novaapi.DeployStatus(status)}
			return tool.OK("cell status change "+out.Status, map[string]any{
				"cell": in.Cell, "operating_state": in.OperatingState, "status": out.Status,
			}, out)
		})
	}
	return tool.Registration{Toolset: tool.Cells, ReadOnly: false, Add: add}
}

// --- update_cell_version ---

func updateCellVersion(c *novaapi.Client) tool.Registration {
	const name = "update_cell_version"
	add := func(s *sdk.Server) {
		sdk.AddTool(s, &sdk.Tool{
			Name: name,
			Description: "Update a cell's NOVA version to the newest one on a release channel (never above the system version). " +
				"The cell's services restart. Check first with check_cell_update and confirm with the user.",
			Annotations: tool.Write("Update cell version", true),
		}, func(ctx context.Context, _ *sdk.CallToolRequest, in cellChannelInput) (*sdk.CallToolResult, deployOutput, error) {
			if err := tool.Required(name, "cell", in.Cell); err != nil {
				return tool.Fail[deployOutput](err)
			}
			channel, err := system.ValidChannel(name, in.Channel)
			if err != nil {
				return tool.Fail[deployOutput](err)
			}
			status, err := c.JSON(ctx, novaapi.Call{Tool: name, Method: http.MethodPut, Path: cellPath(in.Cell) + "/update",
				Body: map[string]string{"channel": channel}, NotFound: notFound(in.Cell)}, nil)
			if err != nil {
				return tool.Fail[deployOutput](err)
			}
			out := deployOutput{Cell: in.Cell, Status: novaapi.DeployStatus(status)}
			return tool.OK("cell update "+out.Status, map[string]any{"cell": in.Cell, "channel": channel, "status": out.Status}, out)
		})
	}
	return tool.Registration{Toolset: tool.Cells, ReadOnly: false, Add: add}
}

// --- delete_cell ---

func deleteCell(c *novaapi.Client) tool.Registration {
	const name = "delete_cell"
	add := func(s *sdk.Server) {
		sdk.AddTool(s, &sdk.Tool{
			Name: name,
			Description: "Delete a cell with all its controllers, apps and stored data. DESTRUCTIVE and not undoable; " +
				"the user approves it in a dialog of the MCP client.",
			Annotations: tool.Destructive("Delete cell"),
		}, func(ctx context.Context, req *sdk.CallToolRequest, in cellInput) (*sdk.CallToolResult, deployOutput, error) {
			if err := tool.Required(name, "cell", in.Cell); err != nil {
				return tool.Fail[deployOutput](err)
			}
			// look the cell up first, so the human approves what actually exists
			var cell struct {
				Controllers []struct {
					Name string `json:"name"`
				} `json:"controllers"`
				Apps []struct {
					Name string `json:"name"`
				} `json:"apps"`
			}
			if err := c.Get(ctx, name, cellPath(in.Cell), notFound(in.Cell), &cell); err != nil {
				return tool.Fail[deployOutput](err)
			}
			message := fmt.Sprintf("Delete cell '%s' with %d controller(s) and %d app(s)? This removes all its data and can't be undone.",
				in.Cell, len(cell.Controllers), len(cell.Apps))
			if res := tool.Approval(req, name, message); res != nil {
				return res, deployOutput{}, nil
			}
			status, err := c.JSON(ctx, novaapi.Call{Tool: name, Method: http.MethodDelete, Path: cellPath(in.Cell), NotFound: notFound(in.Cell)}, nil)
			if err != nil {
				return tool.Fail[deployOutput](err)
			}
			out := deployOutput{Cell: in.Cell, Status: novaapi.DeployStatus(status)}
			return tool.OK("cell deletion "+out.Status, map[string]any{"cell": in.Cell, "status": out.Status}, out)
		})
	}
	return tool.Registration{Toolset: tool.Cells, ReadOnly: false, Add: add}
}
