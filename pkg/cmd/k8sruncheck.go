package cmd

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/wandelbotsgmbh/nova-cli/pkg/httpx"
	"github.com/wandelbotsgmbh/nova-cli/pkg/instances"
	"gopkg.in/yaml.v3"
)

// kubeCheckTimeout bounds the request to a cluster's Kubernetes API.
var kubeCheckTimeout = 5 * time.Second

// clusterGone tells whether a k8srun cluster is gone, with the reason. Only definite
// answers count as gone:
//   - the cluster service doesn't know the id (404), or
//   - it knows the id, but its kubeconfig for it has another CA than the stored one, or
//   - (only with probe) the cluster at the stored address rejects the stored kubeconfig.
//     A released VM keeps its IP but gets new credentials, so a rejection means the
//     address serves another cluster. This is a heuristic (a TLS-intercepting proxy
//     looks the same), so callers that probe must ask before removing anything.
//
// err is set when the service can't be asked; the entry must then be kept.
func clusterGone(inst instances.Instance, probe bool) (gone bool, reason string, err error) {
	exists, current, err := clusterKubeconfig(inst.ID)
	if err != nil {
		return false, "", err
	}
	if !exists {
		return true, "the cluster service doesn't know the cluster", nil
	}
	// The stored file may hold more clusters than ours: after 'nova instance env' it is
	// KUBECONFIG, and kubectl, kind etc. add contexts and switch current-context. So
	// compare and probe the context whose server is the one the service names, never
	// the current one; without exactly one such context there is nothing definite.
	svc, err := parseKubeconfig(decodeServiceKubeconfig(current), "")
	if err != nil {
		return false, "", nil
	}
	content, err := os.ReadFile(inst.Kubeconfig)
	if inst.Kubeconfig == "" || err != nil {
		return false, "", nil
	}
	stored, err := parseKubeconfig(content, svc.server)
	if err != nil {
		return false, "", nil
	}
	if len(svc.ca) > 0 && len(stored.ca) > 0 && !bytes.Equal(svc.ca, stored.ca) {
		return true, "the cluster service has another kubeconfig for the cluster id (the stored one is stale)", nil
	}
	if probe {
		if why := kubeconfigRejected(stored); why != "" {
			return true, why, nil
		}
	}
	return false, "", nil
}

// kubeconfigShared returns why prune must leave a k8srun entry alone: its stored
// kubeconfig has more than the one context 'nova k8srun create' wrote. Someone added
// other clusters (it is KUBECONFIG after 'nova instance env'), and removing the entry
// would delete their credentials, so only an explicit 'nova instance remove' may.
func kubeconfigShared(inst instances.Instance) string {
	if inst.Kubeconfig == "" {
		return ""
	}
	content, err := os.ReadFile(inst.Kubeconfig)
	if err != nil {
		return ""
	}
	var doc struct {
		Contexts []yaml.Node `yaml:"contexts"`
	}
	if yaml.Unmarshal(content, &doc) != nil || len(doc.Contexts) <= 1 {
		return ""
	}
	return fmt.Sprintf("its kubeconfig %s has %d contexts, not only the cluster's", inst.Kubeconfig, len(doc.Contexts))
}

// decodeServiceKubeconfig returns the kubeconfig of a cluster service answer, which may
// be the YAML itself, base64 of it, or either as a JSON string.
func decodeServiceKubeconfig(body []byte) []byte {
	body = bytes.TrimSpace(body)
	var s string
	if json.Unmarshal(body, &s) == nil {
		body = []byte(s)
	}
	if b, err := base64.StdEncoding.DecodeString(string(bytes.TrimSpace(body))); err == nil {
		return b
	}
	return body
}

// kubeconfigRejected calls the Kubernetes API of a kubeconfig and returns why it rejects
// the credentials, or "" if it accepts them or can't be asked (timeout, connection
// refused: a cluster that doesn't answer isn't proof that it is gone). Kubeconfigs the
// check can't reproduce faithfully (exec or auth-provider users, files instead of
// inline data, tls-server-name, no credentials) are never probed: a request without
// what kubectl would send could be rejected although the cluster is fine.
func kubeconfigRejected(kc kubeAccess) string {
	if !kc.probeable {
		return ""
	}
	client, err := kc.client()
	if err != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), kubeCheckTimeout)
	defer cancel()
	// /api needs authentication (unlike /readyz or /version, which anonymous requests
	// may read), so stale credentials get a 401
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(kc.server, "/")+"/api", nil)
	if err != nil {
		return ""
	}
	resp, err := client.Do(req)
	if err != nil {
		if certificateRejected(err) {
			return fmt.Sprintf("%s presents a certificate the stored kubeconfig doesn't trust (the VM was handed out again)", kc.server)
		}
		return ""
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Sprintf("%s rejects the stored kubeconfig (the VM was handed out again)", kc.server)
	}
	return ""
}

// certificateRejected tells whether err is a TLS verification failure on either side:
// the server's certificate isn't signed by the stored CA, or the server refused our
// client certificate.
func certificateRejected(err error) bool {
	var unknownAuthority x509.UnknownAuthorityError
	var hostname x509.HostnameError
	var invalid x509.CertificateInvalidError
	var verify *tls.CertificateVerificationError
	if errors.As(err, &unknownAuthority) || errors.As(err, &hostname) || errors.As(err, &invalid) || errors.As(err, &verify) {
		return true
	}
	// alerts sent by the server when it doesn't accept our client certificate
	msg := err.Error()
	return strings.Contains(msg, "tls: bad certificate") || strings.Contains(msg, "tls: unknown certificate authority") ||
		strings.Contains(msg, "tls: certificate required")
}

// kubeAccess is what a request to one context's API server needs.
type kubeAccess struct {
	server   string
	ca       []byte
	insecure bool
	cert     []byte
	key      []byte
	token    string
	// probeable is false when the kubeconfig uses settings the probe doesn't implement
	probeable bool
}

// parseKubeconfig returns the access of the context whose cluster has server, or of the
// current context (the only one, if none is set) when server is "". It fails unless
// exactly one context matches.
func parseKubeconfig(content []byte, server string) (kubeAccess, error) {
	var doc struct {
		CurrentContext string `yaml:"current-context"`
		Contexts       []struct {
			Name    string `yaml:"name"`
			Context struct {
				Cluster string `yaml:"cluster"`
				User    string `yaml:"user"`
			} `yaml:"context"`
		} `yaml:"contexts"`
		Clusters []struct {
			Name    string `yaml:"name"`
			Cluster struct {
				Server        string `yaml:"server"`
				CA            string `yaml:"certificate-authority-data"`
				CAFile        string `yaml:"certificate-authority"`
				TLSServerName string `yaml:"tls-server-name"`
				ProxyURL      string `yaml:"proxy-url"`
				Insecure      bool   `yaml:"insecure-skip-tls-verify"`
			} `yaml:"cluster"`
		} `yaml:"clusters"`
		Users []struct {
			Name string `yaml:"name"`
			User struct {
				Cert         string `yaml:"client-certificate-data"`
				Key          string `yaml:"client-key-data"`
				Token        string `yaml:"token"`
				CertFile     string `yaml:"client-certificate"`
				KeyFile      string `yaml:"client-key"`
				TokenFile    string `yaml:"tokenFile"`
				Username     string `yaml:"username"`
				Exec         any    `yaml:"exec"`
				AuthProvider any    `yaml:"auth-provider"`
			} `yaml:"user"`
		} `yaml:"users"`
	}
	if err := yaml.Unmarshal(content, &doc); err != nil {
		return kubeAccess{}, err
	}
	servers := map[string]string{}
	for _, c := range doc.Clusters {
		servers[c.Name] = c.Cluster.Server
	}
	clusterName, userName, matches := "", "", 0
	for _, c := range doc.Contexts {
		var match bool
		if server == "" {
			match = c.Name == doc.CurrentContext || (doc.CurrentContext == "" && len(doc.Contexts) == 1)
		} else {
			match = servers[c.Context.Cluster] != "" && strings.TrimSuffix(servers[c.Context.Cluster], "/") == strings.TrimSuffix(server, "/")
		}
		if match {
			clusterName, userName = c.Context.Cluster, c.Context.User
			matches++
		}
	}
	if matches != 1 {
		return kubeAccess{}, fmt.Errorf("kubeconfig: %d contexts match", matches)
	}
	a := kubeAccess{probeable: true}
	var err error
	for _, c := range doc.Clusters {
		if c.Name == clusterName {
			a.server, a.insecure = c.Cluster.Server, c.Cluster.Insecure
			if c.Cluster.CAFile != "" || c.Cluster.TLSServerName != "" || c.Cluster.ProxyURL != "" {
				a.probeable = false
			}
			if a.ca, err = decodeData(c.Cluster.CA); err != nil {
				return kubeAccess{}, err
			}
		}
	}
	if a.server == "" {
		return kubeAccess{}, errors.New("kubeconfig: no server for the context")
	}
	for _, u := range doc.Users {
		if u.Name == userName {
			a.token = u.User.Token
			if u.User.CertFile != "" || u.User.KeyFile != "" || u.User.TokenFile != "" ||
				u.User.Username != "" || u.User.Exec != nil || u.User.AuthProvider != nil {
				a.probeable = false
			}
			if a.cert, err = decodeData(u.User.Cert); err != nil {
				return kubeAccess{}, err
			}
			if a.key, err = decodeData(u.User.Key); err != nil {
				return kubeAccess{}, err
			}
		}
	}
	if len(a.cert) == 0 && a.token == "" {
		// an anonymous request gets 401 from any cluster
		a.probeable = false
	}
	return a, nil
}

func decodeData(s string) ([]byte, error) {
	if s == "" {
		return nil, nil
	}
	return base64.StdEncoding.DecodeString(s)
}

// client builds an HTTP client with the kubeconfig's TLS settings. It has its own
// transport (the shared one can't carry per-cluster certificates); httpx still adds the
// User-Agent and --debug logging.
func (a kubeAccess) client() (*http.Client, error) {
	cfg := &tls.Config{InsecureSkipVerify: a.insecure || httpx.InsecureSkipTLSVerify()} //nolint:gosec // as configured
	if len(a.ca) > 0 {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(a.ca) {
			return nil, errors.New("kubeconfig: invalid certificate-authority-data")
		}
		cfg.RootCAs = pool
	}
	if len(a.cert) > 0 {
		pair, err := tls.X509KeyPair(a.cert, a.key)
		if err != nil {
			return nil, err
		}
		cfg.Certificates = []tls.Certificate{pair}
	}
	// direct, without HTTPS_PROXY: a TLS-intercepting proxy would present its own
	// certificate and make every cluster look handed out again
	var rt http.RoundTripper = &http.Transport{TLSClientConfig: cfg}
	if a.token != "" {
		rt = bearerTransport{token: a.token, next: rt}
	}
	return &http.Client{Transport: httpx.WithUserAgent(rt)}, nil
}

type bearerTransport struct {
	token string
	next  http.RoundTripper
}

func (t bearerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("Authorization", "Bearer "+t.token)
	return t.next.RoundTrip(req)
}
