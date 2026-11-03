package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeAppProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"Dockerfile": "FROM scratch\n",
		".nova":      "name: my-app\nimageRegistry: registry.example.com/team\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestGetAppContext(t *testing.T) {
	dir := writeAppProject(t)

	// the project folder and the .nova file in it resolve to the same project (issue #9)
	for _, arg := range []string{dir, filepath.Join(dir, ".nova")} {
		ctx, err := getAppContext([]string{arg})
		if err != nil {
			t.Fatalf("getAppContext(%q): %v", arg, err)
		}
		if ctx.DockerContextPath != dir {
			t.Errorf("getAppContext(%q).DockerContextPath = %q, want %q", arg, ctx.DockerContextPath, dir)
		}
		if ctx.NovaApp.Name != "my-app" {
			t.Errorf("getAppContext(%q).NovaApp.Name = %q, want my-app", arg, ctx.NovaApp.Name)
		}
	}
}

func TestGetAppContextNoProject(t *testing.T) {
	dir := writeAppProject(t)
	missingNova := t.TempDir()
	if err := os.WriteFile(filepath.Join(missingNova, "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	for name, arg := range map[string]string{
		"missing dir":  filepath.Join(dir, "nope"),
		"regular file": filepath.Join(dir, "Dockerfile"), // stat of Dockerfile/Dockerfile fails with ENOTDIR
		".nova dir":    filepath.Join(t.TempDir(), ".nova"),
		"no .nova":     missingNova,
		"empty folder": t.TempDir(),
	} {
		_, err := getAppContext([]string{arg})
		if err == nil || !strings.Contains(err.Error(), "is no nova app project folder") {
			t.Errorf("%s: getAppContext(%q) error = %v, want 'is no nova app project folder'", name, arg, err)
		}
	}
}
