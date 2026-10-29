package cmd

import (
	"fmt"
	"os"

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

func createConfigFileIfMissing(configFile string, configPath string) {
	if _, err := os.Stat(configFile); os.IsNotExist(err) {
		// Config file not found; let's create it
		if err := os.MkdirAll(configPath, 0755); err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
		_, err = os.Create(configFile)
		if err != nil {
			fmt.Println("Can't create config:", err)
			os.Exit(1)
		}
		err = viper.WriteConfig()
		if err != nil {
			fmt.Println("Can't write config:", err)
			os.Exit(1)
		}
	}
}
