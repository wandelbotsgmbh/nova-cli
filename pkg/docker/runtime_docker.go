package docker

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"github.com/google/go-containerregistry/pkg/authn"
)

const buildxBuilderName = "nova-model-builder"

// dockerRuntime implements Runtime using the docker CLI.
type dockerRuntime struct{}

func (dockerRuntime) Name() string {
	return RuntimeNameDocker
}

func (dockerRuntime) BuildImage(pathToDockerFile string, image string) error {
	// TODO for now we assume our nova platform is linux/amd64
	// --load is needed for non-default builders (e.g. docker-container driver),
	// otherwise the image only stays in the build cache and the push fails
	cmd := exec.Command("docker", "buildx", "build", "--load", "-t", image, "--platform", "linux/amd64", pathToDockerFile)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (dockerRuntime) BuildImageOCI(pathToDockerFile string, image string, ociDir string) error {
	// buildx writes a canonical OCI layout to ociDir and does not load the image
	// into the local image store (not needed for copy-image)
	output := fmt.Sprintf("type=oci,dest=%s,tar=false,name=%s", ociDir, image)
	cmd := exec.Command("docker", "buildx", "build", "--platform", "linux/amd64", "--output", output, pathToDockerFile)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (dockerRuntime) PushImage(image string) (string, error) {
	cmd := exec.Command("docker", "push", image)

	var buf bytes.Buffer
	writer := io.MultiWriter(os.Stdout, &buf)

	cmd.Stdout = writer
	cmd.Stderr = writer
	err := cmd.Run()
	if err != nil {
		return "", err
	}

	val := buf.String()
	return getImageDigest(val)
}

func (r dockerRuntime) GetRegistryCredentials(imageName string) (ImageRegistryCredentials, error) {
	return getRegistryCredentials(authn.DefaultKeychain, imageName, r.Name())
}

func (dockerRuntime) BuildAndPushMultiPlatform(dockerfilePath, contextPath, imageTag string) error {
	// Ensure a buildx builder exists that supports multi-platform builds
	if err := ensureBuildxBuilder(); err != nil {
		return err
	}

	cmd := exec.Command("docker", "buildx", "build",
		"--builder", buildxBuilderName,
		"--platform", "linux/amd64,linux/arm64",
		"-f", dockerfilePath,
		"-t", imageTag,
		"--push",
		contextPath,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to build and push image: %w", err)
	}
	return nil
}

// ensureBuildxBuilder creates a docker-container buildx builder if it doesn't exist.
func ensureBuildxBuilder() error {
	// Check if our builder already exists
	cmd := exec.Command("docker", "buildx", "inspect", buildxBuilderName)
	if err := cmd.Run(); err == nil {
		return nil // builder exists
	}

	// Create a new builder with docker-container driver (supports multi-platform)
	fmt.Printf("Creating buildx builder '%s' for multi-platform builds...\n", buildxBuilderName)
	createCmd := exec.Command("docker", "buildx", "create",
		"--name", buildxBuilderName,
		"--driver", "docker-container",
		"--bootstrap",
	)
	createCmd.Stdout = os.Stdout
	createCmd.Stderr = os.Stderr
	if err := createCmd.Run(); err != nil {
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
