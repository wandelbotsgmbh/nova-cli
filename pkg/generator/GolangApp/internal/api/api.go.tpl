package api

import (
	"context"
	"fmt"
	"net/http"
	"{{ .PluginName }}/internal/environment"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/labstack/echo/v4"
	nova "github.com/wandelbotsgmbh/nova-api-client-go/v25/pkg/nova"
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
//	The URL to the nova api gateway (e.g. https://xdxqzvpu.instance.wandelbots.io)
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
	controllerResp, err := a.client.ListControllersWithResponse(context.Background(), a.Cell)
	if err != nil {
		return err
	}
	if controllerResp.JSON200 == nil || len(controllerResp.JSON200.Instances) == 0 {
		return ctx.String(http.StatusNotFound, "No controller found")
	}
	// get the first controller
	controller := controllerResp.JSON200.Instances[0]

	// get the first motion group
	if len(controller.PhysicalMotionGroups) == 0 {
		return ctx.String(http.StatusNotFound, "No motion groups found for controller")
	}

	mg := controller.PhysicalMotionGroups[0].MotionGroup

	// get the tcp position in world coordinates
	stateResp, err := a.client.GetCurrentMotionGroupStateWithResponse(context.Background(), a.Cell, mg, nil)
	if err != nil {
		return err
	}

	if stateResp.JSON200 == nil {
		return ctx.String(http.StatusNotFound, "No position found for motion group")
	}

	posResp := stateResp.JSON200.State.TcpPose

	// map the target response
	ori := []float64{posResp.Orientation.X, posResp.Orientation.Y, posResp.Orientation.Z}
	pos := []float64{posResp.Position.X, posResp.Position.Y, posResp.Position.Z}

	payload := Position{
		Pose: &Pose{
			Orientation: &ori,
			Position:    &pos,
		},
		Joints: &stateResp.JSON200.State.JointPosition.Joints,
	}

	return ctx.JSON(http.StatusOK, payload)
}
