package cmd

import (
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/cobra/doc"
)

var genDocDir string

var genDoc = &cobra.Command{
	Use:    "gendoc",
	Short:  "Generates the documentation for the CLI",
	Hidden: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := os.MkdirAll(genDocDir, os.ModePerm); err != nil {
			return err
		}
		disableAutoGenTag(rootCmd)
		return doc.GenMarkdownTree(rootCmd, genDocDir)
	},
}

// cobra only inherits DisableAutoGenTag on pages with a "SEE ALSO" section,
// so it is set on every command to keep the date out of all pages.
func disableAutoGenTag(c *cobra.Command) {
	c.DisableAutoGenTag = true
	for _, sub := range c.Commands() {
		disableAutoGenTag(sub)
	}
}

func init() {
	genDoc.Flags().StringVar(&genDocDir, "dir", "_docs", "Output directory for the generated markdown files")
	rootCmd.AddCommand(genDoc)
}
