package generator

import (
	"embed"
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
		ProjectFs: nextJsAppProject,
		RootDir:   p.pluginFolder,
		OutPath:   outpath,
	}
}
