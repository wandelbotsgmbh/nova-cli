package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"time"

	"{{ .PluginName }}/internal/api"
	"{{ .PluginName }}/internal/environment"
	"{{ .PluginName }}/internal/renderer"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
)

func main() {
	e := echo.New()
	e.HideBanner = true

	opts := &slog.HandlerOptions{
		Level: environment.GetLogLevel(),
	}

	logHander := slog.NewTextHandler(os.Stdout, opts)
	slog.SetDefault(slog.New(logHander))

	e.Pre(middleware.RemoveTrailingSlash())
	e.Use(middleware.CORS())
	e.Use(logging())
	e.Use(middleware.Recover())

	basePath := environment.GetBasePath()
	cellName := environment.GetNovaCellName()
	novaApi := environment.GetNovaApiPath()
	if novaApi == "" {
		slog.Error("NOVA_API environment variable is not set")
		os.Exit(1)
	}

	e.Renderer = renderer.NewRenderer()

	appApi, err := api.NewAppApi(basePath, novaApi, cellName)
	if err != nil {
		slog.Error("failed to create app api", slog.Any("error", err))
		os.Exit(1)
	}

	api.RegisterHandlersWithBaseURL(e, appApi, basePath)

	// if there is a base path set, the root is not properly handled (would require a trailing slash)
	if len(basePath) > 2 {
		e.GET(basePath, func(c echo.Context) error {
			return appApi.Get(c)
		})
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	// Start server
	go func() {
		if err := e.Start(":8080"); err != nil && err != http.ErrServerClosed {
			e.Logger.Fatal("shutting down the server")
		}
	}()

	// Wait for interrupt signal to gracefully shutdown the server with a timeout of 10 seconds.
	<-ctx.Done()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := e.Shutdown(ctx); err != nil {
		e.Logger.Fatal(err)
	}
}

func logging() echo.MiddlewareFunc {
	return middleware.RequestLoggerWithConfig(middleware.RequestLoggerConfig{
		LogStatus:   true,
		LogURI:      true,
		LogError:    true,
		HandleError: true, // forwards error to the global error handler, so it can decide appropriate status code
		LogValuesFunc: func(c echo.Context, v middleware.RequestLoggerValues) error {
			if v.Error == nil {
				slog.Debug("http request", slog.String("uri", v.URI), slog.Int("uri", v.Status))
			} else {
				slog.Error("http error", slog.String("uri", v.URI), slog.Int("uri", v.Status), slog.Any("error", v.Error))
			}
			return nil
		},
	})
}
