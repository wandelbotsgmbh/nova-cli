package cmd

import (
	"fmt"
	"os"
	"runtime"

	homedir "github.com/mitchellh/go-homedir"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/wandelbotsgmbh/nova-cli/pkg/httpx"
)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "nova",
	Short: "CLI for interacting with Wandelbots NOVA OS",
	Long:  `CLI for interacting with Wandelbots NOVA OS, such as creating and installing apps.`,
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	rejectUnknownSubcommands(rootCmd)
	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}

// insecureSkipTLSVerify is bound to the --insecure-skip-tls-verify flag only.
// It is intentionally NOT bound to viper so it is never read from or persisted
// to the config file.
var insecureSkipTLSVerify bool

func init() {
	cobra.OnInitialize(initConfig)

	rootCmd.PersistentFlags().BoolVar(&insecureSkipTLSVerify, "insecure-skip-tls-verify", false,
		"If true, the server's certificate will not be checked for validity (disables TLS verification for all requests)")
}

func initConfig() {
	home, err := homedir.Dir()
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	configPath := home + "/.nova"
	configFile := home + "/.nova/config.yaml"

	viper.AddConfigPath(configPath)
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath(".")
	viper.SetConfigPermissions(configFileMode)

	viper.SetDefault("host", "")
	viper.SetDefault("createcell", "true")
	viper.SetDefault("cellname", "cell")

	// TODO: maybe there is a better way to achieve this
	viper.SetDefault("image-registry", "")
	viper.SetDefault("registryuser", "")
	viper.SetDefault("container-runtime", "")

	createConfigFileIfMissing(configFile, configPath)

	err = viper.ReadInConfig()
	if err != nil {
		fmt.Println("Can't read config:", err)
		os.Exit(1)
	}

	httpx.SetInsecureSkipTLSVerify(insecureSkipTLSVerify)
}

// The config holds portal logins and nucleus tokens, so only its owner may read it.
const (
	configDirMode  os.FileMode = 0o700
	configFileMode os.FileMode = 0o600
)

func createConfigFileIfMissing(configFile string, configPath string) {
	if _, err := os.Stat(configFile); os.IsNotExist(err) {
		// Config file not found; let's create it
		if err := os.MkdirAll(configPath, configDirMode); err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
		f, err := os.OpenFile(configFile, os.O_CREATE|os.O_EXCL|os.O_WRONLY, configFileMode)
		if err != nil {
			fmt.Println("Can't create config:", err)
			os.Exit(1)
		}
		if err := f.Close(); err != nil {
			fmt.Println("Can't create config:", err)
			os.Exit(1)
		}
		err = viper.WriteConfig()
		if err != nil {
			fmt.Println("Can't write config:", err)
			os.Exit(1)
		}
	}
	restrictPermissions(configPath, configDirMode)
	restrictPermissions(configFile, configFileMode)
}

// restrictPermissions removes the permission bits beyond mode, e.g. from a config
// created world-readable by older CLI versions. Windows doesn't use these bits.
func restrictPermissions(path string, mode os.FileMode) {
	if runtime.GOOS == "windows" {
		return
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm()&^mode == 0 {
		return
	}
	restricted := info.Mode().Perm() & mode
	if err := os.Chmod(path, restricted); err != nil {
		fmt.Fprintf(os.Stderr, "warning: %s is readable by other users and can't be restricted: %v\n", path, err)
		return
	}
	fmt.Fprintf(os.Stderr, "note: restricted %s to mode %04o, it holds login tokens\n", path, restricted)
}
