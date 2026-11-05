package api

import (
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/labstack/echo/v4"
)

type OpenApiRoute struct {
	BasePath string
}

func NewOpenApiRoute(basePath string) *OpenApiRoute {
	return &OpenApiRoute{
		BasePath: basePath,
	}
}

func (sl *OpenApiRoute) ServeOpenApi(ctx echo.Context) error {
	swagger, err := GetSwagger()
	if err != nil {
		return err
	}

	// remove servers from spec
	swagger.Servers = openapi3.Servers{}
	swagger.AddServers(&openapi3.Server{
		URL: sl.BasePath,
	})

	json, err := swagger.MarshalJSON()
	if err != nil {
		return err
	}

	ctx.Response().Header().Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	ctx.Response().Write(json)
	return nil
}
