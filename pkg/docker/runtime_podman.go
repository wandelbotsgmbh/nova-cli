package docker

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/google/go-containerregistry/pkg/authn"
)

// nova is tested against podman >= 5.4 (the version shipped by Debian stable
// and covered by the test-podman CI job). Older versions mostly work but have
// known issues, e.g. multi-platform builds silently collapsing to a single
// architecture due to platform-agnostic build caching.
const minPodmanMajor, minPodmanMinor = 5, 4

// podmanRuntime implements Runtime using the podman CLI.
type podmanRuntime struct{}

func warnIfUnsupportedPodmanVersion() {
	out, err := exec.Command("podman", "version", "--format", "{{.Client.Version}}").Output()
	if err != nil {
		return
	}
	version := strings.TrimSpace(string(out))
	if !isSupportedPodmanVersion(version) {
		fmt.Printf("Warning: podman %s detected, nova is tested with podman >= %d.%d - older versions may misbehave\n", version, minPodmanMajor, minPodmanMinor)
	}
}

// isSupportedPodmanVersion reports whether the given version is at least the
// minimum tested one. Unparseable versions are treated as supported to avoid
// false warnings.
func isSupportedPodmanVersion(version string) bool {
	parts := strings.SplitN(version, ".", 3)
	if len(parts) < 2 {
		return true
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return true
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		return true
	}
	return major > minPodmanMajor || (major == minPodmanMajor && minor >= minPodmanMinor)
}

func (podmanRuntime) Name() string {
	return RuntimeNamePodman
}

func (podmanRuntime) BuildImage(pathToDockerFile string, image string) error {
	cmd := exec.Command("podman", "build", "--platform", "linux/amd64", "-t", image, pathToDockerFile)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("podman build failed: %w (on macOS/Windows, ensure the podman machine is running: 'podman machine start')", err)
	}
	return nil
}

func (podmanRuntime) BuildImageOCI(pathToDockerFile string, image string, ociDir string) error {
	// build into podman's local storage, then export it as an OCI layout dir
	build := exec.Command("podman", "build", "--platform", "linux/amd64", "-t", image, pathToDockerFile)
	build.Stdout = os.Stdout
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		return fmt.Errorf("podman build failed: %w (on macOS/Windows, ensure the podman machine is running: 'podman machine start')", err)
	}

	save := exec.Command("podman", "save", "--format", "oci-dir", "-o", ociDir, image)
	save.Stdout = os.Stdout
	save.Stderr = os.Stderr
	if err := save.Run(); err != nil {
		return fmt.Errorf("podman save (oci-dir) failed: %w", err)
	}
	return nil
}

// PushImage pushes via podman. Unlike docker, podman does not print a
// "digest: sha256:..." line, so the digest is captured with --digestfile.
func (podmanRuntime) PushImage(image string) (string, error) {
	digestFile, err := os.CreateTemp("", "nova-push-digest-*")
	if err != nil {
		return "", err
	}
	digestFile.Close()
	defer os.Remove(digestFile.Name())

	cmd := exec.Command("podman", "push", "--digestfile", digestFile.Name(), image)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("podman push failed: %w (on macOS/Windows, ensure the podman machine is running: 'podman machine start')", err)
	}

	content, err := os.ReadFile(digestFile.Name())
	if err != nil {
		return "", err
	}
	return parseDigestFile(string(content))
}

// GetRegistryCredentials consults podman's auth.json first, with the docker
// config as fallback, because authn.DefaultKeychain only falls back to
// podman's auth file when ~/.docker/config.json is absent and never checks
// the ~/.config/containers location podman uses on macOS/Windows.
func (r podmanRuntime) GetRegistryCredentials(imageName string) (ImageRegistryCredentials, error) {
	keychain := authn.NewMultiKeychain(podmanKeychain{}, authn.DefaultKeychain)
	return getRegistryCredentials(keychain, imageName, r.Name())
}

// BuildAndPushMultiPlatform builds a multi-platform manifest list and pushes it.
func (podmanRuntime) BuildAndPushMultiPlatform(dockerfilePath, contextPath, imageTag string) error {
	// podman build --manifest appends to an existing manifest list, so remove
	// any leftover from a previous build to avoid duplicate entries
	rmCmd := exec.Command("podman", "manifest", "rm", imageTag)
	_ = rmCmd.Run()

	// --no-cache: older podman versions (e.g. 5.4) don't include the platform
	// in the build cache key, so cached layers from one platform leg get
	// reused for the other and the manifest list collapses to a single entry
	buildCmd := exec.Command("podman", "build",
		"--no-cache",
		"--platform", "linux/amd64,linux/arm64",
		"--manifest", imageTag,
		"-f", dockerfilePath,
		contextPath,
	)
	buildCmd.Stdout = os.Stdout
	buildCmd.Stderr = os.Stderr
	if err := buildCmd.Run(); err != nil {
		return fmt.Errorf("failed to build image: %w (multi-platform builds require podman >= %d.%d; on macOS/Windows ensure the podman machine is running: 'podman machine start')", err, minPodmanMajor, minPodmanMinor)
	}

	pushCmd := exec.Command("podman", "manifest", "push", "--all", imageTag, "docker://"+imageTag)
	pushCmd.Stdout = os.Stdout
	pushCmd.Stderr = os.Stderr
	if err := pushCmd.Run(); err != nil {
		return fmt.Errorf("failed to push image: %w", err)
	}
	return nil
}

func parseDigestFile(content string) (string, error) {
	digest := strings.TrimSpace(content)
	if !strings.HasPrefix(digest, "sha256:") {
		return "", errors.New("no image digest found in podman digest file")
	}
	return digest, nil
}
