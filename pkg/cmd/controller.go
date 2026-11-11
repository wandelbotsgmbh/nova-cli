package cmd

import (
	"github.com/spf13/cobra"
	"github.com/wandelbotsgmbh/nova-cli/pkg/nova"
)

var controllerCmd = &cobra.Command{
	Use:   "controller",
	Short: "Interact with robot controllers",
	Long:  `Interact with robot controllers`,
}

var controllerListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all configured robot controllers.",
	Long:  `List all configured robot controllers.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		host := currentHost()
		cell := currentCell()

		controllers, err := nova.ListControllers(host, cell)
		if err != nil {
			return err
		}

		return writeNames(cmd, controllerListOutput, controllers)
	},
}

// -o/--output of 'controller list'; assigned in init to avoid an initialization cycle
var controllerListOutput *outputFormat

func init() {
	controllerListOutput = addOutputFlag(controllerListCmd)
	controllerCmd.AddCommand(controllerListCmd)
	rootCmd.AddCommand(controllerCmd)
}
