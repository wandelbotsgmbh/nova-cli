package cmd

import (
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
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
		host := viper.GetString("host")
		cell := viper.GetString("cellname")

		controllers, err := nova.ListControllers(host, cell)
		if err != nil {
			return err
		}

		for _, controller := range controllers {
			cmd.Println(controller)
		}
		return nil
	},
}

func init() {
	controllerCmd.AddCommand(controllerListCmd)
	rootCmd.AddCommand(controllerCmd)
}
