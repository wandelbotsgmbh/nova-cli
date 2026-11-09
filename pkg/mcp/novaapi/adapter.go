package novaapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	apiv2 "github.com/wandelbotsgmbh/nova-api-client-go/v25/pkg/nova/v2"
)

// New returns an API backed by the NOVA v2 API at baseURL ("https://<host>/api/v2"),
// see NewClient.
func New(baseURL string, httpClient *http.Client, editor RequestEditor, logger *slog.Logger) (API, error) {
	return NewFromClient(NewClient(baseURL, httpClient, editor, logger))
}

// NewFromClient returns an API that sends its requests through c.
func NewFromClient(c *Client) (API, error) {
	gen, err := apiv2.NewClientWithResponses(c.baseURL, apiv2.WithHTTPClient(c.http), apiv2.WithRequestEditorFn(c.edit))
	if err != nil {
		return nil, fmt.Errorf("create NOVA API client: %w", err)
	}
	return &adapter{Client: c, client: gen}, nil
}

type adapter struct {
	*Client
	client *apiv2.ClientWithResponses
}

func (a *adapter) GetSystemVersion(ctx context.Context) (string, error) {
	const tool = "get_system_version"
	resp, err := a.client.GetSystemVersionWithResponse(ctx)
	if err != nil {
		return "", a.transportError(ctx, tool, err)
	}
	if resp.StatusCode() != http.StatusOK {
		return "", a.statusError(ctx, tool, resp.StatusCode(), resp.Body, "")
	}
	var version string
	if err := json.Unmarshal(resp.Body, &version); err == nil {
		return version, nil
	}
	return strings.TrimSpace(string(resp.Body)), nil
}

func (a *adapter) ListCells(ctx context.Context) ([]string, error) {
	resp, err := a.client.ListCellsWithResponse(ctx)
	if err != nil {
		return nil, a.transportError(ctx, "list_cells", err)
	}
	if resp.StatusCode() != http.StatusOK || resp.JSON200 == nil {
		return nil, a.statusError(ctx, "list_cells", resp.StatusCode(), resp.Body, "")
	}
	return *resp.JSON200, nil
}

func (a *adapter) DescribeCell(ctx context.Context, cellID string) (CellInfo, error) {
	resp, err := a.client.GetCellWithResponse(ctx, cellID)
	if err != nil {
		return CellInfo{}, a.transportError(ctx, "describe_cell", err)
	}
	if resp.StatusCode() != http.StatusOK || resp.JSON200 == nil {
		return CellInfo{}, a.statusError(ctx, "describe_cell", resp.StatusCode(), resp.Body,
			fmt.Sprintf("cell '%s' not found", cellID))
	}

	cell := resp.JSON200
	info := CellInfo{Name: cell.Name, Controllers: []string{}, Apps: []string{}}
	if cell.Controllers != nil {
		for _, controller := range *cell.Controllers {
			info.Controllers = append(info.Controllers, controller.Name)
		}
	}
	if cell.Apps != nil {
		for _, app := range *cell.Apps {
			info.Apps = append(info.Apps, app.Name)
		}
	}
	return info, nil
}

func (a *adapter) ListControllers(ctx context.Context, cellID string) ([]string, error) {
	resp, err := a.client.ListRobotControllersWithResponse(ctx, cellID)
	if err != nil {
		return nil, a.transportError(ctx, "list_controllers", err)
	}
	if resp.StatusCode() != http.StatusOK || resp.JSON200 == nil {
		return nil, a.statusError(ctx, "list_controllers", resp.StatusCode(), resp.Body,
			fmt.Sprintf("cell '%s' not found", cellID))
	}
	return *resp.JSON200, nil
}

func (a *adapter) DescribeController(ctx context.Context, cellID, controllerID string) (ControllerInfo, error) {
	resp, err := a.client.GetRobotControllerWithResponse(ctx, cellID, controllerID)
	if err != nil {
		return ControllerInfo{}, a.transportError(ctx, "describe_controller", err)
	}
	if resp.StatusCode() != http.StatusOK || resp.JSON200 == nil {
		return ControllerInfo{}, a.statusError(ctx, "describe_controller", resp.StatusCode(), resp.Body,
			fmt.Sprintf("controller '%s' not found in cell '%s'", controllerID, cellID))
	}
	return projectController(*resp.JSON200), nil
}

// projectController is the hardening boundary: it reads only the discriminator ("kind")
// and, for virtual controllers, the manufacturer and preset "type", never an IP, port or
// credential field of a physical controller's configuration.
func projectController(robot apiv2.RobotController) ControllerInfo {
	info := ControllerInfo{Name: robot.Name, Physical: true}

	kind, err := robot.Configuration.Discriminator()
	if err != nil {
		// unrecognized or malformed configuration counts as physical (ADR-0009)
		return info
	}
	info.Manufacturer = kind
	if !IsVirtualKind(kind) {
		return info
	}

	info.Physical = false
	if virtual, err := robot.Configuration.AsVirtualController(); err == nil {
		info.Manufacturer = string(virtual.Manufacturer)
		if virtual.Type != nil {
			info.VirtualType = string(*virtual.Type)
		}
	}
	return info
}

// ListRobotConfigurations calls GET /robot-configurations, which the generated client
// in use doesn't have yet.
func (a *adapter) ListRobotConfigurations(ctx context.Context) ([]string, error) {
	const tool = "list_robot_configurations"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.baseURL+"/robot-configurations", nil)
	if err != nil {
		return nil, a.transportError(ctx, tool, err)
	}
	req.Header.Set("Accept", "application/json")
	if err := a.edit(ctx, req); err != nil {
		return nil, a.transportError(ctx, tool, err)
	}
	resp, err := a.http.Do(req)
	if err != nil {
		return nil, a.transportError(ctx, tool, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, a.transportError(ctx, tool, err)
	}
	var configurations []string
	if resp.StatusCode != http.StatusOK || json.Unmarshal(body, &configurations) != nil {
		return nil, a.statusError(ctx, tool, resp.StatusCode, body, "")
	}
	return configurations, nil
}

func (a *adapter) CreateVirtualController(ctx context.Context, cellID string, spec VirtualControllerSpec) (DeployResult, error) {
	const tool = "create_virtual_controller"

	preset := apiv2.VirtualControllerTypes(spec.Type)
	var configuration apiv2.ControllerConfig
	if err := configuration.FromVirtualController(apiv2.VirtualController{
		Kind:         apiv2.VirtualControllerKindVirtualController,
		Manufacturer: apiv2.Manufacturer(spec.Manufacturer),
		Type:         &preset,
	}); err != nil {
		return DeployResult{}, a.transportError(ctx, tool, err)
	}

	resp, err := a.client.AddRobotControllerWithResponse(ctx, cellID, nil, apiv2.RobotController{
		Name:          spec.Name,
		Configuration: configuration,
	})
	if err != nil {
		return DeployResult{}, a.transportError(ctx, tool, err)
	}
	switch resp.StatusCode() {
	case http.StatusOK:
		return DeployResult{Name: spec.Name, Status: StatusCompleted}, nil
	case http.StatusAccepted:
		return DeployResult{Name: spec.Name, Status: StatusPending}, nil
	case http.StatusConflict:
		return DeployResult{}, &Error{Code: CodeAlreadyExists, Message: fmt.Sprintf(
			"%s: a controller named '%s' already exists in cell '%s'", tool, spec.Name, cellID)}
	default:
		return DeployResult{}, a.statusError(ctx, tool, resp.StatusCode(), resp.Body,
			fmt.Sprintf("cell '%s' not found", cellID))
	}
}

func (a *adapter) DeleteController(ctx context.Context, cellID, controllerID string) (DeployResult, error) {
	const tool = "delete_controller"

	resp, err := a.client.DeleteRobotControllerWithResponse(ctx, cellID, controllerID, nil)
	if err != nil {
		return DeployResult{}, a.transportError(ctx, tool, err)
	}
	switch resp.StatusCode() {
	case http.StatusOK, http.StatusNoContent:
		return DeployResult{Name: controllerID, Status: StatusCompleted}, nil
	case http.StatusAccepted:
		return DeployResult{Name: controllerID, Status: StatusPending}, nil
	default:
		return DeployResult{}, a.statusError(ctx, tool, resp.StatusCode(), resp.Body,
			fmt.Sprintf("controller '%s' not found in cell '%s'", controllerID, cellID))
	}
}
