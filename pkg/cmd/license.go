package cmd

import (
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
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
	Long:  `Activate the license for the current instance`,
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		host := viper.GetString("host")
		return nova.ActivateLicense(host)
	},
}

var licenseGetCmd = &cobra.Command{
	Use:   "get",
	Short: "Get the license",
	Long:  `Get the license for the current instance`,
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		host := viper.GetString("host")
		license, err := nova.GetLicense(host)
		if err != nil {
			return err
		}
		cmd.Println(string(license))
		return nil
	},
}

func init() {
	licenseCmd.AddCommand(licenseGetCmd)
	licenseCmd.AddCommand(licenseActivateCmd)
	rootCmd.AddCommand(licenseCmd)
}
