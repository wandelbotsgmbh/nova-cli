// Package appenv writes the local development settings (instance url, cell, access token)
// of a generated app project into its gitignored env file.
package appenv

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Settings are the values written into the env file of an app project.
type Settings struct {
	// NOVA API url with protocol, e.g. https://xdxqzvpu.instance.wandelbots.io
	NovaApi string
	Cell    string
	// Name of the app (from .nova), used for the base path
	AppName     string
	AccessToken string
}

// KV is a single env variable.
type KV struct {
	Key   string
	Value string
}

// Layout describes where a project type keeps its local env and which variables it reads.
type Layout struct {
	// File relative to the project root
	File string
	// Variables which are always written
	Values func(s Settings) []KV
	// Variables which are only written if not present yet
	Defaults []KV
}

var serverLayout = Layout{
	File: ".env.local",
	Values: func(s Settings) []KV {
		return []KV{
			{"NOVA_API", s.NovaApi},
			{"CELL_NAME", s.Cell},
			{"BASE_PATH", "/" + s.Cell + "/" + s.AppName},
			{"ACCESS_TOKEN", s.AccessToken},
		}
	},
	Defaults: []KV{{"LOG_LEVEL", "debug"}},
}

var nextJsLayout = Layout{
	File: ".env.local",
	Values: func(s Settings) []KV {
		return []KV{
			{"NOVA_DEV_INSTANCE_URL", s.NovaApi},
			{"NOVA_DEV_ACCESS_TOKEN", s.AccessToken},
			{"CELL_ID", s.Cell},
		}
	},
}

var pythonLayout = Layout{
	File: ".env",
	Values: func(s Settings) []KV {
		return []KV{
			{"NOVA_API", s.NovaApi},
			{"CELL_NAME", s.Cell},
			// novax registers the programs in the cell's program store under the app name from BASE_PATH
			{"BASE_PATH", "/" + s.Cell + "/" + s.AppName},
			{"NOVA_ACCESS_TOKEN", s.AccessToken},
		}
	},
}

// DetectLayout returns the env layout for the app project in dir, based on its build files.
func DetectLayout(dir string) (Layout, error) {
	exists := func(name string) bool {
		_, err := os.Stat(filepath.Join(dir, name))
		return err == nil
	}
	switch {
	case exists("Cargo.toml"), exists("go.mod"):
		return serverLayout, nil
	case exists("package.json"):
		return nextJsLayout, nil
	case exists("pyproject.toml"):
		return pythonLayout, nil
	}
	return Layout{}, fmt.Errorf("unknown app project type in %s: expected Cargo.toml, go.mod, package.json or pyproject.toml", dir)
}

// Write updates the env file of the project in dir and returns its path.
// Existing lines (comments, other variables) are kept, the managed variables are replaced in place.
func Write(dir string, layout Layout, s Settings) (string, error) {
	path := filepath.Join(dir, layout.File)
	return path, Update(path, layout.Values(s), layout.Defaults)
}

// Update sets values in the env file at path, adds defaults which are not set yet
// and keeps everything else. The file is created with 0600 if it does not exist.
func Update(path string, values, defaults []KV) error {
	content, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}

	var lines []string
	if len(content) > 0 {
		lines = strings.Split(strings.TrimRight(string(content), "\n"), "\n")
	} else {
		lines = []string{"# Local development settings, updated by `nova app update-env`.", "# Contains an access token: keep this file out of git."}
	}

	set := func(kv KV, overwrite bool) {
		for i, line := range lines {
			if lineKey(line) == kv.Key {
				if overwrite {
					lines[i] = format(kv)
				}
				return
			}
		}
		lines = append(lines, format(kv))
	}
	for _, kv := range values {
		set(kv, true)
	}
	for _, kv := range defaults {
		set(kv, false)
	}

	return os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0600)
}

func lineKey(line string) string {
	line = strings.TrimPrefix(strings.TrimSpace(line), "export ")
	if strings.HasPrefix(line, "#") {
		return ""
	}
	key, _, found := strings.Cut(line, "=")
	if !found {
		return ""
	}
	return strings.TrimSpace(key)
}

func format(kv KV) string {
	return fmt.Sprintf("%s=%q", kv.Key, kv.Value)
}
