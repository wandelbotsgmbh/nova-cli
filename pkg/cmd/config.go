package cmd

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/wandelbotsgmbh/nova-cli/pkg/credentials"
	"github.com/wandelbotsgmbh/nova-cli/pkg/docker"
)

var keys = map[string]struct{}{
	"host":              {},
	"createcell":        {},
	"cellname":          {},
	"virtualid":         {},
	"image-registry":    {},
	"editor":            {},
	"nucleus-host":      {},
	"nucleus-token":     {},
	"container-runtime": {},
}

var configCmd = &cobra.Command{
	Use:     "config",
	Short:   "Configure the CLI",
	Long:    ``,
	Example: configSetTxtExample,
}

var setCmd = &cobra.Command{
	Use:               "set",
	Short:             "Sets global configuration values",
	Long:              configSetTxtLong,
	Example:           configSetTxtExample,
	ValidArgsFunction: completeConfig,
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) != 2 {
			return errors.New("please provide a config key and a value")
		}

		if _, ok := keys[args[0]]; !ok {
			return fmt.Errorf("unsupported config key: %s", args[0])
		}

		if args[0] == "container-runtime" && args[1] != docker.RuntimeNameDocker && args[1] != docker.RuntimeNamePodman {
			return fmt.Errorf("invalid value %q for container-runtime: must be \"docker\" or \"podman\"", args[1])
		}

		viper.Set(args[0], args[1])
		return viper.WriteConfig()
	},
}

var setRegistryAuthCmd = &cobra.Command{
	Use:   "basic-auth",
	Short: "(deprecated) sets basic auth for the virtual instance",
	Long:  `(deprecated) sets basic auth for the virtual instance`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) != 2 {
			return errors.New("please provide a username and a password")
		}
		return credentials.SetBasicAuth(args[0], args[1])
	},
}

var viewCmd = &cobra.Command{
	Use:   "view",
	Short: "Modify the configuration of the Wandelbots NOVA CLI",
	Long:  ``,
	Run: func(cmd *cobra.Command, args []string) {
		settings := viper.AllSettings()
		for key, value := range settings {
			if _, ok := keys[key]; ok {
				cmd.Printf("%s: %v\n", key, value)
			}
		}
	},
}

func init() {
	rootCmd.AddCommand(configCmd)
	configCmd.AddCommand(setCmd)
	configCmd.AddCommand(viewCmd)
	configCmd.AddCommand(setRegistryAuthCmd)
}

func completeConfig(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	var keysOnly []string
	for key := range keys {
		if toComplete == "" || strings.HasPrefix(key, toComplete) {
			keysOnly = append(keysOnly, key)
		}
	}
	return keysOnly, cobra.ShellCompDirectiveNoFileComp
}
