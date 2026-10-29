package modelpack

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/modelpack/modctl/pkg/modelprovider"
	"github.com/wandelbotsgmbh/nova-cli/pkg/docker"
	"gopkg.in/yaml.v3"
)

// ModelMeta is the metadata file written during init.
type ModelMeta struct {
	Name     string `yaml:"name"`
	Version  string `yaml:"version"`
	Source   string `yaml:"source"`
	Provider string `yaml:"provider,omitempty"`
}

const (
	MetaFileName   = "model.yaml"
	DockerfileName = "Dockerfile"
)

// InitOptions configures the Init operation.
type InitOptions struct {
	Source    string
	Name      string
	Version   string
	Provider  string
	OutputDir string
}

// Init downloads the model into the output directory and creates a Dockerfile + model.yaml metadata file.
func Init(opts InitOptions) error {
	if opts.Source == "" {
		return fmt.Errorf("model source is required")
	}

	if opts.OutputDir == "" {
		opts.OutputDir = "."
	}

	name := opts.Name
	if name == "" {
		name = deriveModelName(opts.Source)
	}

	version := opts.Version
	if version == "" {
		version = "1.0.0"
	}

	// Create output directory
	if err := os.MkdirAll(opts.OutputDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	// Resolve model files: download from provider or use local source
	modelFilesDir, err := resolveSource(opts.Source, opts.Provider, opts.OutputDir)
	if err != nil {
		return err
	}

	// Canonicalize paths to get correct case on case-insensitive filesystems
	absOutputDir, err := realPath(opts.OutputDir)
	if err != nil {
		absOutputDir = opts.OutputDir
	}
	absModelFilesDir, err := realPath(modelFilesDir)
	if err != nil {
		absModelFilesDir = modelFilesDir
	}

	// Get the model subdirectory name relative to the output dir
	modelRelPath, err := filepath.Rel(absOutputDir, absModelFilesDir)
	if err != nil {
		return fmt.Errorf("failed to compute relative path: %w", err)
	}

	// Generate Dockerfile
	dockerfile := generateDockerfile(modelRelPath)

	dockerfilePath := filepath.Join(opts.OutputDir, DockerfileName)
	if err := os.WriteFile(dockerfilePath, []byte(dockerfile), 0644); err != nil {
		return fmt.Errorf("failed to write Dockerfile: %w", err)
	}

	// Generate .dockerignore to exclude HF cache and hidden files
	dockerignore := generateDockerignore(modelRelPath)
	dockerignorePath := filepath.Join(opts.OutputDir, ".dockerignore")
	if err := os.WriteFile(dockerignorePath, []byte(dockerignore), 0644); err != nil {
		return fmt.Errorf("failed to write .dockerignore: %w", err)
	}

	// Write metadata in output-dir
	meta := &ModelMeta{
		Name:     name,
		Version:  version,
		Source:   opts.Source,
		Provider: opts.Provider,
	}

	metaPath := filepath.Join(opts.OutputDir, MetaFileName)
	data, err := yaml.Marshal(meta)
	if err != nil {
		return err
	}
	if err := os.WriteFile(metaPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write %s: %w", MetaFileName, err)
	}

	fmt.Printf("Model initialized in %s\n", opts.OutputDir)
	fmt.Printf("  Metadata:   %s\n", metaPath)
	fmt.Printf("  Dockerfile: %s\n", dockerfilePath)
	fmt.Printf("  Name:       %s\n", name)
	fmt.Printf("  Version:    %s\n", version)
	return nil
}

// BuildAndPush builds a multi-platform scratch image using the Dockerfile
// in the given directory, and pushes it with the specified image tag.
func BuildAndPush(projectPath, imageTag string, rt docker.Runtime) error {
	// Verify Dockerfile exists
	dockerfilePath := filepath.Join(projectPath, DockerfileName)
	if _, err := os.Stat(dockerfilePath); os.IsNotExist(err) {
		return fmt.Errorf("no %s found in %s — run 'nova model init' first", DockerfileName, projectPath)
	}

	fmt.Printf("Building and pushing model image: %s\n", imageTag)

	// The generated Dockerfile is FROM scratch + COPY only, so the arm64 leg
	// needs no qemu/binfmt emulation.
	if err := rt.BuildAndPushMultiPlatform(dockerfilePath, projectPath, imageTag); err != nil {
		return fmt.Errorf("failed to build and push model image: %w", err)
	}

	fmt.Printf("\nModel image pushed successfully: %s\n", imageTag)
	return nil
}

// ReadMeta reads the model.yaml metadata from the given directory.
func ReadMeta(path string) (*ModelMeta, error) {
	metaPath := filepath.Join(path, MetaFileName)
	info, err := os.Stat(metaPath)
	if os.IsNotExist(err) || (info != nil && info.IsDir()) {
		return nil, fmt.Errorf("no %s found in %s — run 'nova model init' first", MetaFileName, path)
	}

	data, err := os.ReadFile(metaPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", MetaFileName, err)
	}

	meta := &ModelMeta{}
	if err := yaml.Unmarshal(data, meta); err != nil {
		return nil, fmt.Errorf("failed to parse %s: %w", MetaFileName, err)
	}

	return meta, nil
}

// resolveSource downloads model files from a provider or verifies a local source exists.
func resolveSource(source, providerName, outputDir string) (string, error) {
	// Check if source is a local directory
	if info, err := os.Stat(source); err == nil && info.IsDir() {
		absSource, _ := filepath.Abs(source)
		absOutput, _ := filepath.Abs(outputDir)
		if absSource == absOutput {
			return outputDir, nil
		}
		if err := copyDir(source, outputDir); err != nil {
			return "", fmt.Errorf("failed to copy model files: %w", err)
		}
		return outputDir, nil
	}

	// Remote source — download via provider
	provider, err := getProvider(source, providerName)
	if err != nil {
		return "", fmt.Errorf("source '%s' is not a local directory and no provider could be determined: %w", source, err)
	}

	fmt.Printf("Downloading model from %s...\n", provider.Name())
	path, err := provider.DownloadModel(context.Background(), source, outputDir)
	if err != nil {
		return "", fmt.Errorf("failed to download model: %w", err)
	}

	fmt.Printf("Model downloaded to: %s\n", path)
	return path, nil
}

// getProvider returns the appropriate provider for the given source.
func getProvider(source, providerName string) (modelprovider.Provider, error) {
	registry := modelprovider.GetRegistry()
	return registry.SelectProvider(source, providerName)
}

// deriveModelName extracts a model name from a source URL/path.
func deriveModelName(source string) string {
	source = strings.TrimSuffix(source, "/")

	if strings.Contains(source, "/") {
		parts := strings.Split(source, "/")
		name := parts[len(parts)-1]
		if name != "" {
			return strings.ToLower(name)
		}
	}

	return strings.ToLower(source)
}

// generateDockerfile generates a FROM scratch Dockerfile that copies the model files directory.
func generateDockerfile(modelFilesRelPath string) string {
	return fmt.Sprintf("FROM scratch\nCOPY %s /model\n", modelFilesRelPath)
}

// generateDockerignore creates a .dockerignore to exclude unwanted files from the build context.
func generateDockerignore(modelRelPath string) string {
	return fmt.Sprintf(`# HuggingFace cache metadata
%s/.cache
%s/huggingface

# Hidden files
.*
!.dockerignore
`, modelRelPath, modelRelPath)
}

// realPath resolves a path to its true filesystem casing by walking each component
// and matching against actual directory entries. This is needed on case-insensitive
// filesystems (macOS) where the path "Foo" might actually be stored as "foo".
func realPath(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return p, err
	}

	parts := strings.Split(abs, string(filepath.Separator))
	resolved := string(filepath.Separator)

	for _, part := range parts[1:] { // skip empty first element from leading /
		entries, err := os.ReadDir(resolved)
		if err != nil {
			return abs, err
		}

		found := false
		for _, entry := range entries {
			if strings.EqualFold(entry.Name(), part) {
				resolved = filepath.Join(resolved, entry.Name())
				found = true
				break
			}
		}
		if !found {
			// Fall back to the original component
			resolved = filepath.Join(resolved, part)
		}
	}

	return resolved, nil
}

// copyDir copies all files from src to dst recursively.
func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}

		dstPath := filepath.Join(dst, relPath)

		if info.IsDir() {
			return os.MkdirAll(dstPath, 0755)
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(dstPath, data, info.Mode())
	})
}
