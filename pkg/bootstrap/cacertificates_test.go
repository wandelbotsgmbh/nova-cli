package bootstrap

import (
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// testCertPEM is a self-signed certificate used only for parsing tests.
const testCertPEM = `-----BEGIN CERTIFICATE-----
MIIBhTCCASugAwIBAgIQIRi6zePL6mKjOipn+dNuaTAKBggqhkjOPQQDAjASMRAw
DgYDVQQKEwdBY21lIENvMB4XDTE3MTAyMDE5NDMwNloXDTE4MTAyMDE5NDMwNlow
EjEQMA4GA1UEChMHQWNtZSBDbzBZMBMGByqGSM49AgEGCCqGSM49AwEHA0IABD0d
7VNhbWvZLWPuj/RtHFjvtJBEwOkhbN/BnnE8rnZR8+sbwnc/KhCk3FhnpHZnQz7B
5aETbbIgmuvewdjvSBSjYzBhMA4GA1UdDwEB/wQEAwICpDATBgNVHSUEDDAKBggr
BgEFBQcDATAPBgNVHRMBAf8EBTADAQH/MCkGA1UdEQQiMCCCDmxvY2FsaG9zdDo1
NDUzgg4xMjcuMC4wLjE6NTQ1MzAKBggqhkjOPQQDAgNIADBFAiEA2zpJEPQyz6/l
Wf86aX6PepsntZv2GYlA5UpabfT2EZICICpJ5h/iI+i341gBmLiAFQOyTDT+/wQc
6MF9+Yw1Yy0t
-----END CERTIFICATE-----
`

func writeTempPEM(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cert.pem")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write temp pem: %v", err)
	}
	return path
}

func TestReadCACertificates(t *testing.T) {
	good := writeTempPEM(t, testCertPEM)

	t.Run("single valid", func(t *testing.T) {
		out, err := readCACertificates([]string{good})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if strings.Count(out, "BEGIN CERTIFICATE") != 1 {
			t.Errorf("expected 1 cert block, got %d", strings.Count(out, "BEGIN CERTIFICATE"))
		}
	})

	t.Run("multiple concatenated", func(t *testing.T) {
		out, err := readCACertificates([]string{good, good})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := strings.Count(out, "BEGIN CERTIFICATE"); got != 2 {
			t.Errorf("expected 2 cert blocks, got %d", got)
		}
	})

	t.Run("none provided", func(t *testing.T) {
		if _, err := readCACertificates(nil); err == nil {
			t.Fatal("expected error for empty input")
		}
	})

	t.Run("missing file", func(t *testing.T) {
		if _, err := readCACertificates([]string{"/no/such/file.pem"}); err == nil {
			t.Fatal("expected error for missing file")
		}
	})

	t.Run("not a certificate", func(t *testing.T) {
		bad := writeTempPEM(t, "definitely not pem\n")
		if _, err := readCACertificates([]string{bad}); err == nil {
			t.Fatal("expected error for non-PEM input")
		}
	})

	t.Run("der is converted to pem", func(t *testing.T) {
		// Extract the DER bytes from the test PEM and write them raw.
		block, _ := pem.Decode([]byte(testCertPEM))
		if block == nil {
			t.Fatal("failed to decode test PEM")
		}
		derPath := filepath.Join(t.TempDir(), "cert.der")
		if err := os.WriteFile(derPath, block.Bytes, 0o600); err != nil {
			t.Fatalf("write der: %v", err)
		}

		out, err := readCACertificates([]string{derPath})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(out, "BEGIN CERTIFICATE") {
			t.Errorf("DER input was not converted to PEM: %q", out)
		}
	})
}

func TestRenderCAConfigMapIsValidYAML(t *testing.T) {
	out, err := renderCAConfigMap("wandelbots", testCertPEM)
	if err != nil {
		t.Fatalf("renderCAConfigMap: %v", err)
	}

	var cm struct {
		Metadata struct {
			Name      string `yaml:"name"`
			Namespace string `yaml:"namespace"`
		} `yaml:"metadata"`
		Data map[string]string `yaml:"data"`
	}
	// renderCAConfigMap emits a leading comment + document separator, which
	// yaml.v3 handles as a single document.
	if err := yaml.Unmarshal([]byte(out), &cm); err != nil {
		t.Fatalf("configmap is not valid yaml: %v\n%s", err, out)
	}
	if cm.Metadata.Namespace != "wandelbots" {
		t.Errorf("namespace = %q, want wandelbots", cm.Metadata.Namespace)
	}
	if !strings.Contains(cm.Data[caConfigMapKey], "BEGIN CERTIFICATE") {
		t.Errorf("configmap data missing certificate: %q", cm.Data[caConfigMapKey])
	}
}

func TestRenderCAPolicyIsValidYAML(t *testing.T) {
	out, err := renderCAPolicy("wandelbots", DefaultCAInitImage, nil)
	if err != nil {
		t.Fatalf("renderCAPolicy: %v", err)
	}

	// Kyverno's {{ element.name }} must survive verbatim for the webhook.
	if !strings.Contains(out, `name: "{{ element.name }}"`) {
		t.Error("expected literal Kyverno variable {{ element.name }} in policy")
	}

	// No target apps: the policy must not contain a label selector.
	if strings.Contains(out, "matchExpressions") {
		t.Error("unexpected selector when no target apps given")
	}

	var policy map[string]any
	if err := yaml.Unmarshal([]byte(out), &policy); err != nil {
		t.Fatalf("policy is not valid yaml: %v\n%s", err, out)
	}
	if policy["kind"] != "ClusterPolicy" {
		t.Errorf("kind = %v, want ClusterPolicy", policy["kind"])
	}
}

func TestResolveCATargetApps(t *testing.T) {
	t.Run("default narrow scope", func(t *testing.T) {
		got := resolveCATargetApps(false, nil)
		if len(got) == 0 || got[0] != "service-manager-backend" {
			t.Errorf("got %v, want default %v", got, DefaultCATargetApps)
		}
	})
	t.Run("explicit list wins", func(t *testing.T) {
		got := resolveCATargetApps(false, []string{"settings", "backup"})
		if len(got) != 2 || got[0] != "settings" {
			t.Errorf("got %v", got)
		}
	})
	t.Run("all-apps means no selector", func(t *testing.T) {
		if got := resolveCATargetApps(true, nil); got != nil {
			t.Errorf("got %v, want nil", got)
		}
	})
}

func TestRenderCAPolicyWithTargetApps(t *testing.T) {
	out, err := renderCAPolicy("wandelbots", DefaultCAInitImage,
		[]string{"service-manager-backend", "settings"})
	if err != nil {
		t.Fatalf("renderCAPolicy: %v", err)
	}

	var policy map[string]any
	if err := yaml.Unmarshal([]byte(out), &policy); err != nil {
		t.Fatalf("policy is not valid yaml: %v\n%s", err, out)
	}
	for _, want := range []string{
		"app.kubernetes.io/name",
		"operator: In",
		"- service-manager-backend",
		"- settings",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("policy missing %q\n%s", want, out)
		}
	}
	// Selector must appear in both rules.
	if n := strings.Count(out, "matchExpressions"); n != 2 {
		t.Errorf("matchExpressions count = %d, want 2", n)
	}
}

func TestRenderKyvernoHelmChartInsecureSkipTLS(t *testing.T) {
	off, err := renderKyvernoHelmChart(Config{})
	if err != nil {
		t.Fatalf("renderKyvernoHelmChart: %v", err)
	}
	if strings.Contains(off, "insecureSkipTLSVerify") {
		t.Errorf("did not expect insecureSkipTLSVerify when flag is unset:\n%s", off)
	}
	out, err := renderKyvernoHelmChart(Config{InsecureSkipTLSVerify: true})
	if err != nil {
		t.Fatalf("renderKyvernoHelmChart: %v", err)
	}
	if !strings.Contains(out, "insecureSkipTLSVerify: true") {
		t.Errorf("expected insecureSkipTLSVerify when flag is set:\n%s", out)
	}
	if err := yaml.Unmarshal([]byte(out), &map[string]any{}); err != nil {
		t.Fatalf("kyverno helmchart is not valid yaml: %v\n%s", err, out)
	}
}
