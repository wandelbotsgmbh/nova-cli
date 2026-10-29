package bootstrap

import (
	"strings"
)

const (
	// caConfigMapName is the ConfigMap holding the proxy CA bundle. It is
	// created in the target namespace and referenced by injected Pods.
	caConfigMapName = "proxy-ca-cert"

	// caConfigMapKey is the data key inside caConfigMapName.
	caConfigMapKey = "proxy-ca.crt"

	// repoCAConfigMapNamespace is the namespace the HelmChart CRs live in; the
	// proxy CA ConfigMap referenced by spec.repoCAConfigMap must be co-located.
	repoCAConfigMapNamespace = "kube-system"
)

// renderKyvernoHelmChart builds the Kyverno HelmChart CR manifest.
func renderKyvernoHelmChart(cfg Config) (string, error) {
	return renderManifest("kyverno.yaml.tmpl", struct {
		Version               string
		InsecureSkipTLSVerify bool
		RepoCAConfigMap       string
	}{
		Version:               kyvernoChartVersion,
		InsecureSkipTLSVerify: cfg.InsecureSkipTLSVerify,
		RepoCAConfigMap:       cfg.RepoCAConfigMap(),
	})
}

// renderCAConfigMap builds the ConfigMap manifest, indenting the PEM bundle as
// a YAML block scalar.
func renderCAConfigMap(namespace, bundle string) (string, error) {
	var indented strings.Builder
	for _, line := range strings.Split(strings.TrimRight(bundle, "\n"), "\n") {
		indented.WriteString("    ")
		indented.WriteString(line)
		indented.WriteByte('\n')
	}
	return renderManifest("ca-configmap.yaml.tmpl", struct {
		Name           string
		Namespace      string
		Key            string
		IndentedBundle string
	}{
		Name:           caConfigMapName,
		Namespace:      namespace,
		Key:            caConfigMapKey,
		IndentedBundle: indented.String(),
	})
}

// renderCAPolicy builds the namespace-scoped Kyverno ClusterPolicy manifest.
func renderCAPolicy(namespace, initImage string, appNames []string) (string, error) {
	var sb strings.Builder
	err := caPolicyTemplate.ExecuteTemplate(&sb, "ca-policy.kyverno.tmpl", struct {
		Namespace     string
		ConfigMapName string
		ConfigMapKey  string
		InitImage     string
		AppNames      []string
	}{
		Namespace:     namespace,
		ConfigMapName: caConfigMapName,
		ConfigMapKey:  caConfigMapKey,
		InitImage:     initImage,
		AppNames:      appNames,
	})
	if err != nil {
		return "", err
	}
	return sb.String(), nil
}
