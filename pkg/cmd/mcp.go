package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp"
)

var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "EXPERIMENTAL: Start an MCP server",
	Long: `EXPERIMENTAL: Start an MCP server based on the NOVA API of the configured host.

You can configure the host using 'nova config set host <host>'.
The local running server will proxy all requests from the LLM to the configured host.

The configuration depends on the AI Coding Assistent of your choice.
For example if using Cursor (https://www.cursor.com/) it looks like this:
'
{
  "mcpServers": {
    "nova": {
      "command": "nova",
      "args": [
        "mcp"
      ]
    }
  }
}
'

To stop the server, use Ctrl+C.
`,
	RunE: func(cmd *cobra.Command, args []string) error {
		host := viper.GetString("host")
		if host == "" {
			return fmt.Errorf("error: Host is not configured. Please set the host using 'nova config set host <host>'")
		}
		return mcp.RunOpenAPIMCPServer(host)
	},
}

func init() {
	rootCmd.AddCommand(mcpCmd)
}
