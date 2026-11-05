package docker

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/wandelbotsgmbh/nova-cli/pkg/debug"
	"github.com/wandelbotsgmbh/nova-cli/pkg/execx"

	"github.com/google/go-containerregistry/pkg/authn"
)

const buildxBuilderName = "nova-model-builder"

// dockerRuntime implements Runtime using the docker CLI.
type dockerRuntime struct{}

func (dockerRuntime) Name() string {
	return RuntimeNameDocker
}

// errBuildxMissing is returned when the docker buildx plugin is not installed.
var errBuildxMissing = errors.New("the docker buildx plugin is not installed (install docker-buildx-plugin or Docker Desktop; check with 'docker buildx version')")

// requireBuildx fails early if the docker buildx plugin is missing. Otherwise
// "docker buildx build -t ..." fails with the misleading
// "unknown shorthand flag: 't' in -t" from the docker root command (issue #46).
func requireBuildx() error {
	if err := debug.Command("docker", "buildx", "version").Run(); err != nil {
		return errBuildxMissing
	}
	return nil
}

func (dockerRuntime) BuildImage(pathToDockerFile string, image string, platform string) error {
	if err := requireBuildx(); err != nil {
		return err
	}
	// --load is needed for non-default builders (e.g. docker-container driver),
	// otherwise the image only stays in the build cache and the push fails
	cmd := debug.Command("docker", "buildx", "build", "--load", "-t", image, "--platform", platform, pathToDockerFile)
	if err := execx.Run(cmd); err != nil {
		return fmt.Errorf("docker build failed: %w", err)
	}
	return nil
}

func (dockerRuntime) BuildImageOCI(pathToDockerFile string, image string, platform string, ociDir string) error {
	// buildx writes a canonical OCI layout to ociDir and does not load the image
	// into the local image store (not needed for copy-image)
	if err := requireBuildx(); err != nil {
		return err
	}
	output := fmt.Sprintf("type=oci,dest=%s,tar=false,name=%s", ociDir, image)
	cmd := debug.Command("docker", "buildx", "build", "--platform", platform, "--output", output, pathToDockerFile)
	if err := execx.Run(cmd); err != nil {
		return fmt.Errorf("docker build failed: %w", err)
	}
	return nil
}

func (dockerRuntime) PushImage(image string) (string, error) {
	cmd := debug.Command("docker", "push", image)

	// the digest is only printed, so the output is parsed as well
	var buf bytes.Buffer
	if err := execx.Run(cmd, &buf); err != nil {
		return "", fmt.Errorf("docker push failed: %w", err)
	}

	val := buf.String()
	return getImageDigest(val)
}

func (r dockerRuntime) GetRegistryCredentials(imageName string) (ImageRegistryCredentials, error) {
	return getRegistryCredentials(authn.DefaultKeychain, imageName, r.Name())
}

func (dockerRuntime) BuildAndPushMultiPlatform(dockerfilePath, contextPath, imageTag string) error {
	if err := requireBuildx(); err != nil {
		return err
	}

	// Ensure a buildx builder exists that supports multi-platform builds
	if err := ensureBuildxBuilder(); err != nil {
		return err
	}

	cmd := debug.Command("docker", "buildx", "build",
		"--builder", buildxBuilderName,
		"--platform", "linux/amd64,linux/arm64",
		"-f", dockerfilePath,
		"-t", imageTag,
		"--push",
		contextPath,
	)
	if err := execx.Run(cmd); err != nil {
		return fmt.Errorf("failed to build and push image: %w", err)
	}
	return nil
}

// ensureBuildxBuilder creates a docker-container buildx builder if it doesn't exist.
func ensureBuildxBuilder() error {
	// Check if our builder already exists
	cmd := debug.Command("docker", "buildx", "inspect", buildxBuilderName)
	if err := cmd.Run(); err == nil {
		return nil // builder exists
	}

	// Create a new builder with docker-container driver (supports multi-platform)
	fmt.Fprintf(os.Stderr, "Creating buildx builder '%s' for multi-platform builds...\n", buildxBuilderName)
	createCmd := debug.Command("docker", "buildx", "create",
		"--name", buildxBuilderName,
		"--driver", "docker-container",
		"--bootstrap",
	)
	if err := execx.Run(createCmd); err != nil {
		return fmt.Errorf("failed to create buildx builder: %w", err)
	}

	return nil
}

// Extracts the image digest from the push output.
// Returns it in format sha256:<hash>
func getImageDigest(out string) (string, error) {
	pattern := `digest:\s*(sha256:[a-f0-9]{64})`
	re := regexp.MustCompile(pattern)
	matches := re.FindStringSubmatch(out)

	if len(matches) <= 1 {
		return "", errors.New("no docker image digest found")
	}

	match := strings.TrimSpace(matches[1])

	if match != "" {
		return match, nil
	}
	return "", errors.New("no docker image digest found")
}
