package docker

import (
	"context"
	"fmt"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
	"github.com/wandelbotsgmbh/nova-cli/pkg/httpx"
)

// azure needs this specific username for token auth
const azureUsername = "00000000-0000-0000-0000-000000000000"

// getRegistryCredentials gets the credentials for the registry of the given image
// from the given keychain and exchanges them for a scoped bearer token where supported.
// loginBin is the runtime CLI name used in error messages (e.g. "docker login ...").
//
// Note: For Docker Hub, raw credentials are returned because K8s kubelet performs its own token exchange.
// Pre-exchanged tokens don't work with Docker Hub's auth flow in K8s imagePullSecrets.
// For ACR with identity tokens, we exchange for a scoped bearer token.
func getRegistryCredentials(keychain authn.Keychain, imageName string, loginBin string) (ImageRegistryCredentials, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Parse the image reference
	ref, err := name.ParseReference(imageName)
	if err != nil {
		return ImageRegistryCredentials{}, fmt.Errorf("failed to parse image reference %q: %w", imageName, err)
	}

	repo := ref.Context()

	auth, err := keychain.Resolve(repo)
	if err != nil {
		return ImageRegistryCredentials{}, fmt.Errorf("failed to resolve auth for %s: %w", repo.RegistryStr(), err)
	}

	// Check if we have any credentials
	if auth == authn.Anonymous {
		return ImageRegistryCredentials{}, fmt.Errorf("no credentials found for registry %s. please login via '%s login %s'", repo.RegistryStr(), loginBin, repo.RegistryStr())
	}

	// Get the auth config to check what type of credentials we have
	authConfig, err := auth.Authorization()
	if err != nil {
		return ImageRegistryCredentials{}, fmt.Errorf("failed to get authorization: %w", err)
	}

	// If we already have a registry token, use it directly
	if authConfig.RegistryToken != "" {
		return ImageRegistryCredentials{
			Username: azureUsername,
			Password: authConfig.RegistryToken,
		}, nil
	}

	// For identity token auth (ACR with `az acr login`), exchange for scoped bearer token
	// This is required because identity tokens are refresh tokens, not directly usable
	if authConfig.IdentityToken != "" {
		challenge, err := transport.Ping(ctx, repo.Registry, httpx.Transport())
		if err != nil {
			return ImageRegistryCredentials{}, fmt.Errorf("failed to ping registry: %w", err)
		}

		scopes := []string{repo.Scope(transport.PullScope)}
		token, err := transport.Exchange(ctx, repo.Registry, auth, httpx.Transport(), scopes, challenge)
		if err != nil {
			return ImageRegistryCredentials{}, fmt.Errorf("failed to exchange token: %w", err)
		}

		accessToken := token.Token
		if accessToken == "" {
			accessToken = token.AccessToken
		}

		return ImageRegistryCredentials{
			Username: azureUsername,
			Password: accessToken,
		}, nil
	}

	// For username/password auth (Docker Hub, etc.), return raw credentials
	// K8s kubelet handles token exchange itself - pre-exchanged tokens don't work
	// because kubelet uses Basic Auth to fetch its own token
	//
	// TIP: Use a Personal Access Token (PAT) instead of your password for better security:
	// https://docs.docker.com/security/for-developers/access-tokens/
	if authConfig.Username != "" && authConfig.Password != "" {
		return ImageRegistryCredentials{
			Username: authConfig.Username,
			Password: authConfig.Password,
		}, nil
	}

	return ImageRegistryCredentials{}, fmt.Errorf("no valid credentials found for registry %s", repo.RegistryStr())
}
