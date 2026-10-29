package specfile

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	"github.com/wandelbotsgmbh/nova-cli/pkg/httpx"
	"gopkg.in/yaml.v3"
)

// ReadFileContent reads the content of a file or url and returns it as a byte slice (string).
func ReadFileContent(pathToSpec string) ([]byte, error) {
	if strings.HasPrefix(pathToSpec, "http://") || strings.HasPrefix(pathToSpec, "https://") {
		c := httpx.Client()
		_, err := url.Parse(pathToSpec)
		if err != nil {
			return nil, err
		}

		resp, err := c.Get(pathToSpec)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, fmt.Errorf("failed to get spec: %s", resp.Status)
		}
		return io.ReadAll(resp.Body)
	}
	return os.ReadFile(pathToSpec)
}

// Reads the content of a file or url and tries to unmarshal it from json or yaml.
// This means content can be either json or yaml.
// First it will try to unmarshal it as json, if this fails it will try to unmarshal it as yaml.
func UnmarshalSpec(pathToSpec string) (map[string]any, error) {
	fileContent, err := ReadFileContent(pathToSpec)
	if err != nil {
		return nil, err
	}

	out := make(map[string]any)
	err = json.Unmarshal(fileContent, &out)
	if err != nil {
		err = yaml.Unmarshal(fileContent, &out)
		if err != nil {
			return nil, fmt.Errorf("provided spec seems to be neither json nor yaml: %w", err)
		}
	}
	return out, nil
}

// Simple check if the given path might be a URL or a file with supported extension (json, yaml, yml, cell).
func IsSpec(pathToSpec string) bool {
	hasExtension := strings.HasSuffix(pathToSpec, ".json") || strings.HasSuffix(pathToSpec, ".yaml") || strings.HasSuffix(pathToSpec, ".yml") || strings.HasSuffix(pathToSpec, ".cell")
	isHttpSource := strings.HasPrefix(pathToSpec, "http://") || strings.HasPrefix(pathToSpec, "https://")

	if !hasExtension && !isHttpSource {
		return false
	}

	if !isHttpSource {
		fi, err := os.Stat(pathToSpec)
		if err != nil {
			return false
		}
		if fi.IsDir() {
			return false
		}
		return true
	}

	// for http we make nor further checks. It will fail later when reading/installing the manifest.
	return true
}
