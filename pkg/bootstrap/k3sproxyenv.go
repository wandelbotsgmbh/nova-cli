package bootstrap

import (
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const (
	// DefaultK3sEnvPath is the host path of the k3s systemd environment file
	// that holds the proxy configuration applied at install time. It is mounted
	// by the reader pod (see templates/k3s-env-reader.yaml.tmpl).
	DefaultK3sEnvPath = "/etc/systemd/system/k3s.service.env"

	// k3sEnvReaderPodName and k3sEnvReaderNamespace identify the transient pod
	// defined in templates/k3s-env-reader.yaml.tmpl.
	k3sEnvReaderPodName   = "nova-k3s-env-reader"
	k3sEnvReaderNamespace = "kube-system"
)

// ResolveK3sProxyEnv populates the proxy fields from the k3s systemd env file
// when UseK3sProxyEnv is set. It must be called after the pull secrets have
// been applied, because the reader pod's image is pulled from a private
// registry.
//
// In dry-run mode the cluster is not touched: empty proxy fields are filled
// with a placeholder so the rendered manifest shows where the node-resolved
// values would go.
func ResolveK3sProxyEnv(cfg *Config) error {
	if !cfg.UseK3sProxyEnv {
		return nil
	}
	if dryRun {
		fillProxyPlaceholders(cfg)
		return nil
	}
	return LoadProxyFromK3sEnv(cfg)
}

// k3sProxyPlaceholder marks proxy values that would be resolved from the k3s
// env file at apply time. Only used to render dry-run manifests.
const k3sProxyPlaceholder = "TODO-resolved-from-k3s.service.env-at-apply-time"

// fillProxyPlaceholders fills empty proxy fields with k3sProxyPlaceholder so a
// dry-run manifest still renders the proxy block.
func fillProxyPlaceholders(cfg *Config) {
	if cfg.HTTPProxy == "" {
		cfg.HTTPProxy = k3sProxyPlaceholder
	}
	if cfg.HTTPSProxy == "" {
		cfg.HTTPSProxy = k3sProxyPlaceholder
	}
	if cfg.NoProxy == "" {
		cfg.NoProxy = k3sProxyPlaceholder
	}
}

// LoadProxyFromK3sEnv reads the proxy environment variables from the k3s
// systemd env file (DefaultK3sEnvPath) on a cluster node and populates any
// unset proxy fields in cfg.
//
// The file is a host-level file with no Kubernetes API representation, so it is
// read by scheduling a short-lived pod that hostPath-mounts the file, prints
// its contents, and is then deleted. Explicit --http-proxy/--https-proxy/
// --no-proxy flags take precedence: only empty fields are filled.
//
// Requires a working kubectl context and permission to create a pod with a
// hostPath volume in kube-system.
func LoadProxyFromK3sEnv(cfg *Config) error {
	if err := ensureKubectl(); err != nil {
		return err
	}

	content, err := readK3sEnvFile(cfg.pullSecretNames())
	if err != nil {
		return err
	}

	env := parseEnvFile(content)
	applyProxyEnv(cfg, env)
	return nil
}

// applyProxyEnv fills empty proxy fields in cfg from the parsed env map,
// preferring the standard *_PROXY keys and falling back to the CONTAINERD_*
// variants that k3s also writes.
func applyProxyEnv(cfg *Config, env map[string]string) {
	pick := func(keys ...string) string {
		for _, k := range keys {
			if v := env[k]; v != "" {
				return v
			}
		}
		return ""
	}
	if cfg.HTTPProxy == "" {
		cfg.HTTPProxy = pick("HTTP_PROXY", "http_proxy", "CONTAINERD_HTTP_PROXY")
	}
	if cfg.HTTPSProxy == "" {
		cfg.HTTPSProxy = pick("HTTPS_PROXY", "https_proxy", "CONTAINERD_HTTPS_PROXY")
	}
	if cfg.NoProxy == "" {
		cfg.NoProxy = pick("NO_PROXY", "no_proxy", "CONTAINERD_NO_PROXY")
	}
}

// parseEnvFile parses a systemd EnvironmentFile-style KEY=VALUE document into a
// map. Blank lines and comments are skipped, and surrounding single/double
// quotes are stripped from values.
func parseEnvFile(content string) map[string]string {
	env := make(map[string]string)
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if len(value) >= 2 {
			if (value[0] == '"' && value[len(value)-1] == '"') ||
				(value[0] == '\'' && value[len(value)-1] == '\'') {
				value = value[1 : len(value)-1]
			}
		}
		env[key] = value
	}
	return env
}

// readK3sEnvFile schedules the transient reader pod (which hostPath-mounts the
// k3s env file on a node), prints its contents, and returns them. The pod is
// always cleaned up. pullSecretNames are added as imagePullSecrets so the
// reader image can be pulled from the private registry.
func readK3sEnvFile(pullSecretNames []string) (string, error) {
	manifest, err := renderManifest("k3s-env-reader.yaml.tmpl", map[string]any{
		"Name":            k3sEnvReaderPodName,
		"Namespace":       k3sEnvReaderNamespace,
		"Path":            DefaultK3sEnvPath,
		"PullSecretNames": pullSecretNames,
	})
	if err != nil {
		return "", err
	}

	// Remove any leftover pod from a previous interrupted run so the apply
	// (fixed name) starts from a clean Succeeded/Failed phase.
	_ = exec.Command("kubectl", "-n", k3sEnvReaderNamespace, "delete", "pod",
		k3sEnvReaderPodName, "--ignore-not-found").Run()

	applyCmd := exec.Command("kubectl", "apply", "-f", "-")
	applyCmd.Stdin = strings.NewReader(manifest)
	if out, err := applyCmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("create k3s-env reader pod: %w\n%s", err, out)
	}
	// Best-effort cleanup regardless of the outcome below.
	defer func() {
		_ = exec.Command("kubectl", "-n", k3sEnvReaderNamespace, "delete", "pod",
			k3sEnvReaderPodName, "--ignore-not-found", "--wait=false").Run()
	}()

	if err := waitForPodTerminal(90 * time.Second); err != nil {
		return "", err
	}

	out, err := exec.Command("kubectl", "-n", k3sEnvReaderNamespace, "logs",
		k3sEnvReaderPodName).Output()
	if err != nil {
		return "", fmt.Errorf("read k3s-env reader pod logs: %w", err)
	}
	content := string(out)
	if strings.TrimSpace(content) == "" {
		return "", fmt.Errorf("k3s env file %q on node was empty or unreadable", DefaultK3sEnvPath)
	}
	return content, nil
}

// waitForPodTerminal polls until the pod reaches Succeeded (file read) or
// Failed (e.g. the file does not exist on the node).
func waitForPodTerminal(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		out, err := exec.Command("kubectl", "-n", k3sEnvReaderNamespace, "get", "pod",
			k3sEnvReaderPodName, "-o", "jsonpath={.status.phase}").Output()
		phase := strings.TrimSpace(string(out))
		if err == nil {
			switch phase {
			case "Succeeded":
				return nil
			case "Failed":
				return fmt.Errorf("k3s-env reader pod failed; the env file likely "+
					"does not exist on the node (%s)", DefaultK3sEnvPath)
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for k3s-env reader pod (phase %q)", phase)
		}
		time.Sleep(2 * time.Second)
	}
}
