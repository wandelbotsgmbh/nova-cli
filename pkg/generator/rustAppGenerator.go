package generator

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

//go:embed all:RustApp/*
var rustAppProject embed.FS

type RustAppGenerator struct {
	pluginFolder          string
	generatorId           string
	generatorReadableName string
	generatorLanguage     string
}

func NewRustAppGenerator() RustAppGenerator {
	return RustAppGenerator{
		pluginFolder:          "RustApp",
		generatorId:           "rust_app",
		generatorReadableName: "Rust App",
		generatorLanguage:     "rust",
	}
}

func (p RustAppGenerator) CreateProjectZip(outpath string, project PluginSpec) (string, error) {
	settings := p.createSettingsForRustApp(outpath)
	return generateProjectZip(project, settings)
}

// writes the project to the given outpath
func (p RustAppGenerator) CreateProject(outpath string, project PluginSpec) error {
	settings := p.createSettingsForRustApp(outpath)
	return createProject(project, settings)
}

// returns the generator id
func (p RustAppGenerator) GeneratorId() string { return p.generatorId }

// returns a readbale name for the generator
func (p RustAppGenerator) GeneratorReadableName() string { return p.generatorReadableName }

func (p RustAppGenerator) GeneratorLanguage() string { return p.generatorLanguage }

func (p RustAppGenerator) createSettingsForRustApp(outpath string) GeneratorSettings {
	return GeneratorSettings{
		ProjectFs:   rustAppProject,
		RootDir:     p.pluginFolder,
		OutPath:     outpath,
		PostProcess: renameCargoPackage,
	}
}

// rustAppPackagePlaceholder is the package name in RustApp/Cargo.toml and
// RustApp/Cargo.lock.
//
// The two files are deliberately not .tpl templates: they must stay a valid
// cargo project in this repo, since CodeQL's Rust analysis (and any other
// cargo-based tooling) runs cargo on them and fails on the template syntax
// ("{{ .PluginName }}" is not a valid package name). The placeholder is
// replaced with the plugin name after generation instead.
//
// Keep it in sync in both files: the Dockerfile builds with --locked, which
// fails when Cargo.lock's root package name doesn't match Cargo.toml.
const rustAppPackagePlaceholder = "nova-rust-app"

// renameCargoPackage replaces the placeholder package name in the generated
// Cargo.toml and Cargo.lock with the plugin name.
func renameCargoPackage(outPath string, project PluginSpec) error {
	if project.PluginName == "" {
		return nil // keep the placeholder, it's a valid package name
	}
	placeholder := fmt.Sprintf("name = %q", rustAppPackagePlaceholder)
	replacement := fmt.Sprintf("name = %q", project.PluginName)
	for _, file := range []string{"Cargo.toml", "Cargo.lock"} {
		path := filepath.Join(outPath, file)
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		// exactly one occurrence: [package] in Cargo.toml, the root package in Cargo.lock
		if n := strings.Count(string(content), placeholder); n != 1 {
			return fmt.Errorf("%s: expected one package placeholder %q, found %d", file, rustAppPackagePlaceholder, n)
		}
		content = []byte(strings.Replace(string(content), placeholder, replacement, 1))
		if err := os.WriteFile(path, content, 0o644); err != nil {
			return err
		}
	}
	return nil
}
