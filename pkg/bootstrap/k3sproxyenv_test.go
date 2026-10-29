package bootstrap

import "testing"

func TestParseEnvFile(t *testing.T) {
	content := `# k3s proxy config
HTTP_PROXY=http://proxy:3128
HTTPS_PROXY="http://proxy:3128"
NO_PROXY='.svc,.cluster.local,10.0.0.0/8'

CONTAINERD_HTTP_PROXY=http://cproxy:3128
`
	env := parseEnvFile(content)
	if env["HTTP_PROXY"] != "http://proxy:3128" {
		t.Errorf("HTTP_PROXY = %q", env["HTTP_PROXY"])
	}
	if env["HTTPS_PROXY"] != "http://proxy:3128" {
		t.Errorf("HTTPS_PROXY = %q (quotes not stripped)", env["HTTPS_PROXY"])
	}
	if env["NO_PROXY"] != ".svc,.cluster.local,10.0.0.0/8" {
		t.Errorf("NO_PROXY = %q", env["NO_PROXY"])
	}
}

func TestApplyProxyEnv(t *testing.T) {
	t.Run("fills empty fields, falls back to containerd vars", func(t *testing.T) {
		cfg := &Config{}
		applyProxyEnv(cfg, map[string]string{
			"HTTP_PROXY":             "http://http:3128",
			"CONTAINERD_HTTPS_PROXY": "http://chttps:3128",
			"NO_PROXY":               ".svc",
		})
		if cfg.HTTPProxy != "http://http:3128" {
			t.Errorf("HTTPProxy = %q", cfg.HTTPProxy)
		}
		if cfg.HTTPSProxy != "http://chttps:3128" {
			t.Errorf("HTTPSProxy = %q", cfg.HTTPSProxy)
		}
		if cfg.NoProxy != ".svc" {
			t.Errorf("NoProxy = %q", cfg.NoProxy)
		}
	})

	t.Run("explicit values take precedence", func(t *testing.T) {
		cfg := &Config{HTTPProxy: "http://flag:3128"}
		applyProxyEnv(cfg, map[string]string{"HTTP_PROXY": "http://env:3128"})
		if cfg.HTTPProxy != "http://flag:3128" {
			t.Errorf("explicit HTTPProxy overwritten: %q", cfg.HTTPProxy)
		}
	})
}
