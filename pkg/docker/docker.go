package docker

import (
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type ImageRegistryCredentials struct {
	Username string
	Password string
}

// Runtime abstracts a container runtime CLI (docker or podman) for building
// and pushing images and resolving registry credentials.
type Runtime interface {
	// Name returns the CLI binary name of the runtime ("docker" or "podman").
	Name() string
	// BuildImage finds the dockerfile in the given path and builds the image for linux/amd64.
	// Provide the image in format e.g. registry.example.com/team/servicename (one could use CreateImageName() method)
	BuildImage(pathToDockerFile string, image string) error
	// BuildImageOCI builds the image for linux/amd64 and writes it as an OCI
	// layout directory at ociDir (with per-layer blobs under blobs/sha256), used
	// by the --copy-image path to transfer only the layers a target is missing.
	BuildImageOCI(pathToDockerFile string, image string, ociDir string) error
	// PushImage pushes the image to its registry.
	// Returns the image digest sha256:<hash>
	PushImage(image string) (string, error)
	// GetRegistryCredentials gets the credentials for the registry of the given image
	// from the runtime's credential store.
	GetRegistryCredentials(imageName string) (ImageRegistryCredentials, error)
	// BuildAndPushMultiPlatform builds a multi-platform (linux/amd64, linux/arm64)
	// image and pushes it to its registry.
	BuildAndPushMultiPlatform(dockerfilePath, contextPath, imageTag string) error
}

const (
	RuntimeNameDocker = "docker"
	RuntimeNamePodman = "podman"
)

// test seam
var lookPath = exec.LookPath

// Detect returns the container runtime to use.
// override comes from the config key "container-runtime" ("" = auto-detect,
// preferring docker over podman).
func Detect(override string) (Runtime, error) {
	switch override {
	case RuntimeNameDocker, RuntimeNamePodman:
		if _, err := lookPath(override); err != nil {
			return nil, fmt.Errorf("configured container-runtime %q is not installed or not on PATH", override)
		}
		return newRuntime(override), nil
	case "":
		for _, name := range []string{RuntimeNameDocker, RuntimeNamePodman} {
			if _, err := lookPath(name); err == nil {
				return newRuntime(name), nil
			}
		}
		return nil, errors.New("neither docker nor podman is installed. Install one of them, or set a runtime explicitly via 'nova config set container-runtime <docker|podman>'")
	default:
		return nil, fmt.Errorf("invalid container-runtime %q: must be \"docker\" or \"podman\"", override)
	}
}

func newRuntime(name string) Runtime {
	var rt Runtime
	if name == RuntimeNamePodman {
		rt = podmanRuntime{}
	} else {
		rt = dockerRuntime{}
	}
	fmt.Printf("Using container runtime: %s\n", rt.Name())
	if name == RuntimeNamePodman {
		warnIfUnsupportedPodmanVersion()
	}
	return rt
}

// create image name with a unique local tag (e.g. registry.example.com/team/servicename:local-1757000000)
// for images that are imported directly into the instance's containerd instead of being pushed.
// The unique tag ensures the deployment spec changes on every install, so the pod is
// rolled onto the newly imported image (apps run with imagePullPolicy IfNotPresent).
func CreateLocalImageName(registry string, imageName string, now time.Time) string {
	return CreateImageName(registry, imageName) + ":local-" + strconv.FormatInt(now.Unix(), 10)
}

// create image name in format registry/imageName:latest (e.g. registry.example.com/team/servicename:latest)
func CreateImageName(registry string, imageName string) string {
	imageName = normalizeImageName(imageName)
	registry, _ = strings.CutSuffix(registry, "/")
	registry, _ = strings.CutPrefix(registry, "https://")
	registry, _ = strings.CutPrefix(registry, "http://")
	return registry + "/" + imageName
}

// The name component of the docker image must be lowercase and can include digits,
// underscores (`_`), periods ([`.`]), and dashes (`-`).
func normalizeImageName(image string) string {
	image = strings.TrimSpace(image)
	image = strings.ToLower(image)

	// Define a regular expression that matches characters NOT allowed in Docker image names
	// Allowed characters are lowercase letters, digits, underscore, period, and dash
	notAllowedCharsRegex := regexp.MustCompile(`[^a-z0-9_.-]+`)

	// Replace each sequence of not allowed characters with an underscore
	normalizedImage := notAllowedCharsRegex.ReplaceAllStringFunc(image, func(s string) string {
		return "_"
	})

	if strings.HasPrefix(normalizedImage, "_") {
		normalizedImage = "nova" + normalizedImage
	}

	return normalizedImage
}
