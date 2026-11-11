package cmd

import (
	"os"

	"github.com/spf13/cobra"
	"github.com/wandelbotsgmbh/nova-cli/pkg/httpx"
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
		host := currentHost()
		resp, err := httpx.Client().Post("http://"+host+"/diagnose/sendslack", "application/json", nil)
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
		host := currentHost()
		if err := nova.DownloadDiagnosisPackage(host); err != nil {
			return err
		}
		if *diagnosisDownloadOutput != outputJSON {
			return nil
		}
		info, err := os.Stat(nova.DiagnosisPackageFile)
		if err != nil {
			return err
		}
		return writeJSON(cmd, downloadedDiagnosis{Path: absPath(nova.DiagnosisPackageFile), Bytes: info.Size()})
	},
}

// downloadedDiagnosis is the JSON result of 'diagnosis download -o json'.
type downloadedDiagnosis struct {
	Path string `json:"path"`
	// size of the written file
	Bytes int64 `json:"bytes"`
}

// -o/--output of 'diagnosis download'
var diagnosisDownloadOutput *outputFormat

func init() {
	diagnosisDownloadOutput = addOutputFlag(downloadZipCmd)
	diagnosisCmd.AddCommand(sendSlackCmd)
	diagnosisCmd.AddCommand(downloadZipCmd)
	rootCmd.AddCommand(diagnosisCmd)
}
