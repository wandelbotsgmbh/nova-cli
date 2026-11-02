package appenv

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUpdateKeepsOtherLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env.local")
	existing := "# my comment\nNOVA_API=\"old\"\nMY_VAR=keep\nexport ACCESS_TOKEN=expired\nLOG_LEVEL=warn\n"
	if err := os.WriteFile(path, []byte(existing), 0600); err != nil {
		t.Fatal(err)
	}

	err := Update(path,
		[]KV{{"NOVA_API", "https://host"}, {"ACCESS_TOKEN", "fresh"}, {"CELL_NAME", "cell"}},
		[]KV{{"LOG_LEVEL", "debug"}})
	if err != nil {
		t.Fatal(err)
	}

	got, _ := os.ReadFile(path)
	want := "# my comment\nNOVA_API=\"https://host\"\nMY_VAR=keep\nACCESS_TOKEN=\"fresh\"\nLOG_LEVEL=warn\nCELL_NAME=\"cell\"\n"
	if string(got) != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestUpdateCreatesPrivateFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env.local")
	if err := Update(path, []KV{{"ACCESS_TOKEN", "secret"}}, []KV{{"LOG_LEVEL", "debug"}}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("mode = %v, want 0600", info.Mode().Perm())
	}
	got, _ := os.ReadFile(path)
	if want := "# Local development settings, updated by `nova app update-env`.\n# Contains an access token: keep this file out of git.\nACCESS_TOKEN=\"secret\"\nLOG_LEVEL=\"debug\"\n"; string(got) != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestDetectLayout(t *testing.T) {
	for file, want := range map[string]string{"Cargo.toml": ".env.local", "go.mod": ".env.local", "package.json": ".env.local", "pyproject.toml": ".env"} {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, file), nil, 0644)
		layout, err := DetectLayout(dir)
		if err != nil || layout.File != want {
			t.Errorf("%s: got %q, %v", file, layout.File, err)
		}
	}
	if _, err := DetectLayout(t.TempDir()); err == nil {
		t.Error("expected error for unknown project")
	}
}
