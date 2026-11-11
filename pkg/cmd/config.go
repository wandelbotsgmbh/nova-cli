package cmd

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/wandelbotsgmbh/nova-cli/pkg/configfile"
	"github.com/wandelbotsgmbh/nova-cli/pkg/credentials"
	"github.com/wandelbotsgmbh/nova-cli/pkg/docker"
	"github.com/wandelbotsgmbh/nova-cli/pkg/instances"
)

var showSecretsConfigView bool

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
	"simulation-broker": {},
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

		if isNucleusKey(args[0]) {
			// before ADR-0018 '--instance <name> config set nucleus-token' wrote that
			// entry; silently writing the selected server instead could store one
			// server's token on another. NOVA_INSTANCE is ambient (nova instance env)
			// and has no effect here.
			if instanceFlag != "" {
				return fmt.Errorf("%s belongs to a Nucleus server, not an instance: use --nucleus <name> instead of --instance (see 'nova nucleus server list')", args[0])
			}
			return setNucleusValue(cmd, args[0], args[1])
		}
		if field := instanceField(args[0]); field != nil {
			return setInstanceValue(cmd, args[0], args[1])
		}

		return configfile.Set(viper.GetViper(), args[0], args[1])
	},
}

// secretKeys are redacted by 'config view' unless --show-secrets is given.
var secretKeys = map[string]struct{}{"nucleus-token": {}}

// isNucleusKey reports whether key belongs to the Nucleus server, not the instance
// (ADR-0018).
func isNucleusKey(key string) bool { return key == "nucleus-host" || key == "nucleus-token" }

// instanceField returns the entry field for a key stored per instance, or nil.
func instanceField(key string) func(*instances.Instance) *string {
	switch key {
	case "host":
		return func(i *instances.Instance) *string { return &i.Host }
	case "cellname":
		return func(i *instances.Instance) *string { return &i.CellName }
	case "virtualid":
		return func(i *instances.Instance) *string { return &i.ID }
	}
	return nil
}

// setInstanceValue writes a per-instance key to the targeted entry (honouring
// --instance / NOVA_INSTANCE), or creates the entry "default" if there is none.
func setInstanceValue(cmd *cobra.Command, key, value string) error {
	r, err := loadRegistry()
	if err != nil {
		return err
	}
	t, _, err := r.Resolve(instanceFlag, os.Getenv(instances.EnvVar))
	if err != nil {
		return err
	}
	if t.Name == "" {
		if len(r.Instances) > 0 {
			return fmt.Errorf("%w: select one with 'nova instance select <name>' or pass --instance <name>", instances.ErrNoTarget)
		}
		t = instances.Instance{Name: instances.DefaultName, Kind: instances.KindHost}
		if key == "host" {
			t.Kind = instances.GuessKind(value, "")
		}
		r.Current = t.Name
		fmt.Fprintf(cmd.ErrOrStderr(), "created instance %q\n", t.Name)
	}
	*instanceField(key)(&t) = value
	if err := r.Put(t); err != nil {
		return err
	}
	return saveRegistry(r)
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
	Short: "Shows the configuration of the Wandelbots NOVA CLI",
	Long: `Shows the configuration of the Wandelbots NOVA CLI: global settings plus the settings
of the targeted instance (selected, or set via --instance / NOVA_INSTANCE) and of the
Nucleus server (selected, or set via --nucleus / NOVA_NUCLEUS).
Tokens are redacted unless --show-secrets is given, also with -o json.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		t, err := ensureTarget()
		if err != nil {
			return err
		}
		nuc, nucSource, err := resolveNucleus()
		if err != nil {
			return err
		}
		values := map[string]string{}
		for key := range keys {
			if key == "nucleus-host" {
				values[key] = nuc.Host
			} else if key == "nucleus-token" {
				values[key] = nuc.Token
			} else if key == "cellname" {
				values[key] = t.Cell()
			} else if field := instanceField(key); field != nil {
				values[key] = *field(&t)
			} else if viper.IsSet(key) {
				values[key] = viper.GetString(key)
			}
		}
		for key, value := range values {
			if _, secret := secretKeys[key]; secret && value != "" && !showSecretsConfigView {
				values[key] = instances.Redact
			}
		}

		if *configViewOutput == outputJSON {
			view := configView{
				Env:      configSource{Name: string(portalEnvironment), Source: envSource},
				Settings: values,
			}
			if t.Name != "" {
				view.Instance = &configInstance{Name: t.Name, Kind: string(t.Kind), Source: string(targetSource)}
			}
			if nuc.Name != "" {
				view.Nucleus = &configSource{Name: nuc.Name, Source: string(nucSource)}
			}
			return writeJSON(cmd, view)
		}

		if t.Name != "" {
			values["instance"] = fmt.Sprintf("%s (%s, from %s)", t.Name, t.Kind, targetSource)
		}
		values["env"] = fmt.Sprintf("%s (from %s)", portalEnvironment, envSource)
		if nuc.Name != "" {
			values["nucleus"] = fmt.Sprintf("%s (from %s)", nuc.Name, nucSource)
		}

		sorted := make([]string, 0, len(values))
		for key := range values {
			sorted = append(sorted, key)
		}
		sort.Strings(sorted)
		out := cmd.OutOrStdout()
		for _, key := range sorted {
			fmt.Fprintf(out, "%s: %v\n", key, values[key])
		}
		return nil
	},
}

// configView is the JSON result of 'config view -o json'.
type configView struct {
	// the targeted instance; null when none is selected
	Instance *configInstance `json:"instance"`
	// the portal environment of this invocation
	Env configSource `json:"env"`
	// the targeted Nucleus server; null when there is none
	Nucleus *configSource `json:"nucleus"`
	// the config keys of 'config set' with their values in effect; tokens are
	// redacted unless --show-secrets
	Settings map[string]string `json:"settings"`
}

// configSource is a name and where it was chosen: flag, env, config (or default
// for the portal environment).
type configSource struct {
	Name   string `json:"name"`
	Source string `json:"source"`
}

type configInstance struct {
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	Source string `json:"source"`
}

// -o/--output of 'config view'
var configViewOutput *outputFormat

func init() {
	rootCmd.AddCommand(configCmd)
	configCmd.AddCommand(setCmd)
	configCmd.AddCommand(viewCmd)
	configCmd.AddCommand(setRegistryAuthCmd)

	configViewOutput = addOutputFlag(viewCmd)
	viewCmd.Flags().BoolVar(&showSecretsConfigView, "show-secrets", false, "Print tokens instead of redacting them.")
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
