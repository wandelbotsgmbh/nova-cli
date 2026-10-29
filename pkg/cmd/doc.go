package cmd

import (
	"log"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/cobra/doc"
)

var genDoc = &cobra.Command{
	Use:    "gendoc",
	Short:  "Generates the documentation for the CLI",
	Hidden: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		outFolder := "docs"
		if err := os.Mkdir(outFolder, os.ModePerm); !os.IsExist(err) {
			return err
		}

		err := doc.GenMarkdownTree(rootCmd, outFolder)
		if err != nil {
			log.Fatalf("Failed to generate documentation: %v", err)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(genDoc)
}
