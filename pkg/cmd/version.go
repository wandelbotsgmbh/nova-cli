package cmd

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/wandelbotsgmbh/nova-cli/pkg/nova"
	"github.com/wandelbotsgmbh/nova-cli/pkg/version"
)

var printVersionVerbose bool

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version of the NOVA CLI and the NOVA OS instance",
	Long: `Print the version of the NOVA CLI, and with --verbose the version of the NOVA OS instance
and the versions it can be updated to.

-o json prints {"cli": …}, with --verbose plus "nova", "available_stable" and "available_next"
(each left out when unknown, e.g. when the instance can't be reached).`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cliVersion := version.GetCliVersion()
		if *versionOutput == outputJSON {
			return writeJSON(cmd, versionInfo(cliVersion))
		}
		fmt.Fprintln(cmd.OutOrStdout(), "cli version:", cliVersion)

		if printVersionVerbose {
			host := currentHost()
			if novaVersion, err := nova.GetNovaVersion(host); err == nil {
				updateAvailable := getUpdateAvailable(host)
				fmt.Fprintln(cmd.OutOrStdout(), "nova version:", novaVersion, updateAvailable)
			}
		}
		return nil
	},
}

// versions is the JSON result of 'version -o json'. The instance fields are only
// set with --verbose, and left out when the instance can't be reached (like the
// text output, which then only prints the cli version).
type versions struct {
	CLI string `json:"cli"`
	// NOVA version of the targeted instance
	Nova string `json:"nova,omitempty"`
	// newer versions the instance can be updated to ('version set stable|next')
	AvailableStable string `json:"available_stable,omitempty"`
	AvailableNext   string `json:"available_next,omitempty"`
}

func versionInfo(cliVersion string) versions {
	v := versions{CLI: cliVersion}
	if !printVersionVerbose {
		return v
	}
	host := currentHost()
	novaVersion, err := nova.GetNovaVersion(host)
	if err != nil {
		return v
	}
	v.Nova = unquoteVersion(novaVersion)
	stable, _ := nova.CheckNovaVersionUpdate(host, "stable")
	v.AvailableStable = unquoteVersion(stable)
	next, _ := nova.CheckNovaVersionUpdate(host, "next")
	v.AvailableNext = unquoteVersion(next)
	return v
}

// unquoteVersion returns a version from an API answer, which may be a JSON string.
func unquoteVersion(s string) string {
	s = strings.TrimSpace(s)
	var v string
	if json.Unmarshal([]byte(s), &v) == nil {
		return v
	}
	return s
}

// -o/--output of 'version'
var versionOutput *outputFormat

var versionSetCmd = &cobra.Command{
	Use:   "set",
	Short: "Sets the version of the NOVA OS instance",
	Long:  `Sets the version of the NOVA OS instance (stable, next or a specific version)`,
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		host := currentHost()
		return nova.SetNovaVersion(host, strings.TrimSpace(args[0]))
	},
}

func getUpdateAvailable(host string) string {
	updateText := ""
	stable, _ := nova.CheckNovaVersionUpdate(host, "stable")
	if stable != "" {
		updateText += fmt.Sprintf("(available stable version: %s)", stable)
	}

	next, _ := nova.CheckNovaVersionUpdate(host, "next")
	if next != "" {
		updateText += fmt.Sprintf("(available next version: %s)", next)
	}

	return updateText
}

func init() {
	versionCmd.AddCommand(versionSetCmd)
	rootCmd.AddCommand(versionCmd)

	versionOutput = addOutputFlag(versionCmd)
	versionCmd.Flags().BoolVarP(&printVersionVerbose, "verbose", "v", false, "Print all available version information.")
}
