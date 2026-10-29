package cmd

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/wandelbotsgmbh/nova-cli/pkg/bootstrap"
)

var bootstrapDryRun bool

var bootstrapCfg bootstrap.Config

var bootstrapCmd = &cobra.Command{
	Use:     "bootstrap",
	Short:   "Bootstrap NOVA on an on-premise IPC",
	Long:    bootstrapLong,
	Example: bootstrapExample,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		bootstrap.SetDryRun(bootstrapDryRun)
		bootstrapCfg.InsecureSkipTLSVerify = insecureSkipTLSVerify
		return nil
	},
}

var bootstrapNamespaceCmd = &cobra.Command{
	Use:   "namespace",
	Short: "Create the target namespace",
	Long:  bootstrapNamespaceLong,
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return bootstrap.ApplyNamespace(bootstrapCfg)
	},
}

var bootstrapReflectorCmd = &cobra.Command{
	Use:   "reflector",
	Short: "Deploy Ember Stack Reflector for secret mirroring",
	Long:  bootstrapReflectorLong,
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return bootstrap.ApplyReflector(bootstrapCfg)
	},
}

var bootstrapPullSecretsCmd = &cobra.Command{
	Use:   "pull-secrets",
	Short: "Write container registry pull secrets",
	Long:  bootstrapPullSecretsLong,
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		return bootstrap.WritePullSecrets(ctx, bootstrapCfg)
	},
}

var bootstrapServiceManagerCmd = &cobra.Command{
	Use:   "service-manager",
	Short: "Write the service-manager HelmChart manifest",
	Long:  bootstrapServiceManagerLong,
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := resolveProxyEnv(cmd); err != nil {
			return err
		}
		return bootstrap.WriteServiceManager(bootstrapCfg)
	},
}

var bootstrapSystemOwnerCmd = &cobra.Command{
	Use:   "system-owner",
	Short: "Write the system-owner Secret manifest",
	Long:  bootstrapSystemOwnerLong,
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		return bootstrap.WriteSystemOwner(ctx, bootstrapCfg)
	},
}

var bootstrapCACertsCmd = &cobra.Command{
	Use:   "ca-certificates <pem-file> [pem-file...]",
	Short: "Trust a TLS-inspecting proxy's CA in cluster workloads",
	Long:  bootstrapCACertsLong,
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		bootstrapCfg.CACertPaths = args
		return bootstrap.ApplyCACertificates(bootstrapCfg)
	},
}

var bootstrapAllCmd = &cobra.Command{
	Use:   "all",
	Short: "Run all bootstrap steps in sequence",
	Long:  bootstrapAllLong,
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()

		cmd.Printf("Step 1/5: Creating namespace %q...\n", bootstrapCfg.Namespace)
		if err := bootstrap.ApplyNamespace(bootstrapCfg); err != nil {
			return fmt.Errorf("namespace: %w", err)
		}

		// Proxy CA injection must run before any workloads are created: the
		// Kyverno policy only mutates Pods at admission time, so Kyverno and the
		// policy have to be in place before the service-manager pods spin up.
		if len(bootstrapCfg.CACertPaths) > 0 {
			cmd.Println("Injecting proxy CA certificates (before workloads)...")
			if err := bootstrap.ApplyCACertificates(bootstrapCfg); err != nil {
				return fmt.Errorf("ca-certificates: %w", err)
			}
		}

		cmd.Println("Step 2/5: Deploying Reflector...")
		if err := bootstrap.ApplyReflector(bootstrapCfg); err != nil {
			return fmt.Errorf("reflector: %w", err)
		}

		cmd.Println("Step 3/5: Applying pull secrets...")
		if err := bootstrap.WritePullSecrets(ctx, bootstrapCfg); err != nil {
			return fmt.Errorf("pull-secrets: %w", err)
		}

		// Resolve proxy values only now: the k3s-env reader pod's image is
		// pulled from the private registry, so the pull secrets must exist first.
		if err := resolveProxyEnv(cmd); err != nil {
			return err
		}

		cmd.Println("Step 4/5: Applying service-manager manifest...")
		if err := bootstrap.WriteServiceManager(bootstrapCfg); err != nil {
			return fmt.Errorf("service-manager: %w", err)
		}

		cmd.Println("Step 5/5: Applying system-owner manifest...")
		if err := bootstrap.WriteSystemOwner(ctx, bootstrapCfg); err != nil {
			return fmt.Errorf("system-owner: %w", err)
		}

		cmd.Println("Bootstrap complete!")
		return nil
	},
}

// resolveProxyEnv populates the proxy fields from the k3s env file when
// --use-k3s-proxy-env is set. It is called only after pull secrets exist,
// since the reader pod image comes from the private registry.
func resolveProxyEnv(cmd *cobra.Command) error {
	if !bootstrapCfg.UseK3sProxyEnv {
		return nil
	}
	if bootstrapDryRun {
		cmd.Println("--use-k3s-proxy-env: dry-run, using placeholder proxy values (no node read)")
	} else {
		cmd.Printf("Reading proxy settings from %s on a cluster node...\n",
			bootstrap.DefaultK3sEnvPath)
	}
	if err := bootstrap.ResolveK3sProxyEnv(&bootstrapCfg); err != nil {
		return fmt.Errorf("--use-k3s-proxy-env: %w", err)
	}
	return nil
}

func init() {
	// Shared flags on the parent command, inherited by all subcommands.
	bootstrapCmd.PersistentFlags().BoolVar(&bootstrapDryRun, "dry-run", false,
		"Print manifests to stdout instead of applying via kubectl")
	bootstrapCmd.PersistentFlags().StringVar(&bootstrapCfg.Namespace, "namespace", bootstrap.DefaultNamespace,
		"Target Kubernetes namespace")
	bootstrapCmd.PersistentFlags().StringVar(&bootstrapCfg.HTTPProxy, "http-proxy", "",
		"HTTP proxy for deployed workloads (global.proxy.httpProxy); does not affect the CLI's own calls")
	bootstrapCmd.PersistentFlags().StringVar(&bootstrapCfg.HTTPSProxy, "https-proxy", "",
		"HTTPS proxy for deployed workloads (global.proxy.httpsProxy); does not affect the CLI's own calls")
	bootstrapCmd.PersistentFlags().StringVar(&bootstrapCfg.NoProxy, "no-proxy", "",
		"Comma-separated NO_PROXY entries (global.proxy.noProxy); does not affect the CLI's own calls")
	bootstrapCmd.PersistentFlags().BoolVar(&bootstrapCfg.UseK3sProxyEnv, "use-k3s-proxy-env", false,
		"Read HTTP_PROXY/HTTPS_PROXY/NO_PROXY from "+bootstrap.DefaultK3sEnvPath+" on a cluster node "+
			"(via a transient pod) instead of passing --http-proxy/--https-proxy/--no-proxy; explicit flags take precedence")

	// service-manager specific flags
	bootstrapServiceManagerCmd.Flags().StringVar(&bootstrapCfg.ChartVersion, "version", bootstrap.DefaultChartVersion,
		"Service-manager chart version (default resolves the latest from the portal)")
	bootstrapServiceManagerCmd.Flags().StringVar(&bootstrapCfg.ReleaseChannel, "channel", bootstrap.DefaultReleaseChannel,
		"Release channel used to resolve the latest version: stable or next")

	bootstrapServiceManagerCmd.Flags().StringVar(&bootstrapCfg.OCIRegistry, "oci-registry", bootstrap.DefaultOCIRegistry,
		"OCI registry for the chart")
	bootstrapServiceManagerCmd.Flags().StringVar(&bootstrapCfg.OCIChart, "oci-chart", bootstrap.DefaultOCIChart,
		"Chart path within the OCI registry")
	bootstrapServiceManagerCmd.Flags().StringVar(&bootstrapCfg.PullSecretName, "pull-secret", bootstrap.DefaultPullSecretName,
		"Docker registry secret name for chart pulls")

	// all command also needs the version flag
	bootstrapAllCmd.Flags().StringVar(&bootstrapCfg.ChartVersion, "version", bootstrap.DefaultChartVersion,
		"Service-manager chart version (default resolves the latest from the portal)")
	bootstrapAllCmd.Flags().StringVar(&bootstrapCfg.ReleaseChannel, "channel", bootstrap.DefaultReleaseChannel,
		"Release channel used to resolve the latest version: stable or next")

	bootstrapAllCmd.Flags().StringVar(&bootstrapCfg.OCIRegistry, "oci-registry", bootstrap.DefaultOCIRegistry,
		"OCI registry for the chart")
	bootstrapAllCmd.Flags().StringVar(&bootstrapCfg.OCIChart, "oci-chart", bootstrap.DefaultOCIChart,
		"Chart path within the OCI registry")
	bootstrapAllCmd.Flags().StringVar(&bootstrapCfg.PullSecretName, "pull-secret", bootstrap.DefaultPullSecretName,
		"Docker registry secret name for chart pulls")

	// ca-certificates flags (subcommand and the optional step in "all").
	bootstrapCACertsCmd.Flags().StringVar(&bootstrapCfg.CAInitImage, "init-image", bootstrap.DefaultCAInitImage,
		"Image used to merge system roots with the proxy CA (must ship /etc/ssl/certs/ca-certificates.crt)")
	bootstrapCACertsCmd.Flags().StringSliceVar(&bootstrapCfg.CATargetApps, "target-apps", nil,
		"Restrict CA injection to Pods whose app.kubernetes.io/name is in this list (default: service-manager only)")
	bootstrapCACertsCmd.Flags().BoolVar(&bootstrapCfg.CAAllApps, "all-apps", false,
		"Inject the proxy CA into every Pod in the namespace (overrides the default service-manager-only scope)")
	bootstrapAllCmd.Flags().StringSliceVar(&bootstrapCfg.CACertPaths, "ca-certificates", nil,
		"Proxy CA PEM file(s) to trust cluster-wide; comma-separated or repeat the flag")
	bootstrapAllCmd.Flags().StringVar(&bootstrapCfg.CAInitImage, "ca-init-image", bootstrap.DefaultCAInitImage,
		"Image used to merge system roots with the proxy CA")
	bootstrapAllCmd.Flags().StringSliceVar(&bootstrapCfg.CATargetApps, "ca-target-apps", nil,
		"Restrict CA injection to Pods whose app.kubernetes.io/name is in this list (default: service-manager only)")
	bootstrapAllCmd.Flags().BoolVar(&bootstrapCfg.CAAllApps, "ca-all-apps", false,
		"Inject the proxy CA into every Pod in the namespace (overrides the default service-manager-only scope)")

	bootstrapCmd.AddCommand(bootstrapNamespaceCmd)
	bootstrapCmd.AddCommand(bootstrapReflectorCmd)
	bootstrapCmd.AddCommand(bootstrapPullSecretsCmd)
	bootstrapCmd.AddCommand(bootstrapServiceManagerCmd)
	bootstrapCmd.AddCommand(bootstrapSystemOwnerCmd)
	bootstrapCmd.AddCommand(bootstrapCACertsCmd)
	bootstrapCmd.AddCommand(bootstrapAllCmd)
	rootCmd.AddCommand(bootstrapCmd)
}
