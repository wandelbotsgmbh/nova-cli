package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp"
)

var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "EXPERIMENTAL: Start an MCP server",
	Long: `EXPERIMENTAL: Start an MCP server based on the NOVA API of the selected instance.

The server targets the selected instance ('nova instance select <name>'). To pin it to
a specific instance regardless of the selection, pass --instance <name> or set the
NOVA_INSTANCE environment variable (e.g. in the "env" of the MCP client config).
The local running server will proxy all requests from the LLM to that instance.

The configuration depends on the AI Coding Assistent of your choice.
For example if using Cursor (https://www.cursor.com/) it looks like this:
'
{
  "mcpServers": {
    "nova": {
      "command": "nova",
      "args": [
        "mcp"
      ],
      "env": {
        "NOVA_INSTANCE": "my-instance"
      }
    }
  }
}
'

To stop the server, use Ctrl+C.
`,
	RunE: func(cmd *cobra.Command, args []string) error {
		host := currentHost()
		if host == "" {
			return fmt.Errorf("error: no instance selected. Select one with 'nova instance select <name>' or register one with 'nova instance add <name> --host <url>'")
		}
		return mcp.RunOpenAPIMCPServer(host)
	},
}

func init() {
	rootCmd.AddCommand(mcpCmd)
}
