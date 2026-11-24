package generator

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

//go:embed all:NextJsApp/*
var nextJsAppProject embed.FS

type NextJsAppGenerator struct {
	pluginFolder          string
	generatorId           string
	generatorReadableName string
	generatorLanguage     string
}

func NewNextJsAppGenerator() NextJsAppGenerator {
	return NextJsAppGenerator{
		pluginFolder:          "NextJsApp",
		generatorId:           "nextjs_app",
		generatorReadableName: "NextJs App",
		generatorLanguage:     "nextjs",
	}
}

func (p NextJsAppGenerator) CreateProjectZip(outpath string, project PluginSpec) (string, error) {
	settings := p.createSettingsForNextJsApp(outpath)
	return generateProjectZip(project, settings)
}

// writes the project to the given outpath
func (p NextJsAppGenerator) CreateProject(outpath string, project PluginSpec) error {
	settings := p.createSettingsForNextJsApp(outpath)
	return createProject(project, settings)
}

// returns the generator id
func (p NextJsAppGenerator) GeneratorId() string { return p.generatorId }

// returns a readbale name for the generator
func (p NextJsAppGenerator) GeneratorReadableName() string { return p.generatorReadableName }

func (p NextJsAppGenerator) GeneratorLanguage() string { return p.generatorLanguage }

func (p NextJsAppGenerator) createSettingsForNextJsApp(outpath string) GeneratorSettings {
	return GeneratorSettings{
		ProjectFs:   nextJsAppProject,
		RootDir:     p.pluginFolder,
		OutPath:     outpath,
		PostProcess: renameNpmPackage,
	}
}

// nextJsAppPackagePlaceholder is the package name in NextJsApp/package.json.
//
// package.json is deliberately not a .tpl template: Dependabot and pnpm need a
// valid package.json in this repo (a .tpl was never rendered, generated apps
// were named "{{ .Name }}"). The placeholder is replaced with the app name
// after generation; pnpm-lock.yaml doesn't contain the root package name.
const nextJsAppPackagePlaceholder = "nova-nextjs-app"

// renameNpmPackage replaces the placeholder package name in the generated
// package.json with the app name.
func renameNpmPackage(outPath string, project PluginSpec) error {
	if project.PluginName == "" {
		return nil // keep the placeholder, it's a valid package name
	}
	path := filepath.Join(outPath, "package.json")
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	placeholder := fmt.Sprintf("%q: %q", "name", nextJsAppPackagePlaceholder)
	if n := strings.Count(string(content), placeholder); n != 1 {
		return fmt.Errorf("package.json: expected one package name placeholder %q, found %d", nextJsAppPackagePlaceholder, n)
	}
	replacement := fmt.Sprintf("%q: %q", "name", project.PluginName)
	return os.WriteFile(path, []byte(strings.Replace(string(content), placeholder, replacement, 1)), 0o644)
}
