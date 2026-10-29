package bootstrap

import (
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

const (
	// DefaultCAInitImage builds the merged CA bundle. It must ship a system CA
	// bundle at /etc/ssl/certs/ca-certificates.crt (alpine does).
	DefaultCAInitImage = "wandelbots.azurecr.io/docker.io/library/alpine:3"

	// kyvernoChartVersion pins the Kyverno Helm chart deployed via the k3s
	// HelmChart controller. Bump deliberately and verify k8s compatibility.
	kyvernoChartVersion = "3.8.1"
)

// DefaultCATargetApps is the set of app.kubernetes.io/name values the CA
// injection targets when neither --target-apps nor --all-apps is given. It is
// intentionally narrow (only the service-manager components that make external
// HTTPS calls) so the policy does not touch unrelated workloads by default.
var DefaultCATargetApps = []string{"service-manager-backend"}

// resolveCATargetApps decides which app.kubernetes.io/name values the CA policy
// should match. Precedence: all-apps (nil selector => every Pod in the
// namespace) > an explicit target list > the narrow default.
func resolveCATargetApps(allApps bool, targetApps []string) []string {
	if allApps {
		return nil
	}
	if len(targetApps) > 0 {
		return targetApps
	}
	return DefaultCATargetApps
}

// readCACertificates reads and validates one or more certificate files,
// returning normalized, concatenated PEM. Each file may be PEM (one or more
// CERTIFICATE blocks) or raw DER (one or more concatenated certificates); DER
// is converted to PEM. It fails fast on missing files or input that contains
// no parseable certificate so the user gets a clear error before anything is
// applied to the cluster.
func readCACertificates(paths []string) (string, error) {
	if len(paths) == 0 {
		return "", fmt.Errorf("no CA certificate files provided")
	}

	var sb strings.Builder
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("read %s: %w", path, err)
		}

		certs, err := parseCertificates(raw)
		if err != nil {
			return "", fmt.Errorf("%s: %w", path, err)
		}

		for _, cert := range certs {
			if err := pem.Encode(&sb, &pem.Block{
				Type:  "CERTIFICATE",
				Bytes: cert.Raw,
			}); err != nil {
				return "", fmt.Errorf("encode certificate from %s: %w", path, err)
			}
		}
	}
	return sb.String(), nil
}

// parseCertificates parses certificates from PEM or DER input. PEM is tried
// first; if no CERTIFICATE blocks are present the bytes are parsed as DER
// (which may be a sequence of concatenated certificates).
func parseCertificates(raw []byte) ([]*x509.Certificate, error) {
	var certs []*x509.Certificate
	rest := raw
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parse PEM certificate: %w", err)
		}
		certs = append(certs, cert)
	}
	if len(certs) > 0 {
		return certs, nil
	}

	// No PEM certificate blocks: try DER (possibly concatenated certs).
	if derCerts, err := x509.ParseCertificates(raw); err == nil && len(derCerts) > 0 {
		return derCerts, nil
	}

	return nil, fmt.Errorf("no certificate found (expected PEM or DER)")
}

// ApplyKyverno installs Kyverno via the k3s HelmChart controller. Kyverno
// provides the admission-time mutation used to inject the proxy CA into Pods.
// Safe to run repeatedly (kubectl apply is idempotent).
func ApplyKyverno(cfg Config) error {
	manifest, err := renderKyvernoHelmChart(cfg)
	if err != nil {
		return err
	}
	return applyManifest("kyverno", manifest)
}

// ApplyCACertificates wires a TLS-inspecting proxy's CA certificate(s) into
// the cluster so that workloads in the target namespace trust the proxy. It:
//  1. installs Kyverno (if not already present),
//  2. creates the proxy-ca-cert ConfigMap from the given PEM files,
//  3. applies a Kyverno ClusterPolicy that injects the CA into every Pod in
//     the target namespace (init container merges system roots + proxy CA and
//     mounts the result over /etc/ssl/certs/ca-certificates.crt, plus sets
//     SSL_CERT_FILE).
//
// This fixes both the C/libcurl TLS stack (Cryptlex LexActivator) and Go's
// net/http, which read the same bundle / honor SSL_CERT_FILE.
func ApplyCACertificates(cfg Config) error {
	if cfg.CAAllApps && len(cfg.CATargetApps) > 0 {
		return fmt.Errorf("cannot combine all-apps with an explicit target-apps list")
	}

	bundle, err := readCACertificates(cfg.CACertPaths)
	if err != nil {
		return err
	}

	initImage := cfg.CAInitImage
	if initImage == "" {
		initImage = DefaultCAInitImage
	}

	// Create the proxy CA ConfigMap in the HelmChart namespace (kube-system)
	// first so the helm-install jobs - including Kyverno's own - can verify
	// chart pulls through a TLS-inspecting proxy via spec.repoCAConfigMap.
	if cfg.RepoCAConfigMap() != "" {
		repoCM, err := renderCAConfigMap(repoCAConfigMapNamespace, bundle)
		if err != nil {
			return err
		}
		if err := applyManifest("proxy-ca-configmap-repo", repoCM); err != nil {
			return err
		}
	}

	if err := ApplyKyverno(cfg); err != nil {
		return fmt.Errorf("install kyverno: %w", err)
	}

	if !dryRun {
		if err := waitForCRD("clusterpolicies.kyverno.io", 3*time.Minute); err != nil {
			return fmt.Errorf("kyverno not ready: %w", err)
		}
	}

	configMap, err := renderCAConfigMap(cfg.Namespace, bundle)
	if err != nil {
		return err
	}
	if err := applyManifest("proxy-ca-configmap", configMap); err != nil {
		return err
	}

	policy, err := renderCAPolicy(cfg.Namespace, initImage, resolveCATargetApps(cfg.CAAllApps, cfg.CATargetApps))
	if err != nil {
		return err
	}
	if err := applyManifest("proxy-ca-policy", policy); err != nil {
		return err
	}

	// Wait until Kyverno's mutating webhook actually covers Pods. The policy
	// reports Ready almost immediately, but there is a short gap before Kyverno
	// registers the webhook; Pods created in that window silently miss injection
	// (observed: a backend pod created 6s after the policy was not mutated).
	if !dryRun {
		if err := waitForMutatingWebhookPods(2 * time.Minute); err != nil {
			return fmt.Errorf("kyverno mutating webhook not ready: %w", err)
		}
	}
	return nil
}

// waitForMutatingWebhookPods polls until Kyverno's resource mutating webhook
// configuration includes a rule for Pods, which signals the proxy-CA policy is
// actually being enforced at admission (not merely Ready).
func waitForMutatingWebhookPods(timeout time.Duration) error {
	if err := ensureKubectl(); err != nil {
		return err
	}
	deadline := time.Now().Add(timeout)
	for {
		out, err := exec.Command("kubectl", "get",
			"mutatingwebhookconfigurations.admissionregistration.k8s.io",
			"kyverno-resource-mutating-webhook-cfg",
			"-o", "jsonpath={.webhooks[*].rules[*].resources}").Output()
		if err == nil && strings.Contains(string(out), "pods") {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for kyverno mutating webhook to cover pods")
		}
		time.Sleep(2 * time.Second)
	}
}

// waitForCRD polls until the named CRD is established or the timeout elapses.
// The k3s Helm controller installs Kyverno asynchronously, so its CRDs may not
// exist immediately after applying the HelmChart CR.
func waitForCRD(name string, timeout time.Duration) error {
	if err := ensureKubectl(); err != nil {
		return err
	}
	deadline := time.Now().Add(timeout)
	for {
		if err := exec.Command("kubectl", "get", "crd", name).Run(); err == nil {
			// CRD exists; give the webhook a moment to register.
			_ = exec.Command("kubectl", "-n", "kyverno", "wait", "--for=condition=Available",
				"deployment", "--all", "--timeout=600s").Run()
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for CRD %s", name)
		}
		time.Sleep(5 * time.Second)
	}
}
