package nucleus

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// List returns the entries in a directory.
func (c *Client) List(ctx context.Context, path string) ([]Entry, error) {
	if path != "/" && !strings.HasSuffix(path, "/") {
		path += "/"
	}
	responses, err := c.conn.CallStream(ctx, "list2", map[string]any{
		"path": path,
	})
	if err != nil {
		return nil, fmt.Errorf("list %s: %w", path, err)
	}

	var entries []Entry
	for _, resp := range responses {
		var r struct {
			Status  string  `json:"status"`
			Entries []Entry `json:"entries"`
		}
		if err := json.Unmarshal(resp, &r); err != nil {
			return nil, fmt.Errorf("parse list response: %w", err)
		}
		if r.Status != "" && r.Status != "OK" && r.Status != "DONE" {
			return nil, fmt.Errorf("list %s: %s", path, r.Status)
		}
		entries = append(entries, r.Entries...)
	}

	return entries, nil
}

// Stat returns the entry information for a path.
// It lists the parent directory and finds the matching entry.
func (c *Client) Stat(ctx context.Context, path string) (*Entry, error) {
	// Normalize: ensure path ends without trailing slash for matching,
	// but use the parent directory (with trailing slash) for listing.
	clean := strings.TrimRight(path, "/")
	if clean == "" {
		// Root: return a synthetic entry
		return &Entry{Path: "/", PathType: "folder"}, nil
	}

	// Find parent directory
	parentIdx := strings.LastIndex(clean, "/")
	parent := clean[:parentIdx+1] // e.g. "/Projects/" for "/Projects/Sims"

	entries, err := c.List(ctx, parent)
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", path, err)
	}

	for _, e := range entries {
		ename := strings.TrimRight(e.Path, "/")
		if ename == clean {
			return &e, nil
		}
	}

	return nil, fmt.Errorf("stat %s: not found", path)
}

// CreateFolder creates a directory at the given path.
func (c *Client) CreateFolder(ctx context.Context, path string) error {
	// Nucleus requires a trailing slash for directory paths.
	if !strings.HasSuffix(path, "/") {
		path += "/"
	}
	resp, err := c.conn.Call(ctx, "create_directory", map[string]any{
		"path": map[string]any{
			"path": path,
		},
	})
	if err != nil {
		return fmt.Errorf("create_directory %s: %w", path, err)
	}

	var r struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(resp, &r); err != nil {
		return fmt.Errorf("parse create_directory response: %w", err)
	}
	if r.Status != "OK" && r.Status != "ALREADY_EXISTS" {
		return fmt.Errorf("create_directory %s: %s", path, r.Status)
	}

	return nil
}

// EnsureParentDirs creates all parent directories for the given path.
// Existing directories are silently skipped (ALREADY_EXISTS is not an error).
func (c *Client) EnsureParentDirs(ctx context.Context, path string) error {
	// Find the parent directory path.
	clean := strings.TrimRight(path, "/")
	parentIdx := strings.LastIndex(clean, "/")
	if parentIdx <= 0 {
		return nil // root or top-level, nothing to create
	}
	parent := clean[:parentIdx+1] // e.g. "/Projects/testcli/" for "/Projects/testcli/file.txt"

	// Build list of directories to create, from deepest to shallowest.
	var dirs []string
	for dir := parent; dir != "/"; {
		dirs = append(dirs, dir)
		dir = strings.TrimRight(dir, "/")
		idx := strings.LastIndex(dir, "/")
		if idx <= 0 {
			break
		}
		dir = dir[:idx+1]
	}

	// Create from shallowest to deepest.
	for i := len(dirs) - 1; i >= 0; i-- {
		if err := c.CreateFolder(ctx, dirs[i]); err != nil {
			return err
		}
	}
	return nil
}

// Delete removes a file or empty directory.
// For non-empty directories, use DeleteRecursive.
func (c *Client) Delete(ctx context.Context, path string) error {
	return c.deleteSingle(ctx, path)
}

// DeleteRecursive removes a file or directory, including all contents.
func (c *Client) DeleteRecursive(ctx context.Context, path string) error {
	err := c.deleteSingle(ctx, path)
	if err == nil {
		return nil
	}
	if !strings.Contains(err.Error(), "FOLDER_NOT_EMPTY") {
		return err
	}

	entries, listErr := c.List(ctx, path)
	if listErr != nil {
		return fmt.Errorf("delete %s: listing contents: %w", path, listErr)
	}
	for _, e := range entries {
		childPath := e.Path
		if e.IsDir() && !strings.HasSuffix(childPath, "/") {
			childPath += "/"
		}
		if err := c.DeleteRecursive(ctx, childPath); err != nil {
			return err
		}
	}
	return c.deleteSingle(ctx, path)
}

// deleteSingle deletes a single file or empty directory.
func (c *Client) deleteSingle(ctx context.Context, path string) error {
	resp, err := c.conn.Call(ctx, "delete2", map[string]any{
		"paths_to_delete": []map[string]any{
			{"path": path},
		},
	})
	if err != nil {
		return fmt.Errorf("delete %s: %w", path, err)
	}

	var r struct {
		Status    string   `json:"status"`
		Responses []string `json:"responses"`
	}
	if err := json.Unmarshal(resp, &r); err != nil {
		return fmt.Errorf("parse delete response: %w", err)
	}
	if r.Status != "OK" {
		return fmt.Errorf("delete %s: %s", path, r.Status)
	}
	for _, s := range r.Responses {
		if s != "OK" {
			return fmt.Errorf("delete %s: %s", path, s)
		}
	}

	return nil
}

// Copy copies a file or directory from src to dst.
// Copy copies a file or directory from src to dst.
// For directories, it recursively copies all contents.
func (c *Client) Copy(ctx context.Context, src, dst string) error {
	// Try a direct file copy first.
	err := c.copySingle(ctx, src, dst)
	if err == nil {
		return nil
	}
	if !strings.Contains(err.Error(), "INVALID_URI") {
		return err
	}

	// INVALID_URI likely means it's a directory — copy recursively.
	srcDir := src
	if !strings.HasSuffix(srcDir, "/") {
		srcDir += "/"
	}
	dstDir := dst
	if !strings.HasSuffix(dstDir, "/") {
		dstDir += "/"
	}

	// Create the destination directory.
	if err := c.CreateFolder(ctx, dstDir); err != nil {
		return fmt.Errorf("copy %s -> %s: creating destination: %w", src, dst, err)
	}

	// List source contents and copy each entry.
	entries, err := c.List(ctx, srcDir)
	if err != nil {
		return fmt.Errorf("copy %s -> %s: listing source: %w", src, dst, err)
	}
	for _, e := range entries {
		// Entry paths are absolute; extract the relative part after srcDir.
		relPath := strings.TrimPrefix(e.Path, srcDir)
		relPath = strings.TrimPrefix(relPath, "/")
		childDst := dstDir + relPath

		if e.IsDir() {
			childSrc := e.Path
			if !strings.HasSuffix(childSrc, "/") {
				childSrc += "/"
			}
			if !strings.HasSuffix(childDst, "/") {
				childDst += "/"
			}
			if err := c.Copy(ctx, childSrc, childDst); err != nil {
				return err
			}
		} else {
			if err := c.copySingle(ctx, e.Path, childDst); err != nil {
				return err
			}
		}
	}
	return nil
}

// copySingle copies a single file via copy2.
func (c *Client) copySingle(ctx context.Context, src, dst string) error {
	resp, err := c.conn.Call(ctx, "copy2", map[string]any{
		"paths_to_copy": []map[string]any{
			{
				"src": map[string]any{"path": src},
				"dst": map[string]any{"path": dst},
			},
		},
	})
	if err != nil {
		return fmt.Errorf("copy %s -> %s: %w", src, dst, err)
	}

	var r struct {
		Status    string   `json:"status"`
		Responses []string `json:"responses"`
	}
	if err := json.Unmarshal(resp, &r); err != nil {
		return fmt.Errorf("parse copy response: %w", err)
	}
	if r.Status != "OK" {
		return fmt.Errorf("copy %s -> %s: %s", src, dst, r.Status)
	}
	for _, s := range r.Responses {
		if s != "OK" {
			return fmt.Errorf("copy %s -> %s: %s", src, dst, s)
		}
	}

	return nil
}

// Move renames/moves a file or directory.
func (c *Client) Move(ctx context.Context, src, dst string) error {
	responses, err := c.conn.CallStream(ctx, "rename2", map[string]any{
		"paths_to_rename": []map[string]any{
			{
				"src": map[string]any{"path": src},
				"dst": map[string]any{"path": dst},
			},
		},
	})
	if err != nil {
		return fmt.Errorf("move %s -> %s: %w", src, dst, err)
	}

	for _, resp := range responses {
		var r struct {
			Status string `json:"status"`
		}
		if err := json.Unmarshal(resp, &r); err != nil {
			return fmt.Errorf("parse move response: %w", err)
		}
		if r.Status != "" && r.Status != "OK" && r.Status != "DONE" {
			return fmt.Errorf("move %s -> %s: %s", src, dst, r.Status)
		}
	}

	return nil
}

// WriteFile uploads content to a path. For large files (above the server's
// LFT threshold), it uploads via the LFT HTTP service automatically.
func (c *Client) WriteFile(ctx context.Context, path string, content []byte) error {
	return c.WriteFileFrom(ctx, path, bytes.NewReader(content), int64(len(content)))
}

// WriteFileFrom uploads size bytes read from r to a path. Files above the
// server's LFT threshold are streamed via the LFT HTTP service (constant
// memory); smaller ones are buffered and sent inline over the connlib
// WebSocket.
func (c *Client) WriteFileFrom(ctx context.Context, path string, r io.Reader, size int64) error {
	// Check that the parent directory exists to fail fast with a clear error
	// (otherwise LFT hangs for ~30s then returns HTTP 408).
	parent := path[:strings.LastIndex(path, "/")+1]
	if parent != "" && parent != "/" {
		if _, err := c.List(ctx, parent); err != nil {
			return fmt.Errorf("write %s: parent directory %s does not exist (use -p to create)", path, parent)
		}
	}

	// Use LFT for files above the threshold (if configured).
	if c.connInfo.LFTAddress != "" && c.connInfo.LFTThreshold > 0 && size >= c.connInfo.LFTThreshold {
		return c.uploadLFT(ctx, path, r, size)
	}
	content, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("write %s: reading content: %w", path, err)
	}
	return c.writeFileConnlib(ctx, path, content)
}

// writeFileConnlib uploads content inline over the connlib WebSocket.
func (c *Client) writeFileConnlib(ctx context.Context, path string, content []byte) error {
	id := c.conn.NextID()

	msg := map[string]any{
		"id":        id,
		"command":   "create",
		"uri":       path,
		"overwrite": true,
	}

	jsonData, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal create: %w", err)
	}

	// Connlib binary payload: json + \0 + binary
	payload := make([]byte, 0, len(jsonData)+1+len(content))
	payload = append(payload, jsonData...)
	payload = append(payload, 0)
	payload = append(payload, content...)

	ch := c.conn.Register(id)
	defer c.conn.Unregister(id)

	if err := c.conn.Send(ctx, payload); err != nil {
		return fmt.Errorf("send create: %w", err)
	}

	select {
	case resp := <-ch:
		var r struct {
			Status string `json:"status"`
		}
		if err := json.Unmarshal(resp, &r); err != nil {
			return fmt.Errorf("parse create response: %w", err)
		}
		if r.Status != "OK" {
			return fmt.Errorf("write %s: %s", path, r.Status)
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ReadFile downloads the content of a file.
// The server may return the content inline (binary after \0) or redirect
// to the LFT HTTP service via a uri_redirection field.
func (c *Client) ReadFile(ctx context.Context, path string) ([]byte, error) {
	id := c.conn.NextID()

	msg := map[string]any{
		"id":      id,
		"command": "read",
		"uri":     path,
	}

	jsonData, err := json.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("marshal read: %w", err)
	}

	ch := c.conn.RegisterBinary(id, 1)
	defer c.conn.UnregisterBinary(id)

	if err := c.conn.Send(ctx, jsonData); err != nil {
		return nil, fmt.Errorf("send read: %w", err)
	}

	select {
	case frame, ok := <-ch:
		if !ok {
			return nil, fmt.Errorf("read %s: connection closed", path)
		}
		var r struct {
			Status         string `json:"status"`
			URIRedirection string `json:"uri_redirection"`
			Size           int64  `json:"size"`
		}
		if err := json.Unmarshal(frame.JSON, &r); err != nil {
			return nil, fmt.Errorf("parse read response: %w", err)
		}
		if r.Status != "OK" && r.Status != "LATEST" {
			return nil, fmt.Errorf("read %s: %s", path, r.Status)
		}
		// If the server redirects to LFT, download via HTTP.
		if r.URIRedirection != "" {
			return c.downloadLFT(ctx, r.URIRedirection)
		}
		// Otherwise content is inline.
		return frame.Binary, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// downloadLFT fetches file content from the LFT HTTP service.
func (c *Client) downloadLFT(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("lft request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("lft download: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("lft download: HTTP %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("lft read body: %w", err)
	}

	return data, nil
}

// uploadLFT streams size bytes from body to the LFT HTTP service.
func (c *Client) uploadLFT(ctx context.Context, path string, body io.Reader, size int64) error {
	encodedPath := base64.StdEncoding.EncodeToString([]byte(path))
	params := fmt.Sprintf("path=%s&token=%s&iat=%d",
		encodedPath, c.accessToken, time.Now().UnixMilli())
	url := fmt.Sprintf("%s/path/?%s", c.connInfo.LFTAddress, params)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
	if err != nil {
		return fmt.Errorf("lft upload request: %w", err)
	}
	// body is a generic reader (e.g. *os.File), so the length isn't inferred.
	req.ContentLength = size
	req.Header.Set("Content-Type", "application/octet-stream")

	// Use a transport with dial/TLS/response-header timeouts but no overall timeout,
	// so large file transfers aren't killed mid-stream.
	httpClient := &http.Client{
		Transport: &http.Transport{
			ResponseHeaderTimeout: 60 * time.Second,
		},
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("lft upload: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		if resp.StatusCode == http.StatusRequestTimeout {
			return fmt.Errorf("lft upload %s: server timed out (parent directory may not exist — use -p to create)", path)
		}
		return fmt.Errorf("lft upload: HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}
