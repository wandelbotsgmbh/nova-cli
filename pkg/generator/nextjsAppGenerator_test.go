package generator

import (
	"fmt"
	"os"
	"testing"
)

func TestCreateNextJsAppProject(t *testing.T) {
	// uncomment to test manually
	t.Skip("has to be executed manually to check file contents. No automated test right now")

	path, err := os.MkdirTemp("", "testproject")
	//defer os.RemoveAll(path)

	if err != nil {
		t.Errorf("Error creating temporary directory: %v", err)
	}

	fmt.Println("writing project to", path)

	generator := NewNextJsAppGenerator()
	err = generator.CreateProject(path, PluginSpec{
		Language:   generator.generatorLanguage,
		Name:       "testproject",
		ModuleName: "testmodule",
		InstanceInformation: InstanceInformation{
			NovaApiHost: "hostname",
			BasicAuth: BasicAuth{
				Username: "testuser",
				Password: "testpassword",
			},
		},
	})

	// Check for the expected outcome
	if err != nil {
		t.Errorf("CreateProject() returned an error: %v", err)
	}
}
