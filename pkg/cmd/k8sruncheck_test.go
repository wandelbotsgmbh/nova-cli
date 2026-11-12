package cmd

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/viper"
	"github.com/wandelbotsgmbh/nova-cli/pkg/instances"
)

// fakeKubeAPI answers /api with status, over TLS with its own CA.
func fakeKubeAPI(t *testing.T, status int) *httptest.Server {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// kubeconfigFor writes a kubeconfig for server that trusts the certificate of caFrom.
func kubeconfigFor(t *testing.T, server string, caFrom *httptest.Server) string {
	t.Helper()
	return kubeconfigWithCA(t, server, caFrom.Certificate().Raw)
}

// otherCA returns a self-signed certificate (DER) unrelated to httptest's.
func otherCA(t *testing.T) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "other-cluster-ca"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return der
}

func kubeconfigWithCA(t *testing.T, server string, caDER []byte) string {
	t.Helper()
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
	content := fmt.Sprintf(`apiVersion: v1
kind: Config
current-context: default
clusters:
- name: default
  cluster:
    server: %s
    certificate-authority-data: %s
contexts:
- name: default
  context: {cluster: default, user: default}
users:
- name: default
  user: {token: t0k3n}
`, server, base64.StdEncoding.EncodeToString(ca))
	path := filepath.Join(t.TempDir(), "kubeconfig")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestKubeconfigRejected(t *testing.T) {
	ok := fakeKubeAPI(t, http.StatusOK)
	unauthorized := fakeKubeAPI(t, http.StatusUnauthorized)
	forbidden := fakeKubeAPI(t, http.StatusForbidden)
	closed := fakeKubeAPI(t, http.StatusOK)
	closedURL := closed.URL
	closed.Close()

	for _, tc := range []struct {
		name     string
		path     string
		rejected bool
	}{
		{"accepted", kubeconfigFor(t, ok.URL, ok), false},
		{"401", kubeconfigFor(t, unauthorized.URL, unauthorized), true},
		{"other CA (VM handed out again)", kubeconfigWithCA(t, ok.URL, otherCA(t)), true},
		{"403 is no proof", kubeconfigFor(t, forbidden.URL, forbidden), false},
		{"not answering", kubeconfigFor(t, closedURL, ok), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			kc, err := parseKubeconfig([]byte(mustReadFile(t, tc.path)), "")
			if err != nil {
				t.Fatal(err)
			}
			why := kubeconfigRejected(kc)
			if (why != "") != tc.rejected {
				t.Errorf("rejected = %q, want rejected %v", why, tc.rejected)
			}
		})
	}
}

// Kubeconfigs with settings the probe doesn't implement are never probed: without
// them, a fine cluster could reject the request.
func TestKubeconfigNotProbeable(t *testing.T) {
	unauthorized := fakeKubeAPI(t, http.StatusUnauthorized)
	base := mustReadFile(t, kubeconfigFor(t, unauthorized.URL, unauthorized))
	for name, edit := range map[string]func(string) string{
		"exec user": func(s string) string {
			return strings.Replace(s, "user: {token: t0k3n}", "user: {exec: {command: kubelogin}}", 1)
		},
		"client cert file": func(s string) string {
			return strings.Replace(s, "user: {token: t0k3n}", "user: {client-certificate: /c.pem, client-key: /k.pem}", 1)
		},
		"no credentials": func(s string) string { return strings.Replace(s, "user: {token: t0k3n}", "user: {}", 1) },
		"tls-server-name": func(s string) string {
			return strings.Replace(s, "    server:", "    tls-server-name: kubernetes\n    server:", 1)
		},
	} {
		t.Run(name, func(t *testing.T) {
			kc, err := parseKubeconfig([]byte(edit(base)), "")
			if err != nil {
				t.Fatal(err)
			}
			if why := kubeconfigRejected(kc); why != "" {
				t.Errorf("must not be probed, got %q", why)
			}
		})
	}
}

func TestDecodeServiceKubeconfig(t *testing.T) {
	yamlDoc := "apiVersion: v1\nkind: Config\n"
	b64 := base64.StdEncoding.EncodeToString([]byte(yamlDoc))
	for _, body := range []string{yamlDoc, b64, `"` + b64 + `"`, "\"apiVersion: v1\\nkind: Config\\n\"\n"} {
		if got := strings.TrimSpace(string(decodeServiceKubeconfig([]byte(body)))); got != strings.TrimSpace(yamlDoc) {
			t.Errorf("decode(%q) = %q", body, got)
		}
	}
}

func mustReadFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestKubeconfigRejectedSendsToken(t *testing.T) {
	var auth string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
	}))
	defer srv.Close()
	kc, err := parseKubeconfig([]byte(mustReadFile(t, kubeconfigFor(t, srv.URL, srv))), "")
	if err != nil {
		t.Fatal(err)
	}
	if why := kubeconfigRejected(kc); why != "" {
		t.Fatal(why)
	}
	if auth != "Bearer t0k3n" {
		t.Errorf("Authorization = %q", auth)
	}
}

func TestK8sRunPruneDropsRejectedKubeconfig(t *testing.T) {
	withConfig(t, "")
	fake := newFakeK8sRunService(t)
	mustRun(t, "k8srun", "create", "--name", "ours")   // id-1
	mustRun(t, "k8srun", "create", "--name", "reused") // id-2
	r, _ := instances.Load(viper.GetViper())
	ok, unauthorized := fakeKubeAPI(t, http.StatusOK), fakeKubeAPI(t, http.StatusUnauthorized)
	for name, kc := range map[string]string{
		"ours":   kubeconfigFor(t, ok.URL, ok),
		"reused": kubeconfigFor(t, unauthorized.URL, unauthorized),
	} {
		inst, _ := r.Get(name)
		content, _ := os.ReadFile(kc)
		if err := os.WriteFile(inst.Kubeconfig, content, 0o600); err != nil {
			t.Fatal(err)
		}
		// the service still hands out the stored kubeconfig (its database drifted)
		fake.kubeconfigs[inst.ID] = string(content)
	}

	// 'nova instance prune' removes without asking, so it doesn't probe
	out, errOut, err := runWithConfig(t, "instance", "prune", "--dry-run")
	if err != nil || out != "" {
		t.Fatalf("instance prune must keep both: out=%q err=%v\n%s", out, err, errOut)
	}

	out, errOut, err = runWithConfig(t, "k8srun", "prune", "-y")
	if err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	if out != "removed reused\n" || !strings.Contains(errOut, `"reused" is gone:`) || !strings.Contains(errOut, "rejects the stored kubeconfig") {
		t.Errorf("out=%q errOut=%q", out, errOut)
	}
	r, _ = instances.Load(viper.GetViper())
	if _, ok := r.Get("ours"); !ok {
		t.Error("ours must be kept")
	}
	if _, ok := r.Get("reused"); ok {
		t.Error("reused must be removed")
	}
}

// The service's kubeconfig for the id has another CA than the stored one: definite,
// so 'nova instance prune' removes the entry too.
func TestPruneDropsStaleKubeconfig(t *testing.T) {
	withConfig(t, "")
	fake := newFakeK8sRunService(t)
	mustRun(t, "k8srun", "create", "--name", "same")  // id-1
	mustRun(t, "k8srun", "create", "--name", "stale") // id-2
	srv := fakeKubeAPI(t, http.StatusOK)
	r, _ := instances.Load(viper.GetViper())
	for _, name := range []string{"same", "stale"} {
		inst, _ := r.Get(name)
		if err := os.WriteFile(inst.Kubeconfig, []byte(mustReadFile(t, kubeconfigFor(t, srv.URL, srv))), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	fake.kubeconfigs["id-1"] = mustReadFile(t, kubeconfigFor(t, srv.URL, srv))
	fake.kubeconfigs["id-2"] = mustReadFile(t, kubeconfigWithCA(t, srv.URL, otherCA(t)))

	out, errOut, err := runWithConfig(t, "instance", "prune")
	if err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	if out != "removed stale\n" || !strings.Contains(errOut, "the stored one is stale") {
		t.Errorf("out=%q errOut=%q", out, errOut)
	}
}

// After 'nova instance env', the stored file is KUBECONFIG: kubectl or kind add other
// clusters and switch current-context. Neither prune may judge by those.
func TestPruneIgnoresOtherContexts(t *testing.T) {
	withConfig(t, "")
	fake := newFakeK8sRunService(t)
	mustRun(t, "k8srun", "create", "--name", "ours") // id-1
	r, _ := instances.Load(viper.GetViper())
	inst, _ := r.Get("ours")
	ours := fakeKubeAPI(t, http.StatusOK)
	other := fakeKubeAPI(t, http.StatusUnauthorized) // rejects our token
	fake.kubeconfigs[inst.ID] = mustReadFile(t, kubeconfigFor(t, ours.URL, ours))

	ca := base64.StdEncoding.EncodeToString(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ours.Certificate().Raw}))
	otherCA := base64.StdEncoding.EncodeToString(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: otherCA(t)}))
	merged := fmt.Sprintf(`apiVersion: v1
kind: Config
current-context: kind-other
clusters:
- name: default
  cluster: {server: %s, certificate-authority-data: %s}
- name: kind-other
  cluster: {server: %s, certificate-authority-data: %s}
contexts:
- name: default
  context: {cluster: default, user: default}
- name: kind-other
  context: {cluster: kind-other, user: default}
users:
- name: default
  user: {token: t0k3n}
`, ours.URL, ca, other.URL, otherCA)
	if err := os.WriteFile(inst.Kubeconfig, []byte(merged), 0o600); err != nil {
		t.Fatal(err)
	}

	// alive or gone: an entry whose kubeconfig holds other clusters is only removed
	// explicitly, removing it would delete their credentials
	for _, gone := range []bool{false, true} {
		fake.gone[inst.ID] = gone
		for _, args := range [][]string{{"instance", "prune"}, {"k8srun", "prune", "-y"}} {
			out, errOut, err := runWithConfig(t, args...)
			if out != "" || !strings.Contains(errOut, "has 2 contexts") || !strings.Contains(errOut, "nova instance remove ours") {
				t.Errorf("gone=%v %v must keep the entry: out=%q errOut=%q", gone, args, out, errOut)
			}
			// a rule, not a failed check: scripts must not see a failure they can't clear
			if err != nil {
				t.Errorf("gone=%v %v must not fail: %v", gone, args, err)
			}
		}
	}
	if _, err := os.Stat(inst.Kubeconfig); err != nil {
		t.Errorf("the kubeconfig must survive: %v", err)
	}

	// with only our context left, the current-context switch alone changes nothing:
	// the context with the service's server is judged
	fake.gone[inst.ID] = false
	single := strings.Replace(merged, "- name: kind-other\n  context: {cluster: kind-other, user: default}\n", "", 1)
	if err := os.WriteFile(inst.Kubeconfig, []byte(strings.Replace(single, "current-context: kind-other", "current-context: default", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, errOut, err := runWithConfig(t, "k8srun", "prune", "-y"); err != nil || out != "" {
		t.Errorf("single context must be kept: out=%q err=%v\n%s", out, err, errOut)
	}
}

// Another session replaces the entry while the question is open: the new cluster and
// its kubeconfig must survive the "yes".
func TestK8sRunPruneKeepsEntryReplacedMeanwhile(t *testing.T) {
	withConfig(t, "")
	fake := newFakeK8sRunService(t)
	mustRun(t, "k8srun", "create")                    // "k8srun", id-1
	mustRun(t, "k8srun", "create", "--name", "other") // id-2
	fake.gone["id-1"], fake.gone["id-2"] = true, true
	old := afterPruneConfirm
	t.Cleanup(func() { afterPruneConfirm = old })
	afterPruneConfirm = func() {
		// as 'nova k8srun create' replacing the unnamed cluster in another session
		r, err := loadRegistry()
		if err != nil {
			t.Fatal(err)
		}
		for n := range r.Instances {
			if r.Instances[n].Name == "k8srun" {
				r.Instances[n].ID = "id-3"
			}
		}
		if err := saveRegistry(r); err != nil {
			t.Fatal(err)
		}
	}

	out, errOut, err := runWithConfig(t, "k8srun", "prune", "-y")
	if err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	if out != "removed other\n" || !strings.Contains(errOut, `keeping "k8srun": it was changed meanwhile`) {
		t.Errorf("out=%q errOut=%q", out, errOut)
	}
	r, _ := instances.Load(viper.GetViper())
	inst, ok := r.Get("k8srun")
	if !ok || inst.ID != "id-3" {
		t.Fatalf("the new cluster must stay: %+v", inst)
	}
	if _, err := os.Stat(inst.Kubeconfig); err != nil {
		t.Errorf("its kubeconfig must stay: %v", err)
	}
}
