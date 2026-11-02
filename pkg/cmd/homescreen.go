package cmd

import (
	"strings"

	"github.com/cli/browser"
	"github.com/spf13/cobra"
)

var homescreenCmd = &cobra.Command{
	Use:   "homescreen",
	Short: "Open the homescreen in a browser",
	Long:  `Open the current NOVA OS instance's homescreen in a browser`,
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		host := currentHost()
		host, _ = strings.CutPrefix(host, "https://")
		host, _ = strings.CutPrefix(host, "http://")
		host = "http://" + host
		return browser.OpenURL(host)
	},
}

func init() {
	rootCmd.AddCommand(homescreenCmd)
}
