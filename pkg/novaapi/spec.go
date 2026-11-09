package novaapi

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/wandelbotsgmbh/nova-cli/pkg/debug"
	"gopkg.in/yaml.v3"
)

// SpecPath is where an instance serves its v2 OpenAPI spec.
const SpecPath = BasePath + "/ui/public.openapi.yaml"

// Spec is the parsed v2 OpenAPI spec of an instance.
type Spec struct {
	// NovaVersion is the instance's NOVA version, the cache key.
	NovaVersion string
	doc         map[string]any
	ops         []Operation
}

var httpMethods = []string{"get", "put", "post", "delete", "patch"}

// ParseSpec reads a spec (YAML or JSON).
func ParseSpec(data []byte, novaVersion string) (*Spec, error) {
	var doc map[string]any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parsing the OpenAPI spec: %w", err)
	}
	doc, _ = normalize(doc).(map[string]any)
	paths, _ := doc["paths"].(map[string]any)
	if len(paths) == 0 {
		return nil, fmt.Errorf("the OpenAPI spec has no paths")
	}
	s := &Spec{NovaVersion: novaVersion, doc: doc}
	for p, item := range paths {
		methods, _ := item.(map[string]any)
		for _, m := range httpMethods {
			op, ok := methods[m].(map[string]any)
			if !ok {
				continue
			}
			id, _ := op["operationId"].(string)
			summary, _ := op["summary"].(string)
			stream := op["x-serverstream"] == true || op["x-bidistream"] == true
			s.ops = append(s.ops, Operation{ID: id, Method: strings.ToUpper(m), Path: BasePath + p, Summary: summary, Stream: stream})
		}
	}
	sortOperations(s.ops)
	return s, nil
}

// Operations lists the spec's operations, sorted by path and method.
func (s *Spec) Operations() []Operation { return s.ops }

// APIVersion is info.version of the spec (e.g. 2.8.0).
func (s *Spec) APIVersion() string {
	info, _ := s.doc["info"].(map[string]any)
	v, _ := info["version"].(string)
	return v
}

// NovaVersion returns the instance's NOVA version (e.g. 26.8.0).
func (c *Client) NovaVersion(ctx context.Context) (string, error) {
	b, _, err := c.get(ctx, BasePath+"/system/version", "text/plain, application/json")
	if err != nil {
		return "", err
	}
	v := strings.Trim(strings.TrimSpace(string(b)), `"`)
	if v == "" {
		return "", fmt.Errorf("the instance reported no NOVA version")
	}
	return v, nil
}

// LoadSpec returns the instance's spec, from cacheDir if a copy for its host and
// NOVA version is there, else fetched and cached (ADR-0013). An empty cacheDir
// disables the cache.
func (c *Client) LoadSpec(ctx context.Context, cacheDir string) (*Spec, error) {
	novaVersion, err := c.NovaVersion(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading the NOVA version: %w", err)
	}
	file := ""
	if cacheDir != "" {
		file = filepath.Join(cacheDir, cacheName(c.Host()), cacheName(novaVersion)+".json")
		if b, err := os.ReadFile(file); err == nil {
			if s, err := ParseSpec(b, novaVersion); err == nil {
				debug.Logf("api: spec from cache %s", file)
				return s, nil
			}
		}
	}
	b, _, err := c.get(ctx, SpecPath, "application/yaml, application/json, text/plain, */*")
	if err != nil {
		return nil, fmt.Errorf("fetching the API spec: %w", err)
	}
	s, err := ParseSpec(b, novaVersion)
	if err != nil {
		return nil, err
	}
	if file != "" {
		if err := writeCache(file, s.doc); err != nil {
			debug.Logf("api: caching the spec: %v", err)
		}
	}
	return s, nil
}

func writeCache(file string, doc map[string]any) error {
	b, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		return err
	}
	tmp := file + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, file)
}

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9._-]`)

func cacheName(s string) string {
	return unsafeName.ReplaceAllString(s, "_")
}

// normalize turns maps with non-string keys (YAML allows unquoted 200 as a
// response code) into map[string]any, so the document can be cached as JSON.
func normalize(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			t[k] = normalize(val)
		}
		return t
	case map[any]any:
		m := make(map[string]any, len(t))
		for k, val := range t {
			m[fmt.Sprint(k)] = normalize(val)
		}
		return m
	case []any:
		for i, val := range t {
			t[i] = normalize(val)
		}
		return t
	}
	return v
}
