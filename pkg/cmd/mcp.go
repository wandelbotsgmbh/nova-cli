package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/spf13/cobra"
	"github.com/wandelbotsgmbh/nova-cli/pkg/debug"
	"github.com/wandelbotsgmbh/nova-cli/pkg/httpx"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/novaapi"
	"github.com/wandelbotsgmbh/nova-cli/pkg/nova"
	"github.com/wandelbotsgmbh/nova-cli/pkg/oauth"
	"github.com/wandelbotsgmbh/nova-cli/pkg/version"
)

var (
	mcpToolsets    []string
	mcpReadOnly    bool
	mcpDownloadDir string
)

var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "EXPERIMENTAL: Start an MCP server for the selected instance",
	Long: `EXPERIMENTAL: Start an MCP server (stdio) with curated tools for the selected instance.

Toolsets served by default (mostly reads):
  context, cells, controllers, virtual-controllers, motion-group-models, system, apps,
  diagnosis, guides, kinematics, datasets
Opt-in with --toolsets (e.g. --toolsets default,planning,motion or --toolsets all):
  planning, stores, motion, programs, maintenance

The tools follow the roadmap of the instance MCP service that is planned for a NOVA
release; until it ships, 'nova mcp' serves them locally and calls the NOVA v2 API of the
instance with your 'nova' login.

Guard rails (not a safety function):
  - Motion, I/O writes and mode changes only run on virtual controllers; on a physical
    controller they are refused with APPROVAL_REQUIRED, a human runs them through an app
    or the NOVA UI.
  - Deleting a cell, restoring a backup and updating NOVA ask you through the MCP client
    (elicitation); clients that can't ask get APPROVAL_REQUIRED.
  - --read-only drops every tool that changes anything.

Downloads (diagnosis packages, backups) are saved to --download-dir.

The server targets the selected instance ('nova instance select <name>'). To pin it to
a specific instance, pass --instance <name> or set NOVA_INSTANCE (e.g. in the "env" of
the MCP client config). Cloud instances need a 'nova login'; the login is refreshed
automatically while the server runs.

Example for Claude Code ('.mcp.json') or Cursor ('.cursor/mcp.json'):

  {
    "mcpServers": {
      "nova": {
        "command": "nova",
        "args": ["mcp", "--read-only"],
        "env": { "NOVA_INSTANCE": "my-instance" }
      }
    }
  }

Diagnostics go to stderr; stdout carries only the MCP protocol.`,
	Example: `  nova mcp
  nova mcp --instance my-instance --read-only
  nova mcp --toolsets default,planning,motion
  nova mcp --toolsets all --download-dir ./nova-downloads`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		toolsets, err := mcp.ParseToolsets(mcpToolsets)
		if err != nil {
			return err
		}
		host := currentHost()
		if host == "" {
			return nova.ErrNoInstance
		}

		level := slog.LevelWarn
		if debug.Enabled() {
			level = slog.LevelDebug
		}
		logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

		client := novaapi.NewClient(nova.HostWithProtocol(host)+nova.API_BASE_V2, httpx.Client(), mcpAuth(), logger)
		api, err := novaapi.NewFromClient(client)
		if err != nil {
			return err
		}
		downloadDir := mcpDownloadDir
		if downloadDir == "" {
			downloadDir = filepath.Join(os.TempDir(), "nova-mcp")
		}
		if downloadDir, err = filepath.Abs(downloadDir); err != nil {
			return err
		}
		return mcp.ServeStdio(cmd.Context(), api, client, mcp.Options{
			Version:     version.GetCliVersion(),
			Toolsets:    toolsets,
			ReadOnly:    mcpReadOnly,
			DownloadDir: downloadDir,
		})
	},
}

// mcpAuth authenticates every request of the MCP server. Unlike one-shot commands it
// never starts an interactive login (that would block a tool call on a browser dialog):
// it refreshes the stored portal login when it has expired, or fails with a hint.
func mcpAuth() novaapi.RequestEditor {
	var mu sync.Mutex // tool calls run concurrently; a refresh writes the config
	return func(ctx context.Context, req *http.Request) error {
		if oauth.SendsTokenTo(req.URL.Host) {
			env := oauth.ActiveEnvironment()
			mu.Lock()
			err := oauth.RefreshToken(ctx, env)
			mu.Unlock()
			if err != nil && !strings.Contains(err.Error(), "nova login") {
				return fmt.Errorf("%w, run 'nova login --env %s'", err, env)
			}
			if err != nil {
				return err
			}
		}
		if err := oauth.SetAuthForRequest(req); err != nil {
			return err
		}
		return version.SetUserAgent(ctx, req)
	}
}

func init() {
	mcpCmd.Flags().StringSliceVar(&mcpToolsets, "toolsets", nil,
		"comma-separated toolsets to serve, 'default', 'all' or any of: "+strings.Join(mcp.AllToolsets(), ", "))
	mcpCmd.Flags().StringVar(&mcpDownloadDir, "download-dir", "", "where tools save files such as diagnosis packages and backups (default: <temp dir>/nova-mcp)")
	mcpCmd.Flags().BoolVar(&mcpReadOnly, "read-only", false, "serve only tools that don't change the instance")
	rootCmd.AddCommand(mcpCmd)
}
