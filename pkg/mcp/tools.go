package mcp

import (
	"context"
	"slices"

	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/tool"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/novaapi"
)

// tools builds the tools ported from the instance MCP service, bound to api.
func tools(api novaapi.API) []tool.Registration {
	return []tool.Registration{
		contextTool(api),
		listCellsTool(api),
		describeCellTool(api),
		listControllersTool(api),
		describeControllerTool(api),
		listRobotConfigurationsTool(api),
		createVirtualControllerTool(api),
		deleteControllerTool(api),
	}
}

// --- context ---

type contextOutput struct {
	Version string   `json:"version" jsonschema:"the NOVA version this instance runs"`
	Cells   []string `json:"cells" jsonschema:"the robot cells deployed on this instance"`
}

func contextTool(api novaapi.API) tool.Registration {
	add := func(s *sdk.Server) {
		sdk.AddTool(s, &sdk.Tool{
			Name:        "get_context",
			Description: "Always-useful overview of this NOVA instance: version and deployed cells. Call this first.",
			Annotations: tool.Annotations("Get instance context", true, true, false, false),
			Meta:        sdk.Meta{"anthropic/alwaysLoad": true},
		}, func(ctx context.Context, _ *sdk.CallToolRequest, _ struct{}) (*sdk.CallToolResult, contextOutput, error) {
			version, err := api.GetSystemVersion(ctx)
			if err != nil {
				return tool.Fail[contextOutput](tool.AsAPIError(err))
			}
			cells, err := api.ListCells(ctx)
			if err != nil {
				return tool.Fail[contextOutput](tool.AsAPIError(err))
			}
			out := contextOutput{Version: version, Cells: cells}
			return tool.OK("instance context", map[string]any{"version": version, "cells": len(cells)}, out)
		})
	}
	return tool.Registration{Toolset: tool.Context, ReadOnly: true, Add: add}
}

// --- cells ---

type listCellsOutput struct {
	Cells []string `json:"cells" jsonschema:"the robot cells deployed on this instance"`
}

func listCellsTool(api novaapi.API) tool.Registration {
	add := func(s *sdk.Server) {
		sdk.AddTool(s, &sdk.Tool{
			Name:        "list_cells",
			Description: "List the robot cells deployed on this instance (session-filtered).",
			Annotations: tool.Annotations("List cells", true, true, false, false),
		}, func(ctx context.Context, _ *sdk.CallToolRequest, _ struct{}) (*sdk.CallToolResult, listCellsOutput, error) {
			cells, err := api.ListCells(ctx)
			if err != nil {
				return tool.Fail[listCellsOutput](tool.AsAPIError(err))
			}
			return tool.OK("cells listed", map[string]any{"count": len(cells)}, listCellsOutput{Cells: cells})
		})
	}
	return tool.Registration{Toolset: tool.Cells, ReadOnly: true, Add: add}
}

type cellInput struct {
	Cell string `json:"cell" jsonschema:"cell name (see list_cells)"`
}

type describeCellOutput struct {
	Name        string   `json:"name"`
	Controllers []string `json:"controllers"`
	Apps        []string `json:"apps"`
}

func describeCellTool(api novaapi.API) tool.Registration {
	add := func(s *sdk.Server) {
		sdk.AddTool(s, &sdk.Tool{
			Name:        "describe_cell",
			Description: "A cell's controllers and installed apps, by name.",
			Annotations: tool.Annotations("Describe cell", true, true, false, false),
		}, func(ctx context.Context, _ *sdk.CallToolRequest, in cellInput) (*sdk.CallToolResult, describeCellOutput, error) {
			cell, err := api.DescribeCell(ctx, in.Cell)
			if err != nil {
				return tool.Fail[describeCellOutput](tool.AsAPIError(err))
			}
			out := describeCellOutput{Name: cell.Name, Controllers: cell.Controllers, Apps: cell.Apps}
			return tool.OK("cell described", map[string]any{
				"cell": cell.Name, "controllers": len(cell.Controllers), "apps": len(cell.Apps),
			}, out)
		})
	}
	return tool.Registration{Toolset: tool.Cells, ReadOnly: true, Add: add}
}

// --- controllers ---

type listControllersOutput struct {
	Controllers []string `json:"controllers"`
}

func listControllersTool(api novaapi.API) tool.Registration {
	add := func(s *sdk.Server) {
		sdk.AddTool(s, &sdk.Tool{
			Name:        "list_controllers",
			Description: "List the robot controllers of a cell.",
			Annotations: tool.Annotations("List controllers", true, true, false, false),
		}, func(ctx context.Context, _ *sdk.CallToolRequest, in cellInput) (*sdk.CallToolResult, listControllersOutput, error) {
			controllers, err := api.ListControllers(ctx, in.Cell)
			if err != nil {
				return tool.Fail[listControllersOutput](tool.AsAPIError(err))
			}
			return tool.OK("controllers listed", map[string]any{"cell": in.Cell, "count": len(controllers)},
				listControllersOutput{Controllers: controllers})
		})
	}
	return tool.Registration{Toolset: tool.Controllers, ReadOnly: true, Add: add}
}

type controllerInput struct {
	Cell       string `json:"cell" jsonschema:"cell name (see list_cells)"`
	Controller string `json:"controller" jsonschema:"controller name (see list_controllers)"`
}

type describeControllerOutput struct {
	Name         string `json:"name"`
	Manufacturer string `json:"manufacturer"`
	Physical     bool   `json:"physical"`
	VirtualType  string `json:"virtualType,omitempty"`
}

func describeControllerTool(api novaapi.API) tool.Registration {
	add := func(s *sdk.Server) {
		sdk.AddTool(s, &sdk.Tool{
			Name: "describe_controller",
			Description: "A controller's identity: name, manufacturer, whether it is physical, and its " +
				"virtual type if simulated. Connection details and raw configuration are never returned.",
			Annotations: tool.Annotations("Describe controller", true, true, false, false),
		}, func(ctx context.Context, _ *sdk.CallToolRequest, in controllerInput) (*sdk.CallToolResult, describeControllerOutput, error) {
			controller, err := api.DescribeController(ctx, in.Cell, in.Controller)
			if err != nil {
				return tool.Fail[describeControllerOutput](tool.AsAPIError(err))
			}
			out := describeControllerOutput{
				Name: controller.Name, Manufacturer: controller.Manufacturer,
				Physical: controller.Physical, VirtualType: controller.VirtualType,
			}
			return tool.OK("controller described", map[string]any{
				"cell": in.Cell, "controller": controller.Name, "physical": controller.Physical,
			}, out)
		})
	}
	return tool.Registration{Toolset: tool.Controllers, ReadOnly: true, Add: add}
}

func deleteControllerTool(api novaapi.API) tool.Registration {
	add := func(s *sdk.Server) {
		sdk.AddTool(s, &sdk.Tool{
			Name: "delete_controller",
			Description: "Delete a controller from a cell. DESTRUCTIVE and not undoable — confirm with the " +
				"user first, naming the exact cell and controller.",
			Annotations: tool.Annotations("Delete controller", false, true, true, false),
		}, func(ctx context.Context, _ *sdk.CallToolRequest, in controllerInput) (*sdk.CallToolResult, deployOutput, error) {
			result, err := api.DeleteController(ctx, in.Cell, in.Controller)
			if err != nil {
				return tool.Fail[deployOutput](tool.AsAPIError(err))
			}
			out := deployOutput{Name: result.Name, Status: result.Status}
			return tool.OK("controller deletion "+result.Status, map[string]any{
				"cell": in.Cell, "controller": result.Name, "status": result.Status,
			}, out)
		})
	}
	return tool.Registration{Toolset: tool.Controllers, ReadOnly: false, Add: add}
}

// --- virtual controllers ---

type listRobotConfigurationsOutput struct {
	Configurations []string `json:"configurations"`
}

func listRobotConfigurationsTool(api novaapi.API) tool.Registration {
	add := func(s *sdk.Server) {
		sdk.AddTool(s, &sdk.Tool{
			Name: "list_robot_configurations",
			Description: "List the robot configuration presets available on this instance — the valid " +
				"values for create_virtual_controller's 'type'.",
			Annotations: tool.Annotations("List robot configurations", true, true, false, false),
		}, func(ctx context.Context, _ *sdk.CallToolRequest, _ struct{}) (*sdk.CallToolResult, listRobotConfigurationsOutput, error) {
			configurations, err := api.ListRobotConfigurations(ctx)
			if err != nil {
				return tool.Fail[listRobotConfigurationsOutput](tool.AsAPIError(err))
			}
			return tool.OK("robot configurations listed", map[string]any{"count": len(configurations)},
				listRobotConfigurationsOutput{Configurations: configurations})
		})
	}
	return tool.Registration{Toolset: tool.VirtualControllers, ReadOnly: true, Add: add}
}

type createVirtualControllerInput struct {
	Cell         string `json:"cell" jsonschema:"cell name (see list_cells)"`
	Name         string `json:"name" jsonschema:"controller name to create"`
	Manufacturer string `json:"manufacturer" jsonschema:"robot manufacturer, e.g. universalrobots, kuka, abb, fanuc, yaskawa"`
	Type         string `json:"type" jsonschema:"robot configuration preset from list_robot_configurations, e.g. universalrobots-ur5e"`
}

type deployOutput struct {
	Name   string `json:"name"`
	Status string `json:"status" jsonschema:"'completed' or 'pending' — verify with describe_controller when pending"`
}

func createVirtualControllerTool(api novaapi.API) tool.Registration {
	add := func(s *sdk.Server) {
		sdk.AddTool(s, &sdk.Tool{
			Name: "create_virtual_controller",
			Description: "Create a SIMULATED robot controller in a cell (no physical connection). 'type' " +
				"must be one of the presets from list_robot_configurations — without a valid preset the " +
				"controller would have no motion groups and be unusable. Returns status 'pending' while the " +
				"instance rolls it out — verify with describe_controller.",
			Annotations: tool.Annotations("Create virtual controller", false, false, false, false),
		}, func(ctx context.Context, _ *sdk.CallToolRequest, in createVirtualControllerInput) (*sdk.CallToolResult, deployOutput, error) {
			configurations, err := api.ListRobotConfigurations(ctx)
			if err != nil {
				return tool.Fail[deployOutput](tool.AsAPIError(err))
			}
			if !slices.Contains(configurations, in.Type) {
				return tool.Fail[deployOutput](&novaapi.Error{
					Code: novaapi.CodeInvalidArgument,
					Message: "create_virtual_controller: robot configuration '" + in.Type +
						"' not found on this instance — pick one from list_robot_configurations",
				})
			}

			result, err := api.CreateVirtualController(ctx, in.Cell, novaapi.VirtualControllerSpec{
				Name: in.Name, Manufacturer: in.Manufacturer, Type: in.Type,
			})
			if err != nil {
				return tool.Fail[deployOutput](tool.AsAPIError(err))
			}
			out := deployOutput{Name: result.Name, Status: result.Status}
			return tool.OK("virtual controller creation "+result.Status, map[string]any{
				"cell": in.Cell, "controller": result.Name, "status": result.Status,
			}, out)
		})
	}
	// Not readOnly: this tool is a non-destructive, idempotent-ish write
	// (ADR-0009 allows virtual-controller motion/creation freely), but it is
	// still filtered out server-side under --read-only.
	return tool.Registration{Toolset: tool.VirtualControllers, ReadOnly: false, Add: add}
}
