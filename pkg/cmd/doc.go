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
		return doc.GenMarkdownTree(rootCmd, genDocDir)
	},
}

func init() {
	genDoc.Flags().StringVar(&genDocDir, "dir", "docs", "Output directory for the generated markdown files")
	rootCmd.AddCommand(genDoc)
}
