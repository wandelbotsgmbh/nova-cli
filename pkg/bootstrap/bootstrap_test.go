package bootstrap

import (
	"encoding/base64"
	"encoding/json"
	"testing"
)

func TestPullSecretName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"wandelbots.azurecr.io", "pull-secret-wandelbots-azurecr-io"},
		{"https://wandelbots.azurecr.io", "pull-secret-wandelbots-azurecr-io"},
		{"https://registry-1.docker.io/", "pull-secret-registry-1-docker-io"},
		{"my.registry.io/path", "pull-secret-my-registry-io-path"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := pullSecretName(tt.input)
			if got != tt.want {
				t.Errorf("pullSecretName(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestBuildDockerConfigJSON(t *testing.T) {
	data := buildDockerConfigJSON("wandelbots.azurecr.io", "user1", "token1")

	var cfg struct {
		Auths map[string]struct {
			Username string `json:"username"`
			Password string `json:"password"`
			Auth     string `json:"auth"`
		} `json:"auths"`
	}

	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	entry, ok := cfg.Auths["wandelbots.azurecr.io"]
	if !ok {
		t.Fatal("expected registry key in auths")
	}

	if entry.Username != "user1" {
		t.Errorf("username = %q, want %q", entry.Username, "user1")
	}
	if entry.Password != "token1" {
		t.Errorf("password = %q, want %q", entry.Password, "token1")
	}

	wantAuth := base64.StdEncoding.EncodeToString([]byte("user1:token1"))
	if entry.Auth != wantAuth {
		t.Errorf("auth = %q, want %q", entry.Auth, wantAuth)
	}
}
