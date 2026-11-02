package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"{{ .PluginName }}/internal/environment"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/labstack/echo/v4"
	nova "github.com/wandelbotsgmbh/nova-api-client-go/v26/pkg/nova/v2"
)

type AppApi struct {
	// Base path is where this app root gets served from (e.g. /cell/myappname)
	BasePath string
	// The Path where the OpenAPI spec is served from (e.g. /cell/myappname/openapi.json)
	OpenApiPath string
	// Cell is the cell name this app is running in.
	Cell string
	// Client for the Nova API
	client *nova.ClientWithResponses
}

// Returns the API implementation
// basePath:
//
//	The base path where the app is served from (e.g. /cell/myappname)
//
// novaApi:
//
//	The URL to the nova api gateway (e.g. https://xdxqzvpu.instance.wandelbots.io/api/v2/)
func NewAppApi(basePath string, novaApi string, cell string) (*AppApi, error) {
	accessToken := environment.GetAccessToken()

	var client *nova.ClientWithResponses
	var err error
	if accessToken != "" {
		setTokenFunc := nova.WithRequestEditorFn(func(ctx context.Context, req *http.Request) error {
			req.Header.Set("Authorization", "Bearer "+accessToken)
			return nil
		})
		client, err = nova.NewClientWithResponses(novaApi, setTokenFunc)
	} else {
		client, err = nova.NewClientWithResponses(novaApi)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to create nova api client: %w", err)
	}

	return &AppApi{
		BasePath:    basePath,
		OpenApiPath: basePath + "/openapi.json",
		Cell:        cell,
		client:      client,
	}, nil
}

// Gets the stoplight ui.
// (GET /)
func (a *AppApi) Get(ctx echo.Context) error {
	return ctx.Render(http.StatusOK, "stoplight", a)
}

// Gets the app icon for the Nova Homescreen.
// (GET /app_icon.png)
func (a *AppApi) GetAppIconPng(ctx echo.Context) error {
	return ctx.File("static/app_icon.png")
}

// Gets the openapi spec.
// (GET /openapi.json)
func (a *AppApi) GetOpenapiJson(ctx echo.Context) error {
	swagger, err := GetSwagger()
	if err != nil {
		return err
	}

	// remove servers from spec
	swagger.Servers = openapi3.Servers{}
	swagger.AddServers(&openapi3.Server{
		URL: a.BasePath,
	})
	return ctx.JSON(http.StatusOK, swagger)
}

// Gets the position of the first motion group which was found.
// (GET /position)
func (a *AppApi) GetPosition(ctx echo.Context) error {
	reqCtx := ctx.Request().Context()

	controllersResp, err := a.client.ListRobotControllersWithResponse(reqCtx, a.Cell)
	if err != nil {
		return err
	}
	if controllersResp.JSON200 == nil {
		return unexpectedResponse(controllersResp.HTTPResponse, controllersResp.Body)
	}

	// the first motion group of the first controller that has one
	for _, controller := range *controllersResp.JSON200 {
		stateResp, err := a.client.GetCurrentRobotControllerStateWithResponse(reqCtx, a.Cell, controller)
		if err != nil {
			return err
		}
		if stateResp.JSON200 == nil {
			return unexpectedResponse(stateResp.HTTPResponse, stateResp.Body)
		}
		if len(stateResp.JSON200.MotionGroups) == 0 {
			continue
		}
		mg := stateResp.JSON200.MotionGroups[0]

		// the tcp pose in world coordinates
		if mg.TcpPose == nil || mg.TcpPose.Position == nil || mg.TcpPose.Orientation == nil {
			return ctx.String(http.StatusNotFound, "No tcp pose found for motion group")
		}

		payload := Position{
			Pose: &Pose{
				Orientation: mg.TcpPose.Orientation,
				Position:    mg.TcpPose.Position,
			},
			Joints: &mg.JointPosition,
		}
		return ctx.JSON(http.StatusOK, payload)
	}

	return ctx.String(http.StatusNotFound, "No controller with a motion group found")
}

// Describes a NOVA API response that could not be decoded.
// The ingress of an instance may answer errors (e.g. 401 for a missing or expired
// ACCESS_TOKEN) with a plain text error page and status 200.
func unexpectedResponse(resp *http.Response, body []byte) error {
	message, _, _ := strings.Cut(strings.TrimSpace(string(body)), "\n")
	return echo.NewHTTPError(http.StatusBadGateway, fmt.Sprintf(
		"NOVA API request failed: unexpected response for url (%s): %s %q. Check NOVA_API and ACCESS_TOKEN (refresh it with `nova app env`)",
		resp.Request.URL, resp.Status, message))
}
