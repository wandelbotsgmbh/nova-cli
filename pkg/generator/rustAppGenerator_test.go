package generator

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCreateRustAppProject(t *testing.T) {
	path := t.TempDir()

	generator := NewRustAppGenerator()
	err := generator.CreateProject(path, PluginSpec{
		Language:   generator.generatorLanguage,
		Name:       "My Rust App",
		ModuleName: "My Rust App",
		PluginName: "my-rust-app",
		InstanceInformation: InstanceInformation{
			NovaApiHost: "hostname",
			CellName:    "cell",
		},
	})
	if err != nil {
		t.Fatalf("CreateProject() returned an error: %v", err)
	}

	for _, file := range []string{"Cargo.toml", "Cargo.lock"} {
		content, err := os.ReadFile(filepath.Join(path, file))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(content), `name = "my-rust-app"`) {
			t.Errorf("%s does not contain the package name", file)
		}
	}

	// every template must be rendered, with no leftovers
	err = filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if strings.HasSuffix(p, templateSuffix) {
			t.Errorf("unrendered template %s", p)
		}
		content, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if strings.Contains(string(content), "{{") {
			t.Errorf("template placeholder left in %s", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
