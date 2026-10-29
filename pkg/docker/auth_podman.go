package docker

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"

	"github.com/docker/cli/cli/config"
	"github.com/docker/cli/cli/config/types"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
)

// podmanKeychain resolves credentials from podman's auth.json, which uses the
// same format as the auths section of a docker config.json.
type podmanKeychain struct{}

// test seam
var goos = runtime.GOOS

// podmanAuthFile returns the path of podman's auth file, honoring the same
// precedence podman itself uses, or "" if none exists.
func podmanAuthFile() string {
	if path := os.Getenv("REGISTRY_AUTH_FILE"); path != "" && fileExists(path) {
		return path
	}
	// podman only uses the runtime-dir locations on Linux
	if goos == "linux" {
		if dir := os.Getenv("XDG_RUNTIME_DIR"); dir != "" {
			if path := filepath.Join(dir, "containers", "auth.json"); fileExists(path) {
				return path
			}
		} else {
			// rootful podman without XDG_RUNTIME_DIR
			if path := filepath.Join("/run", "containers", strconv.Itoa(os.Getuid()), "auth.json"); fileExists(path) {
				return path
			}
		}
	}
	// default location on macOS/Windows, fallback on Linux
	if home, err := os.UserHomeDir(); err == nil {
		if path := filepath.Join(home, ".config", "containers", "auth.json"); fileExists(path) {
			return path
		}
	}
	return ""
}

func (podmanKeychain) Resolve(target authn.Resource) (authn.Authenticator, error) {
	path := podmanAuthFile()
	if path == "" {
		return authn.Anonymous, nil
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	cf, err := config.LoadFromReader(f)
	if err != nil {
		return nil, fmt.Errorf("failed to parse %s: %w", path, err)
	}

	// same lookup as go-containerregistry's defaultKeychain
	var cfg, empty types.AuthConfig
	for _, key := range []string{
		target.String(),
		target.RegistryStr(),
	} {
		if key == name.DefaultRegistry {
			key = authn.DefaultAuthKey
		}

		cfg, err = cf.GetAuthConfig(key)
		if err != nil {
			return nil, err
		}
		// GetAuthConfig sets ServerAddress; clear it for a proper is-empty test
		cfg.ServerAddress = ""
		if cfg != empty {
			break
		}
	}
	if cfg == empty {
		return authn.Anonymous, nil
	}

	return authn.FromConfig(authn.AuthConfig{
		Username:      cfg.Username,
		Password:      cfg.Password,
		Auth:          cfg.Auth,
		IdentityToken: cfg.IdentityToken,
		RegistryToken: cfg.RegistryToken,
	}), nil
}

// fileExists returns true if the given path exists and is not a directory.
func fileExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir()
}
