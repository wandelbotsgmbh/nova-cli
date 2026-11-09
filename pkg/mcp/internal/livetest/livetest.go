// Package livetest connects toolset tests to a real instance. Tests using it skip unless
// NOVA_MCP_LIVE_HOST is set, so CI never touches an instance:
//
//	NOVA_MCP_LIVE_HOST=xyz.instance.wandelbots.io NOVA_MCP_LIVE_TOKEN=$(nova login token) \
//	NOVA_MCP_LIVE_CELL=cell go test ./pkg/mcp/toolsets/... -run Live
//
// Live tests only read, or write to resources they create themselves with an "mcp-test-"
// prefix and delete again. Never move or switch anything on a physical controller.
package livetest

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/novaapi"
)

// Client returns a Client for the live instance, or skips the test.
func Client(t testing.TB) *novaapi.Client {
	t.Helper()
	host := os.Getenv("NOVA_MCP_LIVE_HOST")
	if host == "" {
		t.Skip("NOVA_MCP_LIVE_HOST not set")
	}
	if !strings.Contains(host, "://") {
		host = "https://" + host
	}
	token := os.Getenv("NOVA_MCP_LIVE_TOKEN")
	return novaapi.NewClient(strings.TrimSuffix(host, "/")+"/api/v2", nil, func(_ context.Context, req *http.Request) error {
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		return nil
	}, nil)
}

// Cell is the cell to test in (NOVA_MCP_LIVE_CELL, default "cell").
func Cell() string {
	if cell := os.Getenv("NOVA_MCP_LIVE_CELL"); cell != "" {
		return cell
	}
	return "cell"
}

// VirtualController is a virtual controller that exists in Cell() for read and motion
// tests (NOVA_MCP_LIVE_CONTROLLER, default "mcp-ur5e").
func VirtualController() string {
	if c := os.Getenv("NOVA_MCP_LIVE_CONTROLLER"); c != "" {
		return c
	}
	return "mcp-ur5e"
}
