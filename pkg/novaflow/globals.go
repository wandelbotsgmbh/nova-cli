package novaflow

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
)

// Global is a control plane global (a typed value flows read, e.g. a pose).
type Global struct {
	Key         string          `json:"key"`
	Type        string          `json:"type"`
	Value       json.RawMessage `json:"value"`
	Description *string         `json:"description,omitempty"`
	JSONSchema  json.RawMessage `json:"json_schema,omitempty"`
}

func (c *Client) globalsPath() string {
	return c.appPath(ControlPlaneApp, "/api/v1/globals")
}

// ListGlobals returns all globals of the cell.
func (c *Client) ListGlobals(ctx context.Context) ([]Global, error) {
	var resp struct {
		Globals []Global `json:"globals"`
	}
	if _, err := c.do(ctx, request{method: http.MethodGet, path: c.globalsPath()}, &resp); err != nil {
		return nil, err
	}
	return resp.Globals, nil
}

// PutGlobals creates or replaces globals in one batch. The control plane
// checks every entry first, so a batch with one bad entry writes nothing.
func (c *Client) PutGlobals(ctx context.Context, globals []Global) error {
	if len(globals) == 0 {
		return nil
	}
	type entry struct {
		Value       json.RawMessage `json:"value"`
		Type        string          `json:"type"`
		Description *string         `json:"description,omitempty"`
		JSONSchema  json.RawMessage `json:"json_schema,omitempty"`
	}
	batch := make(map[string]entry, len(globals))
	for _, g := range globals {
		batch[g.Key] = entry{Value: g.Value, Type: g.Type, Description: g.Description, JSONSchema: nullToEmpty(g.JSONSchema)}
	}
	_, err := c.do(ctx, request{method: http.MethodPut, path: c.globalsPath(), jsonBody: map[string]any{"globals": batch}}, nil)
	return err
}

func nullToEmpty(r json.RawMessage) json.RawMessage {
	if strings.TrimSpace(string(r)) == "null" {
		return nil
	}
	return r
}

// The cell object store is NOVA's, shared by every app of the cell. Forest
// keeps its flows there too (ForestObjectPrefix).

// ForestObjectPrefix is where Forest stores flows in the cell object store.
// Backups leave these keys out: flows go through Forest's API, which keeps its
// caches and change events consistent.
const ForestObjectPrefix = "trees/"

func (c *Client) objectPath(key string) string {
	p := "/api/v2/cells/" + url.PathEscape(c.cell) + "/store/objects"
	if key == "" {
		return p
	}
	// keys may contain "/", which the store expects unescaped
	segs := strings.Split(key, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	return p + "/" + strings.Join(segs, "/")
}

// ListObjects returns the keys of the cell object store.
func (c *Client) ListObjects(ctx context.Context) ([]string, error) {
	var keys []string
	_, err := c.do(ctx, request{method: http.MethodGet, path: c.objectPath("")}, &keys)
	if IsStatus(err, http.StatusNotFound) {
		return nil, nil // the store is created on first write
	}
	return keys, err
}

// GetObject returns an object's content and content type.
func (c *Client) GetObject(ctx context.Context, key string) ([]byte, string, error) {
	resp, err := c.send(ctx, request{method: http.MethodGet, path: c.objectPath(key), header: http.Header{"Accept": {"*/*"}}})
	if err != nil {
		return nil, "", err
	}
	return resp.body, resp.header.Get("Content-Type"), nil
}

// PutObject stores an object.
func (c *Client) PutObject(ctx context.Context, key string, data []byte) error {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile("any_value", lastSegment(key))
	if err != nil {
		return err
	}
	if _, err := part.Write(data); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	_, err = c.send(ctx, request{method: http.MethodPut, path: c.objectPath(key), body: &buf, contentType: w.FormDataContentType()})
	return err
}

func lastSegment(key string) string {
	if i := strings.LastIndex(key, "/"); i >= 0 {
		return key[i+1:]
	}
	return key
}

// ValidObjectKey rejects keys that could escape a backup directory.
func ValidObjectKey(key string) error {
	if key == "" || strings.HasPrefix(key, "/") || strings.Contains(key, "\\") {
		return fmt.Errorf("invalid object key %q", key)
	}
	for _, s := range strings.Split(key, "/") {
		if s == "" || s == "." || s == ".." {
			return fmt.Errorf("invalid object key %q", key)
		}
	}
	return nil
}
