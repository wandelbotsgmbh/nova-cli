// Package bootstrap implements the NOVA bootstrapping logic for on-premise
// IPC installations. It handles device registration, pull-secret retrieval,
// and applying Kubernetes manifests via kubectl so that the Ansible installer
// no longer needs to carry this logic.
package bootstrap

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"

	"github.com/wandelbotsgmbh/nova-cli/pkg/oauth"
)

const (
	// DefaultNamespace is the target namespace for the service-manager.
	DefaultNamespace = "wandelbots"

	// DefaultOCIRegistry is the default OCI registry for the chart.
	DefaultOCIRegistry = "wandelbots.azurecr.io"

	// DefaultOCIChart is the default chart path within the registry.
	DefaultOCIChart = "helm/service-manager"

	// DefaultPullSecretName is the default docker registry secret name used by
	// the HelmChart CR.
	DefaultPullSecretName = "pull-secret-wandelbots-azurecr-io"
)

// Config holds all parameters for a bootstrap run.
type Config struct {
	// ServiceManager chart settings.
	OCIRegistry    string
	OCIChart       string
	ChartVersion   string
	PullSecretName string

	// ReleaseChannel is the developer-portal channel used to resolve the latest
	// version when ChartVersion is empty or the DefaultChartVersion sentinel.
	ReleaseChannel string

	// Namespace for the service-manager deployment.
	Namespace string

	// CACertPaths are PEM files holding a TLS-inspecting proxy's CA
	// certificate(s). When set, the proxy CA is injected into the trust store
	// of workloads in Namespace via Kyverno.
	CACertPaths []string

	// CAInitImage is the image used to merge system roots with the proxy CA.
	// Defaults to DefaultCAInitImage when empty.
	CAInitImage string

	// CATargetApps optionally restricts CA injection to Pods whose
	// app.kubernetes.io/name label is in this list. When empty (and CAAllApps
	// is false) DefaultCATargetApps is used.
	CATargetApps []string

	// CAAllApps injects the proxy CA into every Pod in Namespace instead of the
	// default narrow target set. Mutually exclusive with CATargetApps.
	CAAllApps bool

	// Proxy settings. When set they are written into the service-manager chart
	// values (global.proxy.*) so deployed workloads use the proxy, and exported
	// to the CLI process so its own portal/Auth0 calls go through the proxy.
	HTTPProxy  string
	HTTPSProxy string
	NoProxy    string

	// UseK3sProxyEnv reads the proxy settings from the k3s systemd env file
	// (DefaultK3sEnvPath) on a cluster node instead of taking them from the
	// HTTPProxy/HTTPSProxy/NoProxy fields. Explicitly-set proxy fields still
	// take precedence over values discovered from the node.
	UseK3sProxyEnv bool

	// InsecureSkipTLSVerify disables TLS certificate verification when the
	// Helm controller pulls the chart from the OCI registry. When true the
	// generated HelmChart CR gets "insecureSkipTLSVerify: true".
	InsecureSkipTLSVerify bool

	// DryRun prints manifests to stdout instead of applying via kubectl.
	DryRun bool
}

// DefaultConfig returns a Config with production defaults.
func DefaultConfig() Config {
	return Config{
		OCIRegistry:    DefaultOCIRegistry,
		OCIChart:       DefaultOCIChart,
		PullSecretName: DefaultPullSecretName,
		Namespace:      DefaultNamespace,
	}
}

// pullSecretNames returns the docker-registry secret names to attach to pods
// that pull from the private registry. It falls back to the default when no
// secret name is configured.
func (c Config) pullSecretNames() []string {
	name := c.PullSecretName
	if name == "" {
		name = DefaultPullSecretName
	}
	return []string{name}
}

// loadEnvironment reads the auth environment from the stored login token.
// The user sets this via "nova login --env <env>".
func loadEnvironment() (oauth.AuthEnvironment, error) {
	token, err := oauth.LoadToken()
	if err != nil {
		return "", fmt.Errorf("load token: %w (did you run 'nova login'?)", err)
	}
	if token.AuthEnvironment == "" {
		return oauth.AuthProd, nil
	}
	return token.AuthEnvironment, nil
}

// ensureKubectl checks that kubectl is available in PATH.
func ensureKubectl() error {
	if _, err := exec.LookPath("kubectl"); err != nil {
		return fmt.Errorf("kubectl not found in PATH: %w", err)
	}
	return nil
}

// dryRun is set by the command layer when --dry-run is used.
var dryRun bool

// RepoCAConfigMap returns the name of the ConfigMap to wire into a HelmChart's
// spec.repoCAConfigMap so the helm-install jobs trust a TLS-inspecting proxy
// when pulling charts. It is empty when no proxy CA is supplied or when TLS
// verification is being skipped (insecureSkipTLSVerify takes precedence).
func (c Config) RepoCAConfigMap() string {
	if c.InsecureSkipTLSVerify || len(c.CACertPaths) == 0 {
		return ""
	}
	return caConfigMapName
}

// SetDryRun enables or disables dry-run mode for all manifest operations.
func SetDryRun(enabled bool) {
	dryRun = enabled
}

// applyManifest applies a Kubernetes manifest via "kubectl apply -f -".
// In dry-run mode it prints the manifest to stdout instead.
func applyManifest(description, content string) error {
	if dryRun {
		fmt.Print(content)
		return nil
	}
	if err := ensureKubectl(); err != nil {
		return err
	}
	cmd := exec.Command("kubectl", "apply", "-f", "-")
	cmd.Stdin = strings.NewReader(content)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("kubectl apply (%s): %w\n%s", description, err, stderr.String())
	}
	fmt.Print(string(output))
	return nil
}
