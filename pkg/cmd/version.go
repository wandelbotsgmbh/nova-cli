package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/wandelbotsgmbh/nova-cli/pkg/nova"
	"github.com/wandelbotsgmbh/nova-cli/pkg/version"
)

var printVersionVerbose bool

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version of the NOVA CLI and the NOVA OS instance",
	Long:  `Print the version of the NOVA CLI and the NOVA OS instance`,
	Run: func(cmd *cobra.Command, args []string) {
		cliVersion := version.GetCliVersion()
		fmt.Println("cli version:", cliVersion)

		if printVersionVerbose {
			host := viper.GetString("host")
			if novaVersion, err := nova.GetNovaVersion(host); err == nil {
				updateAvailable := getUpdateAvailable(host)
				cmd.Println("nova version:", novaVersion, updateAvailable)
			}
		}
	},
}

var versionSetCmd = &cobra.Command{
	Use:   "set",
	Short: "Sets the version of the NOVA OS instance",
	Long:  `Sets the version of the NOVA OS instance (stable, next or a specific version)`,
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		host := viper.GetString("host")
		return nova.SetNovaVersion(host, strings.TrimSpace(args[0]))
	},
}

func getUpdateAvailable(host string) string {
	updateText := ""
	stable, _ := nova.CheckNovaVersionUpdate(host, "stable")
	if stable != "" {
		updateText += fmt.Sprintf("(available stable version: %s)", stable)
	}

	next, _ := nova.CheckNovaVersionUpdate(host, "next")
	if next != "" {
		updateText += fmt.Sprintf("(available next version: %s)", next)
	}

	return updateText
}

func init() {
	versionCmd.AddCommand(versionSetCmd)
	rootCmd.AddCommand(versionCmd)

	versionCmd.Flags().BoolVarP(&printVersionVerbose, "verbose", "v", false, "Print all available version information.")
}
