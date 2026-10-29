package cmd

import (
	"fmt"
	"os"
	"sync"

	"github.com/schollz/progressbar/v3"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/wandelbotsgmbh/nova-cli/pkg/nova"
)

var backupCmd = &cobra.Command{
	Use:   "backup",
	Short: "Download or install any backup on the current instance",
	Long:  `Download or install any backup on the current instance`,
}

var downloadBackupCmd = &cobra.Command{
	Use:   "download [filename]",
	Short: "Create and download a backup for the current instance",
	Long:  `Create and download a backup for the current instance`,
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		outname := "backup.tar.gz"
		if len(args) > 0 {
			outname = args[0]
		}

		f, err := os.Stat(outname)
		if err == nil && !f.IsDir() {
			return fmt.Errorf("file '%s' already exists", outname)
		}

		bar := progressbar.DefaultBytes(-1)

		progress := make(chan nova.DownloadStatus, 10)
		var wg sync.WaitGroup
		wg.Add(1)
		var lastError error

		go func() {
			for status := range progress {
				if status.Error != nil {
					lastError = status.Error
				} else {
					bar.Describe(status.Message)
					if status.TotalBytes > 0 && bar.GetMax() <= 0 {
						bar.AddMax(status.TotalBytes)
					}

					if status.BytesDownloaded > 0 {
						bar.Set(status.BytesDownloaded)
					}
				}
			}
			wg.Done()
		}()

		go nova.DownloadBackupWithProgress(viper.GetString("host"), outname, progress)

		wg.Wait()

		// seems we need to close the progressbar otherwise (final) message might not be shown
		bar.Close()

		return lastError
	},
}

var restoreBackupCmd = &cobra.Command{
	Use:   "restore <filename>",
	Short: "Restore a backup for the current instance.",
	Long:  `Restore a backup for the current instance.`,
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return nova.RestoreBackup(viper.GetString("host"), args[0])
	},
}

func init() {
	backupCmd.AddCommand(downloadBackupCmd)
	backupCmd.AddCommand(restoreBackupCmd)
	rootCmd.AddCommand(backupCmd)
}
