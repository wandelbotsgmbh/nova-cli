package cmd

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/wandelbotsgmbh/nova-cli/pkg/httpx"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcpbridge"
	"github.com/wandelbotsgmbh/nova-cli/pkg/nova"
	"github.com/wandelbotsgmbh/nova-cli/pkg/novaflow"
)

var flowMCPPrint bool

var flowMCPCmd = &cobra.Command{
	Use:   "mcp",
	Short: "Serve NovaFlow's MCP server (flow authoring) over stdio",
	Long: `Serve the MCP server of NovaFlow on the cell over stdio, for MCP clients that start
a local command (Claude Code, Cursor, …).

NovaFlow's control plane has an MCP endpoint for authoring flows (behavior trees):
the action catalog, the tree schema, validation, and reading and saving flows. It is
an HTTP endpoint behind the instance login, and portal logins expire after a day.
'nova flow mcp' forwards every message to it with a fresh login, so a client set up
once keeps working:

  claude mcp add novaflow -- nova flow mcp

The instance is resolved when the client starts the server (--instance,
NOVA_INSTANCE or the selected instance); pin one with
'claude mcp add novaflow -- nova flow mcp --instance <name>'. Log in with 'nova login'
first: the server never starts an interactive login. NovaFlow must be installed on
the cell, version 2026.9 or newer ('nova flow status').

The server runs until stdin ends. Batches are split into single messages, and every
request gets one answer: failures (login, network, HTTP) come back as JSON-RPC errors.

--print shows the endpoint and the command to register the server instead.

Instance tools (cells, controllers, motion) are served by 'nova mcp'; both can be
registered side by side.`,
	Example: `  claude mcp add novaflow -- nova flow mcp
  claude mcp add novaflow-lab -- nova flow mcp --instance lab
  nova flow mcp --print`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		t := currentTarget()
		if t.Host == "" {
			return nova.ErrNoInstance
		}
		endpoint := flowMCPEndpoint(t.Host, t.Cell())
		if flowMCPPrint {
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "endpoint: %s\n", endpoint)
			fmt.Fprintf(out, "register: claude mcp add novaflow -- nova flow mcp\n")
			if t.Name != "" {
				fmt.Fprintf(out, "pinned:   claude mcp add novaflow -- nova flow mcp --instance %s\n", t.Name)
			}
			return nil
		}
		if f, ok := cmd.InOrStdin().(*os.File); ok && isTerminal(f) {
			cmd.PrintErrln("nova flow mcp speaks MCP (JSON-RPC) on stdin/stdout for an MCP client, e.g.:\n" +
				"  claude mcp add novaflow -- nova flow mcp\n" +
				"Waiting for messages on stdin (Ctrl-D ends).")
		}
		hc := *httpx.Client()
		hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		b := &mcpbridge.Bridge{
			Endpoint: endpoint,
			HTTP:     &hc,
			Auth:     (&mcpSession{}).auth,
			Hint:     "is NovaFlow 2026.9 or newer installed on the cell? check with 'nova flow status'",
		}
		return b.Serve(cmd.Context(), cmd.InOrStdin(), cmd.OutOrStdout())
	},
}

// flowMCPEndpoint is the MCP endpoint of NovaFlow's control plane on the cell.
func flowMCPEndpoint(host, cell string) string {
	if cell == "" {
		cell = "cell"
	}
	return strings.TrimSuffix(nova.HostWithProtocol(host), "/") + "/" + url.PathEscape(cell) + "/" + novaflow.ControlPlaneApp + "/api/v1/mcp"
}

func init() {
	flowMCPCmd.Flags().BoolVar(&flowMCPPrint, "print", false, "Print the endpoint and the command to register the server instead of serving it")
	flowCmd.AddCommand(flowMCPCmd)
}
