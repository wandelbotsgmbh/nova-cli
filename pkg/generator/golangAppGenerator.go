package generator

import "embed"

//go:embed all:GolangApp/*
var golangAppProject embed.FS

type GolangAppGenerator struct {
	pluginFolder          string
	generatorId           string
	generatorReadableName string
	generatorLanguage     string
}

func NewGolangAppGenerator() GolangAppGenerator {
	return GolangAppGenerator{
		pluginFolder:          "GolangApp",
		generatorId:           "golang_app",
		generatorReadableName: "Golang App",
		generatorLanguage:     "golang",
	}
}

func (p GolangAppGenerator) CreateProjectZip(outpath string, project PluginSpec) (string, error) {
	settings := p.createSettingsForGolangApp(outpath)
	return generateProjectZip(project, settings)
}

// writes the project to the given outpath
func (p GolangAppGenerator) CreateProject(outpath string, project PluginSpec) error {
	settings := p.createSettingsForGolangApp(outpath)
	return createProject(project, settings)
}

// returns the generator id
func (p GolangAppGenerator) GeneratorId() string { return p.generatorId }

// returns a readbale name for the generator
func (p GolangAppGenerator) GeneratorReadableName() string { return p.generatorReadableName }

func (p GolangAppGenerator) GeneratorLanguage() string { return p.generatorLanguage }

func (p GolangAppGenerator) createSettingsForGolangApp(outpath string) GeneratorSettings {
	return GeneratorSettings{
		ProjectFs: golangAppProject,
		RootDir:   p.pluginFolder,
		OutPath:   outpath,
	}
}
