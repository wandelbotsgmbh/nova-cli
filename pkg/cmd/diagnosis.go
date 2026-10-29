package cmd

import (
	"net/http"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/wandelbotsgmbh/nova-cli/pkg/nova"
)

var diagnosisCmd = &cobra.Command{
	Use:   "diagnosis",
	Short: "Send or download diagnostic information from Wandelbots NOVA OS",
	Long:  `Send or download diagnostic information from Wandelbots NOVA OS`,
}

var sendSlackCmd = &cobra.Command{
	Use:   "send-slack",
	Short: "Send diagnostic information from Wandelbots NOVA OS via slack",
	Long:  `Send diagnostic information from Wandelbots NOVA OS via slack`,
	// TODO slack is not available for the normal customers right now
	Hidden: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		host := viper.GetString("host")
		resp, err := http.Post("http://"+host+"/diagnose/sendslack", "application/json", nil)
		if err != nil {
			return err
		}
		defer resp.Body.Close()

		return nil
	},
}

var downloadZipCmd = &cobra.Command{
	Use:   "download",
	Short: "Download diagnostic information from Wandelbots NOVA OS",
	Long:  `Download diagnostic information from Wandelbots NOVA OS`,
	RunE: func(cmd *cobra.Command, args []string) error {
		host := viper.GetString("host")
		return nova.DownloadDiagnosisPackage(host)
	},
}

func init() {
	diagnosisCmd.AddCommand(sendSlackCmd)
	diagnosisCmd.AddCommand(downloadZipCmd)
	rootCmd.AddCommand(diagnosisCmd)
}
