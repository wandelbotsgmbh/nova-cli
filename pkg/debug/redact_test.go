package debug

import (
	"net/url"
	"strings"
	"testing"
)

const jwt0 = "eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJ1c2VyIn0.c2lnbmF0dXJl"

func TestRedact(t *testing.T) {
	for _, tc := range []struct {
		name, in, want string
	}{
		{"json token", `{"access_token":"abc","expires_in":86400}`, `{"access_token":"[redacted]","expires_in":86400}`},
		{"json escaped quote", `{"password": "a\"b", "user": "x"}`, `{"password": "[redacted]", "user": "x"}`},
		{"json nested name", `{"nucleus-token":"t1","host":"h"}`, `{"nucleus-token":"[redacted]","host":"h"}`},
		{"form", `grant_type=refresh_token&refresh_token=r1&client_id=c`, `grant_type=refresh_token&refresh_token=[redacted]&client_id=c`},
		{"device code", `device_code=d1&client_id=c`, `device_code=[redacted]&client_id=c`},
		{"bearer", `Authorization was Bearer abc.def`, `Authorization was Bearer [redacted]`},
		{"jwt", `got ` + jwt0 + ` back`, `got [redacted] back`},
		{"cli flag", `ssh host --token=s3cret run`, `ssh host --token=[redacted] run`},
		{"plain text kept", `Error 401: Unauthorized`, `Error 401: Unauthorized`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Redact(tc.in); got != tc.want {
				t.Errorf("Redact(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// AGENTS.md: never print kubeconfig contents. Credentials in it are redacted,
// the structure stays readable.
func TestRedactKubeconfig(t *testing.T) {
	in := `apiVersion: v1
clusters:
- cluster:
    certificate-authority-data: Q0E=
    server: https://10.0.0.1:6443
users:
- name: default
  user:
    client-certificate-data: Q0VSVA==
    client-key-data: S0VZ
    token: abc
`
	got := Redact(in)
	for _, secret := range []string{"Q0VSVA==", "S0VZ", "abc"} {
		if strings.Contains(got, secret) {
			t.Errorf("kubeconfig secret %q not redacted:\n%s", secret, got)
		}
	}
	for _, kept := range []string{"server: https://10.0.0.1:6443", "client-key-data: [redacted]", "- name: default"} {
		if !strings.Contains(got, kept) {
			t.Errorf("redacted kubeconfig lacks %q:\n%s", kept, got)
		}
	}
}

func TestRedactURL(t *testing.T) {
	u, _ := url.Parse("https://user:pw@h.example/api?token=t1&cell=cell")
	got := RedactURL(u)
	if strings.Contains(got, "pw") || strings.Contains(got, "t1") {
		t.Errorf("RedactURL = %q, leaks a secret", got)
	}
	if !strings.Contains(got, "cell=cell") || !strings.Contains(got, "token=[redacted]") {
		t.Errorf("RedactURL = %q, want other parameters kept", got)
	}
}

func TestRedactHeader(t *testing.T) {
	for _, tc := range []struct{ name, value, want string }{
		{"Authorization", "Bearer " + jwt0, "Bearer [redacted]"},
		{"Authorization", "Basic dXNlcjpwdw==", "Basic [redacted]"},
		{"Cookie", "_oauth2_proxy=abc; other=1", "[redacted]"},
		{"Set-Cookie", "session=abc; Path=/", "[redacted]"},
		{"X-Api-Key", "k1", "[redacted]"},
		{"Content-Type", "application/json", "application/json"},
	} {
		if got := RedactHeader(tc.name, tc.value); got != tc.want {
			t.Errorf("RedactHeader(%s, %q) = %q, want %q", tc.name, tc.value, got, tc.want)
		}
	}
}

func TestEnabledByEnv(t *testing.T) {
	for v, want := range map[string]bool{"": false, "0": false, "false": false, "1": true, "true": true, "api": true} {
		t.Setenv(EnvVar, v)
		if got := EnabledByEnv(); got != want {
			t.Errorf("NOVA_DEBUG=%q: EnabledByEnv() = %v, want %v", v, got, want)
		}
	}
}

// A JSON value cut off by the body limit is redacted, also when the cut falls right
// after a backslash (an escaped \n in a PEM key).
func TestRedactCutOffValue(t *testing.T) {
	for _, in := range []string{`{"private_key":"-----BEGIN KEY-----\nMIIEv`, `{"private_key":"-----BEGIN KEY-----\`} {
		if got := Redact(in); got != `{"private_key":"[redacted]"` {
			t.Errorf("Redact(%q) = %q", in, got)
		}
	}
}
