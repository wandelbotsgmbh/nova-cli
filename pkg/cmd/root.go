package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime"

	homedir "github.com/mitchellh/go-homedir"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/wandelbotsgmbh/nova-cli/pkg/debug"
	"github.com/wandelbotsgmbh/nova-cli/pkg/httpx"
	"github.com/wandelbotsgmbh/nova-cli/pkg/version"
	"golang.org/x/term"
)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "nova",
	Short: "CLI for interacting with Wandelbots NOVA OS",
	Long:  `CLI for interacting with Wandelbots NOVA OS, such as creating and installing apps.`,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		argsAccepted = true
		return resolveTarget(cmd, args)
	},
	// Execute prints errors without the usage, which would push the message out
	// of view, and adds a hint to --help for invocation errors.
	SilenceErrors: true,
	SilenceUsage:  true,
}

// argsAccepted is set once flags and arguments were parsed and validated, so an
// error after that comes from the command itself, not from how it was invoked.
var argsAccepted bool

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	rejectUnknownSubcommands(rootCmd)
	if err := executeRoot(); err != nil {
		var exit exitCodeError
		if errors.As(err, &exit) {
			os.Exit(exit.code)
		}
		var typed *typedError
		if errors.As(err, &typed) && typed.exit != 0 {
			os.Exit(typed.exit)
		}
		os.Exit(1)
	}
}

// exitCodeError ends nova with the exit code of a command it ran, which has reported
// its failure itself.
type exitCodeError struct{ code int }

func (e exitCodeError) Error() string { return fmt.Sprintf("exit status %d", e.code) }

// executeRoot runs the root command and prints its error, if any.
func executeRoot() error {
	argsAccepted = false
	cmd, err := rootCmd.ExecuteC()
	if err != nil && !errors.As(err, new(exitCodeError)) {
		cmd.PrintErrln(cmd.ErrPrefix(), err.Error())
		var typed *typedError
		if errors.As(err, &typed) {
			printTypedError(typed)
		}
		if !argsAccepted {
			cmd.PrintErrf("Run '%s --help' for usage.\n", cmd.CommandPath())
		}
	}
	return err
}

// printTypedError adds the hint of an ADR-0010 error and, when stderr is no
// terminal (agents, CI), the error as one JSON line:
// {"error": {"code": "APPROVAL_REQUIRED", "message": …, "hint": …, "retryable": false}}
func printTypedError(e *typedError) {
	if e.Hint != "" {
		fmt.Fprintf(os.Stderr, "Hint: %s\n", e.Hint)
	}
	if stderrIsTerminal() {
		return
	}
	b, err := json.Marshal(map[string]any{"error": e})
	if err == nil {
		fmt.Fprintln(os.Stderr, string(b))
	}
}

// stderrIsTerminal is replaced in tests.
var stderrIsTerminal = func() bool { return term.IsTerminal(int(os.Stderr.Fd())) }

// insecureSkipTLSVerify is bound to the --insecure-skip-tls-verify flag only.
// It is intentionally NOT bound to viper so it is never read from or persisted
// to the config file.
var insecureSkipTLSVerify bool

// debugFlag is bound to --debug only, like --insecure-skip-tls-verify: debug output
// is for one invocation (or a session via NOVA_DEBUG), never persisted.
var debugFlag bool

func init() {
	cobra.OnInitialize(initConfig)
	// run the root's target resolution in addition to the pre-run hooks of subcommands
	cobra.EnableTraverseRunHooks = true

	rootCmd.PersistentFlags().StringVar(&instanceFlag, "instance", "",
		"Name of the instance to work against for this invocation (overrides the selected instance; env: NOVA_INSTANCE)")
	rootCmd.PersistentFlags().StringVar(&envFlag, "env", "",
		"(Wandelbots internal) Portal environment for this invocation: prod, stg or dev (default: the environment of the cloud instance, else of the last 'nova login'; env: NOVA_ENV)")

	// --instance only takes local entries (an unknown name is an error), so no portal lookup
	_ = rootCmd.RegisterFlagCompletionFunc("instance", func(cmd *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return completeLocalInstance(cmd, nil, toComplete)
	})
	_ = rootCmd.RegisterFlagCompletionFunc("env", cobra.FixedCompletions([]string{"prod", "stg", "dev"}, cobra.ShellCompDirectiveNoFileComp))

	rootCmd.PersistentFlags().BoolVar(&debugFlag, "debug", false,
		"Write diagnostic output to stderr: HTTP requests and responses, external commands, the resolved instance. Known secrets are redacted, but bodies (app environments, kubeconfigs) can still contain some: don't share the output unreviewed (env: "+debug.EnvVar+"=1)")
	rootCmd.PersistentFlags().BoolVar(&insecureSkipTLSVerify, "insecure-skip-tls-verify", false,
		"If true, the server's certificate will not be checked for validity (disables TLS verification for all requests)")
}

func initConfig() {
	debug.Enable(debugFlag || debug.EnabledByEnv())
	debug.Logf("nova %s on %s/%s", version.GetCliVersion(), runtime.GOOS, runtime.GOARCH)

	home, err := homedir.Dir()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
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
		fmt.Fprintln(os.Stderr, "Can't read config:", err)
		os.Exit(1)
	}

	debug.Logf("config: %s", viper.ConfigFileUsed())
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
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		f, err := os.OpenFile(configFile, os.O_CREATE|os.O_EXCL|os.O_WRONLY, configFileMode)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Can't create config:", err)
			os.Exit(1)
		}
		if err := f.Close(); err != nil {
			fmt.Fprintln(os.Stderr, "Can't create config:", err)
			os.Exit(1)
		}
		err = viper.WriteConfig()
		if err != nil {
			fmt.Fprintln(os.Stderr, "Can't write config:", err)
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
