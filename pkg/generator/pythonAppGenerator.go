package generator

import (
	"embed"
	"regexp"
	"strings"
)

//go:embed all:PythonApp/*
var pythonProject embed.FS

type PythonAppGenerator struct {
	pluginFolder          string
	generatorId           string
	generatorReadableName string
	generatorLanguage     string
}

func NewPythonGenerator() PythonAppGenerator {
	return PythonAppGenerator{
		pluginFolder:          "PythonApp",
		generatorId:           "python_app",
		generatorReadableName: "Python App",
		generatorLanguage:     "Python",
	}
}

func (p PythonAppGenerator) CreateProjectZip(outpath string, project PluginSpec) (string, error) {
	project = getSpecWithValidProjectName(project)
	settings := GeneratorSettings{
		ProjectFs: pythonProject,
		RootDir:   p.pluginFolder,
		OutPath:   outpath,
	}
	return generateProjectZip(project, settings)
}

// writes the project to the given outpath
func (p PythonAppGenerator) CreateProject(outpath string, project PluginSpec) error {
	project = getSpecWithValidProjectName(project)
	settings := GeneratorSettings{
		ProjectFs: pythonProject,
		RootDir:   p.pluginFolder,
		OutPath:   outpath,
	}
	return createProject(project, settings)
}

// returns the generator id
func (p PythonAppGenerator) GeneratorId() string { return p.generatorId }

// returns a readbale name for the generator
func (p PythonAppGenerator) GeneratorReadableName() string { return p.generatorReadableName }

func (p PythonAppGenerator) GeneratorLanguage() string { return p.generatorLanguage }

func getSpecWithValidProjectName(project PluginSpec) PluginSpec {
	project.Name = getValidProjectName(project.Name)
	project.ModuleName = getValidProjectName(project.ModuleName)
	return project
}

// Returns a valid poetry project name
func getValidProjectName(projectName string) string {
	validName := strings.ToLower(projectName)
	notAllowedCharsRegex := regexp.MustCompile(`[^a-zA-Z0-9_]+`)
	doesNotStartWithNumberRegex := regexp.MustCompile(`^[0-9]`)
	validName = notAllowedCharsRegex.ReplaceAllString(validName, "_")
	validName = doesNotStartWithNumberRegex.ReplaceAllStringFunc(validName, func(s string) string {
		return "_" + s
	})
	if len(validName) == 0 {
		validName = "your_nova_app"
	}
	return validName
}
