package generator

import "embed"

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
		ProjectFs: rustAppProject,
		RootDir:   p.pluginFolder,
		OutPath:   outpath,
	}
}
