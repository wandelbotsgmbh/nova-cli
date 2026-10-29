package generator

import (
	"fmt"
	"os"
	"testing"
)

func TestGetSpecWithValidProjectName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"Valid Name", "valid_project_name", "valid_project_name"},
		{"Invalid Characters", "invalid!project@name", "invalid_project_name"},
		{"Starts With Digit", "123projectname", "_123projectname"},
		{"Empty String", "", "your_nova_app"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in := PluginSpec{
				Name:       tc.input,
				ModuleName: tc.input,
			}

			expected := PluginSpec{
				Name:       tc.expected,
				ModuleName: tc.expected,
			}

			actual := getSpecWithValidProjectName(in)
			if actual != expected {
				t.Errorf("getSpecWithValidProjectName(%q) = \nactual   %q\nexpected %q", tc.input, actual, expected)
			}
		})
	}
}

func TestCreatePythonAppProject(t *testing.T) {
	// uncomment to test manually
	t.Skip("has to be executed manually to check file contents. No automated test right now")

	path, err := os.MkdirTemp("", "testproject")
	//defer os.RemoveAll(path)

	if err != nil {
		t.Errorf("Error creating temporary directory: %v", err)
	}

	fmt.Println("writing project to", path)

	generator := NewPythonGenerator()
	err = generator.CreateProject(path, PluginSpec{
		Language:   "Python",
		Name:       "testproject",
		ModuleName: "testmodule",
	})

	// Check for the expected outcome
	if err != nil {
		t.Errorf("CreateProject() returned an error: %v", err)
	}
}

func TestCreatePythonAppProjectZip(t *testing.T) {
	// uncomment to test manually
	t.Skip("has to be executed manually to check file contents. No automated test right now")

	path, err := os.MkdirTemp("", "testproject")
	//defer os.RemoveAll(path)

	if err != nil {
		t.Errorf("Error creating temporary directory: %v", err)
	}

	fmt.Println("writing project to", path)

	generator := NewPythonGenerator()
	zipPath, err := generator.CreateProjectZip(path, PluginSpec{
		Language:   "Python",
		Name:       "testproject",
		ModuleName: "testmodule",
	})

	//defer os.RemoveAll(zipPath)

	// Check for the expected outcome
	if err != nil {
		t.Errorf("CreateProject() returned an error: %v", err)
	}

	fmt.Println("zip file created at", zipPath)
}
