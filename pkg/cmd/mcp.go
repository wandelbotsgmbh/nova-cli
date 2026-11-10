package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/wandelbotsgmbh/nova-cli/pkg/credentials"
	"github.com/wandelbotsgmbh/nova-cli/pkg/debug"
	"github.com/wandelbotsgmbh/nova-cli/pkg/httpx"
	"github.com/wandelbotsgmbh/nova-cli/pkg/instances"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/novaapi"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/tool"
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
  instances (only when not pinned), context, cells, controllers, virtual-controllers, motion-group-models, system, apps,
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

The server starts on the selected instance ('nova instance select <name>'). The agent
can switch it for the session with list_instances and use_instance; that never changes
the selection in the config. Pinned with --instance <name> or NOVA_INSTANCE (e.g. in the
"env" of the MCP client config), the server stays on that instance and doesn't offer
those tools. Cloud instances need a 'nova login'; the login is refreshed automatically
while the server runs.

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
		t := currentTarget()
		// pinned with --instance or NOVA_INSTANCE: the session stays on it, the instances
		// tools aren't served; otherwise use_instance may switch (never written to the config)
		pinned := targetSource == instances.SourceFlag || targetSource == instances.SourceEnv
		if t.Host == "" && pinned {
			return nova.ErrNoInstance
		}

		level := slog.LevelWarn
		if debug.Enabled() {
			level = slog.LevelDebug
		}
		logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

		session := &mcpSession{current: t.Name}
		base := ""
		if t.Host != "" {
			base = nova.HostWithProtocol(t.Host) + nova.API_BASE_V2
		} else {
			fmt.Fprintln(os.Stderr, "note: no instance selected; the agent can pick one with list_instances and use_instance")
		}
		client := novaapi.NewClient(base, httpx.Client(), session.auth, logger)
		// the startup target with its login environment, so calls pinned to it keep it
		// after a switch
		client.SwitchTarget(novaapi.Target{BaseURL: base, Env: string(oauth.ActiveEnvironment())})
		session.client = client
		var switcher tool.InstanceSwitcher
		if !pinned {
			switcher = session
			// without them an agent can't pick an instance, and with no instance
			// selected every other tool would only say "call use_instance first"
			if !slices.Contains(toolsets, tool.Instances) {
				toolsets = append(toolsets, tool.Instances)
			}
		}
		api, err := novaapi.NewFromClient(client)
		if err != nil {
			return err
		}
		// checked when a tool first saves a file, so a missing cache dir never keeps the
		// server from starting (most toolsets never download)
		downloadDir := mcpDownloadDir
		if downloadDir == "" {
			if downloadDir, err = tool.DefaultDownloadDir(); err != nil {
				fmt.Fprintf(os.Stderr, "note: no download dir (%v); tools that save files need --download-dir\n", err)
				downloadDir = ""
			}
		}
		if downloadDir != "" {
			if downloadDir, err = filepath.Abs(downloadDir); err != nil {
				return err
			}
		}
		return mcp.ServeStdio(cmd.Context(), api, client, mcp.Options{
			Version:     version.GetCliVersion(),
			Toolsets:    toolsets,
			ReadOnly:    mcpReadOnly,
			DownloadDir: downloadDir,
			Instances:   switcher,
		})
	},
}

// mcpSession holds the target of one `nova mcp` server: the instance it talks to
// (switchable with use_instance unless pinned) and the authentication of its requests.
type mcpSession struct {
	// mu serializes token refreshes (they write the config) and instance switches
	// (they change the active portal environment)
	mu      sync.Mutex
	client  *novaapi.Client
	current string
}

// auth authenticates every request. Unlike one-shot commands it never starts an
// interactive login (that would block a tool call on a browser dialog): it refreshes the
// stored portal login when it has expired, or fails with a hint. It holds mu for the
// whole request setup: tool calls run concurrently, and both the refresh (writes) and
// SetAuthForRequest (reads) use viper, which isn't safe for concurrent use.
func (m *mcpSession) auth(ctx context.Context, req *http.Request) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	// the login environment of the instance this call is pinned to; LoadToken in
	// SetAuthForRequest reads the active one, which is only changed under mu
	if t, ok := novaapi.PinnedTarget(ctx); ok && t.Env != "" {
		oauth.SetActiveEnvironment(oauth.AuthEnvironment(t.Env))
	}
	if oauth.SendsTokenTo(req.URL.Host) {
		env := oauth.ActiveEnvironment()
		err := oauth.RefreshToken(ctx, env)
		if err != nil && hasBasicAuth() {
			// basic auth alone is enough (SetAuthForRequest decides); no portal login needed
			err = nil
		}
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

// hasBasicAuth reports whether basic auth credentials are configured.
func hasBasicAuth() bool {
	c, err := credentials.GetBasicAuth()
	return err == nil && c.Username != "" && c.Password != ""
}

// registry reads the instance registry fresh from the config file, so instances added
// in another terminal show up. Read only, never written.
func (m *mcpSession) registry() (*instances.Registry, error) {
	if file := viper.ConfigFileUsed(); file != "" {
		v := viper.New()
		v.SetConfigFile(file)
		if err := v.ReadInConfig(); err == nil {
			return instances.Load(v)
		}
	}
	return instances.Load(viper.GetViper())
}

func instanceInfo(i instances.Instance, current string) tool.InstanceInfo {
	return tool.InstanceInfo{Name: i.Name, Kind: string(i.Kind), Host: i.Host, Cell: i.Cell(), Env: i.Env, Current: i.Name == current}
}

// List implements tool.InstanceSwitcher.
func (m *mcpSession) List() ([]tool.InstanceInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, err := m.registry()
	if err != nil {
		return nil, err
	}
	current := m.current
	list := make([]tool.InstanceInfo, 0, len(r.Instances))
	for _, i := range r.Instances {
		list = append(list, instanceInfo(i, current))
	}
	return list, nil
}

// Use implements tool.InstanceSwitcher: later requests go to the instance name, with
// the portal environment of a cloud instance (an explicit --env/NOVA_ENV that
// contradicts it is an error, as for every command).
func (m *mcpSession) Use(name string) (tool.InstanceInfo, error) {
	// viper and the active environment are shared with the requests of other tool calls
	m.mu.Lock()
	defer m.mu.Unlock()
	r, err := m.registry()
	if err != nil {
		return tool.InstanceInfo{}, err
	}
	i, ok := r.Get(name)
	if !ok {
		return tool.InstanceInfo{}, fmt.Errorf("no instance %q is configured (see list_instances)", name)
	}
	if i.Host == "" {
		return tool.InstanceInfo{}, fmt.Errorf("instance %q has no host", name)
	}
	env, _, err := resolveEnv(i)
	if err != nil {
		return tool.InstanceInfo{}, err
	}
	// tool calls already running keep their pinned target (and its environment)
	m.client.SwitchTarget(novaapi.Target{BaseURL: nova.HostWithProtocol(i.Host) + nova.API_BASE_V2, Env: string(env)})
	m.current = i.Name
	debug.Logf("mcp: session target switched to instance %q (host %q, env %s)", i.Name, i.Host, env)
	return instanceInfo(i, m.current), nil
}

func init() {
	mcpCmd.Flags().StringSliceVar(&mcpToolsets, "toolsets", nil,
		"comma-separated toolsets to serve, 'default', 'all' or any of: "+strings.Join(mcp.AllToolsets(), ", "))
	mcpCmd.Flags().StringVar(&mcpDownloadDir, "download-dir", "", "where tools save files such as diagnosis packages and backups; must be private to you (default: <user cache dir>/nova/mcp-downloads)")
	mcpCmd.Flags().BoolVar(&mcpReadOnly, "read-only", false, "serve only tools that don't change the instance")
	rootCmd.AddCommand(mcpCmd)
}
