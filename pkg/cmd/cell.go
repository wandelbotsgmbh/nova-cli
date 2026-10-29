package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/wandelbotsgmbh/nova-cli/pkg/editor"
	"github.com/wandelbotsgmbh/nova-cli/pkg/nova"
	"github.com/wandelbotsgmbh/nova-cli/pkg/specfile"
	"github.com/wandelbotsgmbh/nova-cli/pkg/utils"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// If this is true, the file will be saved in json format, otherwise in yaml format
var saveJson bool

var cellCmd = &cobra.Command{
	Use:   "cell",
	Short: "Manage cell setups",
	Long:  `Manage cell setups`,
}

var deleteCmd = &cobra.Command{
	Use:               "delete [name]",
	Args:              cobra.MaximumNArgs(1),
	ValidArgsFunction: completeCell,
	Short:             "Deletes a cell",
	Long: `Deletes an existing cell.
If no name is given, the default cellname is used (which can be changed with 'nova config set cellname <name>').`,
	RunE: func(cmd *cobra.Command, args []string) error {
		host := viper.GetString("host")
		cell := viper.GetString("cellname")
		if len(args) > 0 {
			cell = args[0]
		}
		return nova.DeleteCell(host, cell)
	},
}

var addCmd = &cobra.Command{
	Use:   "add [name]",
	Args:  cobra.MaximumNArgs(1),
	Short: "Adds a new cell",
	Long: `Add a new cell with default configuration.
If no name is given, the default cellname is used (which can be changed with 'nova config set cellname <name>').`,
	RunE: func(cmd *cobra.Command, args []string) error {
		host := viper.GetString("host")
		name := viper.GetString("cellname")
		if len(args) > 0 {
			name = args[0]
		}
		err := utils.VerifyProjectName(name)
		if err != nil {
			return err
		}
		return nova.CreateCell(host, name)
	},
}

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List all existing cells",
	Long:  ``,
	RunE: func(cmd *cobra.Command, args []string) error {
		host := viper.GetString("host")
		cells, err := nova.ListCells(host)
		if err != nil {
			return err
		}
		for _, cell := range cells {
			cmd.Println(cell)
		}
		return nil
	},
}

var saveCmd = &cobra.Command{
	Use:               "save [cellname]",
	Args:              cobra.MaximumNArgs(1),
	ValidArgsFunction: completeCell,
	Short:             "Save the cell setup to file",
	Long: `Saves the cell configuration to a file.

The filename will be "cellname.cell". The content can be either in json or yaml format (select via --json flag).

If no name is given, the default cellname is used (which can be changed with 'nova config set cellname <name>').`,
	Example: `
	# save the default cell configuration to yaml file
	nova cell save

	# save the default cell configuration to json file
	nova cell save --json

	# save a specific cell configuration to yaml
	nova cell save mycell
`,
	RunE: func(cmd *cobra.Command, args []string) error {
		host := viper.GetString("host")
		cell := viper.GetString("cellname")
		if len(args) > 0 {
			cell = args[0]
		}

		var cellData []byte

		if saveJson {
			tmp, err := nova.GetCellJson(host, cell)
			if err != nil {
				return err
			}
			cellData = tmp
		} else {
			tmp, err := nova.GetCellYaml(host, cell)
			if err != nil {
				return err
			}
			cellData = tmp
		}

		os.Remove(cell + ".cell")
		file, err := os.Create(cell + ".cell")
		if err != nil {
			return err
		}
		defer file.Close()

		_, err = file.Write(cellData)
		if err != nil {
			return err
		}

		return nil
	},
}

var installCmd = &cobra.Command{
	Use:   "install <source>",
	Args:  cobra.ExactArgs(1),
	Short: "Install a cell from source",
	Long: `Installs cell configurations from a given source.

This command will install the cell depending on the provided source, where source can be either a file or a url.
The content can be either in json or yaml format and should comply with the cell configuration specification (see 'http://host/api/v1/ui/#/operations/updateCell').

If the cell already exists, the configuration will be updated according to the provided specification.

NOTE: When executing this command, the CLI configuration will be updated with the cell name from the provided file.
This is the equivalent of running 'nova config set cellname <name>' with the cell name from the given file.
`,
	Example: `
	# install from json file
	nova cell install path/to/app.json

	# install from yaml file
	nova cell install path/to/cell.yaml
	# or
	nova cell install path/to/cell.yml

	# install from json or yaml from remote location
	nova cell install https://somehost.com/path/to/cell.yaml?latest=true
`,
	RunE: func(cmd *cobra.Command, args []string) error {
		host := viper.GetString("host")

		if !specfile.IsSpec(args[0]) {
			return fmt.Errorf("file '%s' is not a valid spec file (yaml, yml, json, or url)", args[0])
		}

		specfile, err := specfile.UnmarshalSpec(args[0])
		if err != nil {
			return err
		}

		cellName, ok := specfile["name"].(string)
		if !ok {
			return errors.New("spec file does not contain a name field. check that the file contains a valid cell configuration")
		}

		// TODO is this nice?
		viper.Set("cellname", cellName)

		cellSetup, err := json.Marshal(specfile)
		if err != nil {
			return err
		}

		_, err = nova.CreateCellIfNotExists(host, cellName)
		if err != nil {
			return err
		}

		// Use wrapper function which automatically detects V1 or V2 API
		return nova.UpdateConfigurationFromJson(host, cellName, cellSetup)
	},
}

var editCellCmd = &cobra.Command{
	Use:               "edit [name]",
	Args:              cobra.MaximumNArgs(1),
	ValidArgsFunction: completeCell,
	Short:             "edit a cell configuration",
	Long: `Edits the configuration of a cell directly in your favorite editor.
If no name is given, the default cellname is used (which can be changed with 'nova config set cellname <name>').
	
By default the platform editor is used (e.g. vi on linux).
If the files content changed, the cell configuration is updated after closing the editor.

If you want to change the editor you can call e.g. 'nova config set editor "code --wait"' to use Visual Studio Code.
	`,
	RunE: func(cmd *cobra.Command, args []string) error {
		host := viper.GetString("host")
		cellName := viper.GetString("cellname")
		if len(args) > 0 {
			cellName = args[0]
		}

		var cell []byte
		cell, err := nova.GetCellYaml(host, cellName)
		if err != nil {
			return err
		}

		e := editor.NewDefaultEditor()
		c, err := e.EditInTempfile("edit", "cell.yaml", cell)
		if err != nil {
			return err
		}

		if bytes.Equal(cell, c) {
			return nil
		}

		return nova.UpdateConfigurationFromYaml(host, cellName, c)
	},
}

func completeCell(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	cells, err := nova.ListCells(viper.GetString("host"))
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	var comp []string
	for _, cell := range cells {
		if toComplete == "" || strings.HasPrefix(cell, toComplete) {
			comp = append(comp, cell)
		}
	}

	return comp, cobra.ShellCompDirectiveNoFileComp
}

func init() {
	cellCmd.AddCommand(deleteCmd)
	cellCmd.AddCommand(addCmd)
	cellCmd.AddCommand(listCmd)
	cellCmd.AddCommand(saveCmd)
	cellCmd.AddCommand(installCmd)
	cellCmd.AddCommand(editCellCmd)
	rootCmd.AddCommand(cellCmd)

	saveCmd.Flags().BoolVar(&saveJson, "json", false, "If set, the file will be save in json format. Otherwise it will be stored in yaml format.")
}

// Checks for certain field namings to decide if the given spec is v1 or v2.
func isCellV1Spec(spec map[string]any) bool {
	// check app spec in cell
	if apps, ok := spec["apps"]; ok {
		if appList, ok := apps.([]any); ok {
			if len(appList) > 0 {
				app := appList[0]
				if appMap, ok := app.(map[string]any); ok {
					// in v2 it would be "app_icon"
					if _, ok := appMap["appIcon"]; ok {
						return true
					}
				}
			}
		}
	}

	// check the controllers
	if controllers, ok := spec["controllers"]; ok {
		if cList, ok := controllers.([]any); ok {
			if len(cList) > 0 {
				c := cList[0]
				if cMap, ok := c.(map[string]any); ok {
					cc := cMap["configuration"]
					if ccMap, ok := cc.(map[string]any); ok {
						// in v2 it would be "initial_joint_position"
						if _, ok := ccMap["position"]; ok {
							return true
						}

						// in v1 it would be "controllerIp"
						if _, ok := ccMap["controllerIp"]; ok {
							return true
						}
					}
				}
			}
		}
	}

	return false
}
