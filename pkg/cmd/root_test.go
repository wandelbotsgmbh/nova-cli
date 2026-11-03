package cmd

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	homedir "github.com/mitchellh/go-homedir"
	"github.com/spf13/viper"
)

func configHome(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Windows doesn't use Unix permission bits")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	homedir.DisableCache = true
	t.Cleanup(func() { homedir.DisableCache = false })
	// a config.yaml in the working directory must not be picked up
	t.Chdir(home)
	viper.Reset()
	t.Cleanup(viper.Reset)
	return home
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Errorf("%s: mode %04o, want %04o", path, got, want)
	}
}

func TestInitConfigCreatesPrivateConfig(t *testing.T) {
	home := configHome(t)
	initConfig()

	dir := filepath.Join(home, ".nova")
	assertMode(t, dir, 0o700)
	assertMode(t, filepath.Join(dir, "config.yaml"), 0o600)

	// viper's own writes keep the mode
	viper.Set("editor", "vim")
	if err := viper.WriteConfig(); err != nil {
		t.Fatal(err)
	}
	assertMode(t, filepath.Join(dir, "config.yaml"), 0o600)
}

func TestInitConfigRestrictsExistingConfig(t *testing.T) {
	home := configHome(t)
	dir := filepath.Join(home, ".nova")
	file := filepath.Join(dir, "config.yaml")
	content := "host: 192.168.1.10\n"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	// independent of the umask of the test run
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(file, 0o644); err != nil {
		t.Fatal(err)
	}

	initConfig()

	assertMode(t, dir, 0o700)
	assertMode(t, file, 0o600)
	if b, _ := os.ReadFile(file); string(b) != content {
		t.Errorf("content changed: %q", b)
	}
	if got := viper.GetString("host"); got != "192.168.1.10" {
		t.Errorf("host = %q", got)
	}
}

func TestRestrictPermissionsKeepsStricterModes(t *testing.T) {
	configHome(t)
	file := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(file, nil, 0o400); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(file, 0o400); err != nil {
		t.Fatal(err)
	}
	restrictPermissions(file, 0o600)
	assertMode(t, file, 0o400)
}
