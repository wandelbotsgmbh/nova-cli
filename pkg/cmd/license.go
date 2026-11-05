package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/wandelbotsgmbh/nova-cli/pkg/nova"
)

var licenseCmd = &cobra.Command{
	Use:   "license",
	Short: "Manage licenses",
	Long:  `Display or activate the license`,
	Args:  cobra.NoArgs,
}

var licenseActivateCmd = &cobra.Command{
	Use:   "activate",
	Short: "Activate the license",
	Long: `Activate the license for the current instance.

The instance fetches the license with your portal login, which is sent to it as the
owner. A cloud instance uses the login of its environment; for other instances it is
the environment of your last 'nova login', unless --env or NOVA_ENV picks another.`,
	Example: `$ nova login --env prod
$ nova --instance lab-ipc --env prod license activate`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		host := currentHost()
		return nova.ActivateLicense(host)
	},
}

var licenseGetCmd = &cobra.Command{
	Use:   "get",
	Short: "Get the license",
	Long:  `Get the license for the current instance`,
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		host := currentHost()
		license, err := nova.GetLicense(host)
		if err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), string(license))
		return nil
	},
}

func init() {
	licenseCmd.AddCommand(licenseGetCmd)
	licenseCmd.AddCommand(licenseActivateCmd)
	rootCmd.AddCommand(licenseCmd)
}
