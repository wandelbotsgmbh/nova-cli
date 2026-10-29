package bootstrap

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/wandelbotsgmbh/nova-cli/pkg/portal"
)

// WritePullSecrets retrieves image-registry credentials from the portal API
// and writes them as a k3s auto-deploy manifest containing Kubernetes
// dockerconfigjson Secrets with Reflector annotations.
func WritePullSecrets(ctx context.Context, cfg Config) error {
	env, err := loadEnvironment()
	if err != nil {
		return err
	}

	p, err := portal.NewPortal(env)
	if err != nil {
		return fmt.Errorf("create portal client: %w", err)
	}

	creds, err := p.ListRegistryCredentials()
	if err != nil {
		return fmt.Errorf("list registry credentials: %w", err)
	}

	if len(creds) == 0 {
		return fmt.Errorf("no registry credentials returned from portal API")
	}

	type pullSecret struct {
		Name    string
		Encoded string
	}
	var secrets []pullSecret
	for _, cred := range creds {
		if cred.Token == "" {
			continue
		}
		dockerCfg := buildDockerConfigJSON(cred.Url, cred.User, cred.Token)
		secrets = append(secrets, pullSecret{
			Name:    pullSecretName(cred.Url),
			Encoded: base64.StdEncoding.EncodeToString(dockerCfg),
		})
	}

	manifest, err := renderManifest("pull-secrets.yaml.tmpl", secrets)
	if err != nil {
		return err
	}

	return applyManifest("pull-secrets", manifest)
}

// buildDockerConfigJSON creates a serialised Docker config.json for a single
// registry credential.
func buildDockerConfigJSON(registryURL, user, token string) []byte {
	auth := base64.StdEncoding.EncodeToString([]byte(user + ":" + token))
	cfg := map[string]any{
		"auths": map[string]any{
			registryURL: map[string]any{
				"username": user,
				"password": token,
				"auth":     auth,
			},
		},
	}
	data, _ := json.Marshal(cfg)
	return data
}

// pullSecretName converts a registry URL to a k8s-safe secret name.
// e.g. "wandelbots.azurecr.io" → "pull-secret-wandelbots-azurecr-io"
func pullSecretName(registryURL string) string {
	name := strings.TrimPrefix(registryURL, "https://")
	name = strings.TrimPrefix(name, "http://")
	name = strings.TrimSuffix(name, "/")
	replacer := strings.NewReplacer(".", "-", "/", "-")
	name = replacer.Replace(name)
	return "pull-secret-" + name
}
