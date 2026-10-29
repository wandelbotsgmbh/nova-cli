package docker

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
)

// setGOOS pins the platform seen by podmanAuthFile for the duration of the test.
func setGOOS(t *testing.T, os string) {
	t.Helper()
	original := goos
	goos = os
	t.Cleanup(func() { goos = original })
}

// writeAuthFile writes a podman-style auth.json with a single registry entry
// and returns its path.
func writeAuthFile(t *testing.T, dir, registry, username, password string) string {
	t.Helper()
	auth := base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
	content := `{"auths":{"` + registry + `":{"auth":"` + auth + `"}}}`
	path := filepath.Join(dir, "auth.json")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func resolvePodman(t *testing.T, image string) authn.Authenticator {
	t.Helper()
	ref, err := name.ParseReference(image)
	if err != nil {
		t.Fatal(err)
	}
	auth, err := podmanKeychain{}.Resolve(ref.Context())
	if err != nil {
		t.Fatalf("podmanKeychain.Resolve() failed: %v", err)
	}
	return auth
}

func TestPodmanKeychainRegistryAuthFile(t *testing.T) {
	path := writeAuthFile(t, t.TempDir(), "myregistry.io", "alice", "secret")
	t.Setenv("REGISTRY_AUTH_FILE", path)
	t.Setenv("XDG_RUNTIME_DIR", "")

	auth := resolvePodman(t, "myregistry.io/some/app:latest")
	cfg, err := auth.Authorization()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Username != "alice" || cfg.Password != "secret" {
		t.Errorf("unexpected credentials: %s/%s", cfg.Username, cfg.Password)
	}
}

func TestPodmanKeychainXDGRuntimeDirOnLinux(t *testing.T) {
	setGOOS(t, "linux")
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "containers"), 0700); err != nil {
		t.Fatal(err)
	}
	writeAuthFile(t, filepath.Join(dir, "containers"), "myregistry.io", "bob", "hunter2")
	t.Setenv("REGISTRY_AUTH_FILE", "")
	t.Setenv("XDG_RUNTIME_DIR", dir)

	auth := resolvePodman(t, "myregistry.io/some/app:latest")
	cfg, err := auth.Authorization()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Username != "bob" || cfg.Password != "hunter2" {
		t.Errorf("unexpected credentials: %s/%s", cfg.Username, cfg.Password)
	}
}

func TestPodmanKeychainXDGRuntimeDirIgnoredOffLinux(t *testing.T) {
	setGOOS(t, "darwin")
	xdgDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(xdgDir, "containers"), 0700); err != nil {
		t.Fatal(err)
	}
	writeAuthFile(t, filepath.Join(xdgDir, "containers"), "myregistry.io", "bob", "hunter2")

	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".config", "containers"), 0700); err != nil {
		t.Fatal(err)
	}
	writeAuthFile(t, filepath.Join(home, ".config", "containers"), "myregistry.io", "carol", "letmein")

	t.Setenv("REGISTRY_AUTH_FILE", "")
	t.Setenv("XDG_RUNTIME_DIR", xdgDir)
	t.Setenv("HOME", home)

	auth := resolvePodman(t, "myregistry.io/some/app:latest")
	cfg, err := auth.Authorization()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Username != "carol" {
		t.Errorf("expected home config path to be used on non-linux, got user %q", cfg.Username)
	}
}

func TestPodmanKeychainRegistryAuthFileWinsOverXDG(t *testing.T) {
	setGOOS(t, "linux")
	regDir := t.TempDir()
	regPath := writeAuthFile(t, regDir, "myregistry.io", "alice", "secret")

	xdgDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(xdgDir, "containers"), 0700); err != nil {
		t.Fatal(err)
	}
	writeAuthFile(t, filepath.Join(xdgDir, "containers"), "myregistry.io", "bob", "hunter2")

	t.Setenv("REGISTRY_AUTH_FILE", regPath)
	t.Setenv("XDG_RUNTIME_DIR", xdgDir)

	auth := resolvePodman(t, "myregistry.io/some/app:latest")
	cfg, err := auth.Authorization()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Username != "alice" {
		t.Errorf("expected REGISTRY_AUTH_FILE to take precedence, got user %q", cfg.Username)
	}
}

func TestPodmanKeychainUnknownRegistryIsAnonymous(t *testing.T) {
	path := writeAuthFile(t, t.TempDir(), "myregistry.io", "alice", "secret")
	t.Setenv("REGISTRY_AUTH_FILE", path)
	t.Setenv("XDG_RUNTIME_DIR", "")

	auth := resolvePodman(t, "otherregistry.io/some/app:latest")
	if auth != authn.Anonymous {
		t.Errorf("expected Anonymous for unknown registry, got %v", auth)
	}
}

func TestPodmanKeychainNoAuthFileIsAnonymous(t *testing.T) {
	t.Setenv("REGISTRY_AUTH_FILE", "")
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	t.Setenv("HOME", t.TempDir())

	auth := resolvePodman(t, "myregistry.io/some/app:latest")
	if auth != authn.Anonymous {
		t.Errorf("expected Anonymous when no auth file exists, got %v", auth)
	}
}
