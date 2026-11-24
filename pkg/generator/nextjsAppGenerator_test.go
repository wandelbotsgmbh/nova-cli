package generator

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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

func TestNextJsAppPackageName(t *testing.T) {
	dir := t.TempDir()
	if err := NewNextJsAppGenerator().CreateProject(dir, PluginSpec{Name: "My App", ModuleName: "my_app", PluginName: "my-app"}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	var pkg struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(b, &pkg); err != nil {
		t.Fatalf("package.json is no valid JSON: %v", err)
	}
	if pkg.Name != "my-app" {
		t.Errorf("package name = %q, want my-app", pkg.Name)
	}
}
