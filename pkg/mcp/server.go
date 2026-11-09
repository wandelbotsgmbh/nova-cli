// Package mcp serves curated MCP tools for a NOVA instance over stdio (`nova mcp`).
//
// The first tools are a port of the instance MCP service (service-manager MR !2924); the
// toolsets in pkg/mcp/toolsets implement the rest of that service's roadmap with the
// same names and rules, so they can move into the service later (ADR-0015, ADR-0019).
package mcp

import (
	"context"
	"fmt"
	"slices"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/novaapi"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/tool"
)

// Options configures the server.
type Options struct {
	// Version is reported as the MCP server's implementation version.
	Version string
	// Toolsets to serve; empty means DefaultToolsets.
	Toolsets []string
	// ReadOnly drops every tool that writes, server-side.
	ReadOnly bool
	// DownloadDir is where tools save files (diagnosis packages, backups).
	DownloadDir string
}

// NewServer returns an MCP server with the tools of opts.Toolsets. api serves the ported
// tools, client the toolset packages.
func NewServer(api novaapi.API, client *novaapi.Client, opts Options) *sdk.Server {
	toolsets := opts.Toolsets
	if len(toolsets) == 0 {
		toolsets = DefaultToolsets()
	}
	server := sdk.NewServer(&sdk.Implementation{Name: "nova-cli", Version: opts.Version},
		&sdk.ServerOptions{Instructions: instructions})
	Register(server, api, tool.Env{Client: client, DownloadDir: opts.DownloadDir}, toolsets, opts.ReadOnly)
	return server
}

// ServeStdio runs the server on stdin/stdout until the client disconnects or ctx ends.
func ServeStdio(ctx context.Context, api novaapi.API, client *novaapi.Client, opts Options) error {
	return NewServer(api, client, opts).Run(ctx, &sdk.StdioTransport{})
}

// ParseToolsets validates a list of toolset names; "default" and "all" expand to those
// sets. Unlike the instance service, which ignores unknown names for forward
// compatibility, the CLI fails early on a typo.
func ParseToolsets(names []string) ([]string, error) {
	all := AllToolsets()
	var selected []string
	add := func(name string) {
		if !slices.Contains(selected, name) {
			selected = append(selected, name)
		}
	}
	for _, name := range names {
		name = strings.TrimSpace(name)
		switch {
		case name == "":
		case name == "all":
			for _, n := range all {
				add(n)
			}
		case name == "default":
			for _, n := range DefaultToolsets() {
				add(n)
			}
		case slices.Contains(all, name):
			add(name)
		default:
			return nil, fmt.Errorf("unknown toolset %q (available: default, all, %s)", name, strings.Join(all, ", "))
		}
	}
	return selected, nil
}
