package nucleus

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/wandelbotsgmbh/nova-cli/pkg/httpx"
	"github.com/wandelbotsgmbh/nova-cli/pkg/omniverse/nucleus/connlib"
)

// List returns the entries in a directory.
// If the API connection has died, it reconnects and retries.
func (c *Client) List(ctx context.Context, path string) ([]Entry, error) {
	if path != "/" && !strings.HasSuffix(path, "/") {
		path += "/"
	}
	var responses []json.RawMessage
	err := c.withReconnect(ctx, func() error {
		return c.withCmd(func() error {
			var err error
			responses, err = c.api().CallStream(ctx, "list2", map[string]any{
				"path": path,
			})
			return err
		})
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
	var resp json.RawMessage
	err := c.withCmd(func() error {
		var err error
		resp, err = c.api().Call(ctx, "create_directory", map[string]any{
			"path": map[string]any{
				"path": path,
			},
		})
		return err
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
	var resp json.RawMessage
	err := c.withCmd(func() error {
		var err error
		resp, err = c.api().Call(ctx, "delete2", map[string]any{
			"paths_to_delete": []map[string]any{
				{"path": path},
			},
		})
		return err
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
	var resp json.RawMessage
	err := c.withCmd(func() error {
		var err error
		resp, err = c.api().Call(ctx, "copy2", map[string]any{
			"paths_to_copy": []map[string]any{
				{
					"src": map[string]any{"path": src},
					"dst": map[string]any{"path": dst},
				},
			},
		})
		return err
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
	var responses []json.RawMessage
	err := c.withCmd(func() error {
		var err error
		responses, err = c.api().CallStream(ctx, "rename2", map[string]any{
			"paths_to_rename": []map[string]any{
				{
					"src": map[string]any{"path": src},
					"dst": map[string]any{"path": dst},
				},
			},
		})
		return err
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
	if c.Info().LFTAddress != "" && c.Info().LFTThreshold > 0 && size >= c.Info().LFTThreshold {
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
	conn := c.api()
	return c.withCmd(func() error {
		id := conn.NextID()
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
		payload := bytes.Join([][]byte{jsonData, content}, []byte{0})

		ch := conn.Register(id)
		defer conn.Unregister(id)

		if err := conn.Send(ctx, payload); err != nil {
			return fmt.Errorf("send create: %w", err)
		}

		select {
		case resp, ok := <-ch:
			if !ok {
				return fmt.Errorf("write %s: %w", path, connlib.ErrClosed)
			}
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
	})
}

// Default retry policy for transient read failures (LFT 5xx, ingress error
// pages, network errors, lost API connection, content that fails
// verification). Backoff doubles per attempt and is capped: 1s, 2s, 4s, 8s,
// 16s, 30s, 30s — about 90s in total, enough to ride out a short overload
// of the LFT service. Override via [ClientConfig].
const (
	defaultReadMaxAttempts  = 8
	defaultReadRetryBackoff = time.Second
	maxReadRetryBackoff     = 30 * time.Second
)

// lftToken returns the JWT access token for the current connection.
func (c *Client) lftToken() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.accessToken
}

// retryPolicy returns the effective attempts/backoff from the config.
func (c *Client) retryPolicy() (attempts int, backoff time.Duration) {
	attempts, backoff = c.cfg.ReadMaxAttempts, c.cfg.ReadRetryBackoff
	if attempts <= 0 {
		attempts = defaultReadMaxAttempts
	}
	if backoff <= 0 {
		backoff = defaultReadRetryBackoff
	}
	return attempts, backoff
}

// retryDelay returns the capped exponential delay before attempt+1.
func retryDelay(backoff time.Duration, attempt int) time.Duration {
	d := backoff << (attempt - 1)
	if d > maxReadRetryBackoff || d <= 0 {
		d = maxReadRetryBackoff
	}
	return d
}

// withReconnect runs op and, if it failed because the API connection died,
// re-dials and runs it again (with the read retry policy). Only use it for
// idempotent operations.
func (c *Client) withReconnect(ctx context.Context, op func() error) error {
	maxAttempts, backoff := c.retryPolicy()
	for attempt := 1; ; attempt++ {
		dead := c.api()
		err := op()
		if err == nil || ctx.Err() != nil || !errors.Is(err, connlib.ErrClosed) || attempt >= maxAttempts {
			return err
		}
		select {
		case <-time.After(retryDelay(backoff, attempt)):
		case <-ctx.Done():
			return ctx.Err()
		}
		if rerr := c.reconnect(ctx, dead); rerr != nil && ctx.Err() != nil {
			return ctx.Err()
		}
		// If reconnecting failed we still loop: op will fail fast with
		// ErrClosed and we try to reconnect again after the next delay.
	}
}

// transientError marks a failure that is worth retrying with a fresh read
// request (and therefore a fresh LFT ticket).
type transientError struct{ err error }

func (e *transientError) Error() string { return e.err.Error() }
func (e *transientError) Unwrap() error { return e.err }

// IsTransient reports whether err is a transient download failure that was
// retried and still failed (e.g. the LFT service kept returning 5xx).
func IsTransient(err error) bool {
	var t *transientError
	return errors.As(err, &t)
}

// expectedContent is what the read response promises about the file so the
// downloaded bytes can be verified before they are handed to the caller.
type expectedContent struct {
	Size      int64  // -1 when unknown
	HashType  string // e.g. "sha-256-flat"
	HashValue string // hex digest
	BlockSize int    // hash_bsize; 0 when unknown
}

// flatHasher computes a Nucleus "sha-256-flat" digest incrementally: the
// SHA-256 over the concatenated raw SHA-256 digests of each blockSize-byte
// block of the input (the last block may be shorter). A file that fits in
// one block therefore hashes to sha256(sha256(data)), not sha256(data).
// The block size is the hash_bsize field of the read response (1 MiB on
// current servers).
type flatHasher struct {
	blockSize int
	block     hash.Hash // digest of the block in progress
	inBlock   int       // bytes fed into block so far
	digests   hash.Hash // digest over the finished block digests
	n         int64     // total bytes written
}

func newFlatHasher(blockSize int) *flatHasher {
	if blockSize <= 0 {
		blockSize = 1 << 20
	}
	return &flatHasher{blockSize: blockSize, block: sha256.New(), digests: sha256.New()}
}

func (h *flatHasher) Write(p []byte) (int, error) {
	n := len(p)
	for len(p) > 0 {
		room := h.blockSize - h.inBlock
		chunk := p
		if len(chunk) > room {
			chunk = p[:room]
		}
		h.block.Write(chunk)
		h.inBlock += len(chunk)
		h.n += int64(len(chunk))
		p = p[len(chunk):]
		if h.inBlock == h.blockSize {
			h.flushBlock()
		}
	}
	return n, nil
}

func (h *flatHasher) flushBlock() {
	h.digests.Write(h.block.Sum(nil))
	h.block.Reset()
	h.inBlock = 0
}

// Sum returns the hex digest. It may be called once; the hasher is spent.
func (h *flatHasher) Sum() string {
	if h.inBlock > 0 {
		h.flushBlock()
	}
	return hex.EncodeToString(h.digests.Sum(nil))
}

// FlatHash computes the Nucleus "sha-256-flat" digest of data with the given
// block size (hash_bsize). See [flatHasher] for the algorithm.
func FlatHash(data []byte, blockSize int) string {
	if blockSize <= 0 {
		blockSize = len(data)
	}
	h := newFlatHasher(blockSize)
	h.Write(data)
	return h.Sum()
}

// wantsHash reports whether the server gave us enough to check a digest.
func (e expectedContent) wantsHash() bool {
	return e.HashType == "sha-256-flat" && e.HashValue != "" && e.BlockSize > 0
}

// verifySize checks the byte count against what the server reported.
func (e expectedContent) verifySize(n int64) error {
	if e.Size >= 0 && n != e.Size {
		return fmt.Errorf("size mismatch: got %d bytes, server reported %d", n, e.Size)
	}
	return nil
}

// verifyHash checks a computed digest against the server's. Empty content
// is left to the size check.
func (e expectedContent) verifyHash(got string, n int64) error {
	if !e.wantsHash() || n == 0 {
		return nil
	}
	if !strings.EqualFold(got, e.HashValue) {
		return fmt.Errorf("sha-256-flat mismatch: got %s, server reported %s", got, e.HashValue)
	}
	return nil
}

// verify checks in-memory data against the size and (when the type is
// known) hash reported by the server. A mismatch means we got something
// other than the file, most commonly an ingress error page served with
// HTTP 200.
func (e expectedContent) verify(data []byte) error {
	if err := e.verifySize(int64(len(data))); err != nil {
		return err
	}
	if !e.wantsHash() {
		return nil
	}
	return e.verifyHash(FlatHash(data, e.BlockSize), int64(len(data)))
}

// ReadFile downloads the content of a file into memory. See [Client.DownloadFile]
// for the streaming variant; both verify and retry the same way.
func (c *Client) ReadFile(ctx context.Context, path string) ([]byte, error) {
	var buf bytes.Buffer
	err := c.readFileWithRetry(ctx, path, func() (io.Writer, error) {
		buf.Reset()
		return &buf, nil
	})
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// DownloadFile streams a file to localPath. The content goes to a
// "<localPath>.part" file first and is renamed into place only after it has
// been verified against the size and hash reported by the server, so a
// failed download never leaves anything under the real name. The parent
// directory must exist.
//
// The server may return the content inline (binary after \0) or redirect to
// the LFT HTTP service via a uri_redirection field. Transient failures (LFT
// 5xx, error pages, network errors, lost API connection, verification
// mismatches) are retried with exponential backoff; the returned error
// satisfies [IsTransient] if every attempt failed that way.
func (c *Client) DownloadFile(ctx context.Context, path, localPath string) (err error) {
	tmp := localPath + ".part"
	var f *os.File
	defer func() {
		// f is still open only when an earlier step failed (the success
		// path closes it explicitly below); report a close failure if it
		// is the first error.
		if f != nil {
			if closeErr := f.Close(); closeErr != nil && err == nil {
				err = fmt.Errorf("writing %s: %w", tmp, closeErr)
			}
		}
		if err != nil {
			os.Remove(tmp)
		}
	}()

	err = c.readFileWithRetry(ctx, path, func() (io.Writer, error) {
		if f == nil {
			var openErr error
			f, openErr = os.OpenFile(tmp, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0644)
			return f, openErr
		}
		// Retry: discard whatever the previous attempt wrote.
		if err := f.Truncate(0); err != nil {
			return nil, err
		}
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return nil, err
		}
		return f, nil
	})
	if err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return fmt.Errorf("writing %s: %w", tmp, err)
	}
	f = nil
	return os.Rename(tmp, localPath)
}

// readFileWithRetry runs readFileOnce under the read retry policy. sink is
// called at the start of every attempt that delivers content and must
// return a writer positioned at the beginning (previous attempts' bytes
// discarded).
func (c *Client) readFileWithRetry(ctx context.Context, path string, sink func() (io.Writer, error)) error {
	maxAttempts, backoff := c.retryPolicy()

	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		dead := c.api()
		err := c.readFileOnce(ctx, path, sink)
		if err == nil {
			return nil
		}
		lastErr = err
		lostConn := errors.Is(err, connlib.ErrClosed)
		if ctx.Err() != nil || (!IsTransient(err) && !lostConn) {
			return err
		}
		if attempt == maxAttempts {
			break
		}
		select {
		case <-time.After(retryDelay(backoff, attempt)):
		case <-ctx.Done():
			return ctx.Err()
		}
		if lostConn {
			if rerr := c.reconnect(ctx, dead); rerr != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				lastErr = rerr
			}
		}
	}
	return &transientError{fmt.Errorf("read %s: giving up after %d attempts: %w", path, maxAttempts, lastErr)}
}

// readFileOnce performs a single read request without retries, delivering
// verified content to the writer returned by sink.
func (c *Client) readFileOnce(ctx context.Context, path string, sink func() (io.Writer, error)) error {
	conn := c.api()

	// ID allocation, request and response all happen under the command
	// lock: the server rejects a request whose id is lower than the last
	// one it saw on the connection (INVALID_COMMAND), so ids must hit the
	// wire in allocation order. The LFT transfer below runs outside the
	// lock so parallel downloads overlap.
	var frame connlib.RawFrame
	err := c.withCmd(func() error {
		id := conn.NextID()
		msg := map[string]any{
			"id":      id,
			"command": "read",
			"uri":     path,
		}
		jsonData, err := json.Marshal(msg)
		if err != nil {
			return fmt.Errorf("marshal read: %w", err)
		}

		ch := conn.RegisterBinary(id, 1)
		defer conn.UnregisterBinary(id)

		if err := conn.Send(ctx, jsonData); err != nil {
			return fmt.Errorf("send read: %w", err)
		}
		select {
		case f, ok := <-ch:
			if !ok {
				return fmt.Errorf("read %s: %w", path, connlib.ErrClosed)
			}
			frame = f
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	if err != nil {
		return err
	}

	{
		var r struct {
			Status         string `json:"status"`
			URIRedirection string `json:"uri_redirection"`
			Size           *int64 `json:"size"`
			HashType       string `json:"hash_type"`
			HashValue      string `json:"hash_value"`
			HashBlockSize  int    `json:"hash_bsize"`
		}
		if err := json.Unmarshal(frame.JSON, &r); err != nil {
			return fmt.Errorf("parse read response: %w", err)
		}
		if r.Status != "OK" && r.Status != "LATEST" {
			return fmt.Errorf("read %s: %s", path, r.Status)
		}
		want := expectedContent{Size: -1, HashType: r.HashType, HashValue: r.HashValue, BlockSize: r.HashBlockSize}
		if r.Size != nil {
			want.Size = *r.Size
		}
		// If the server redirects to LFT, download via HTTP.
		if r.URIRedirection != "" {
			return c.downloadLFT(ctx, r.URIRedirection, want, sink)
		}
		// Otherwise content is inline.
		if err := want.verify(frame.Binary); err != nil {
			return &transientError{fmt.Errorf("read %s: inline content: %w", path, err)}
		}
		w, err := sink()
		if err != nil {
			return err
		}
		_, err = w.Write(frame.Binary)
		return err
	}
}

// lftHTTPClient has dial/TLS/response-header timeouts but no overall
// timeout, so large downloads aren't killed mid-stream. Idle connections
// are kept per host so parallel downloads reuse them.
var lftHTTPClient = &http.Client{
	Transport: httpx.WithUserAgent(&http.Transport{
		ResponseHeaderTimeout: 60 * time.Second,
		MaxIdleConnsPerHost:   32,
	}),
}

// maxErrorPageBytes bounds how much of a text/* response we read to report
// it; real error pages are a few hundred bytes.
const maxErrorPageBytes = 64 << 10

// downloadLFT fetches file content from the LFT HTTP service, streaming it
// into the sink while hashing, and verifies it against want.
//
// Beware: the ingress in front of LFT may replace a 5xx from the service with
// its own plain-text error page served as HTTP 200, so a 200 status alone
// does not mean we got the file. The content type plus size/hash
// verification is what catches that.
func (c *Client) downloadLFT(ctx context.Context, url string, want expectedContent, sink func() (io.Writer, error)) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("lft request: %w", err)
	}

	resp, err := lftHTTPClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return &transientError{fmt.Errorf("lft download: %w", err)}
	}
	defer resp.Body.Close()

	// Error responses, and text bodies regardless of status (the ingress
	// error page with a 200), are small: read them for the message and
	// never let them near the sink.
	if resp.StatusCode != http.StatusOK || isTextContent(resp.Header.Get("Content-Type")) {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorPageBytes))
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if resp.StatusCode != http.StatusOK {
			err := fmt.Errorf("lft download: HTTP %d: %s", resp.StatusCode, errorPageSummary(body))
			if resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests ||
				resp.StatusCode == http.StatusRequestTimeout {
				return &transientError{err}
			}
			return err
		}
		return &transientError{fmt.Errorf("lft download: server returned an error page instead of the file: %s", errorPageSummary(body))}
	}

	w, err := sink()
	if err != nil {
		return err
	}
	hasher := newFlatHasher(want.BlockSize)
	n, err := io.Copy(io.MultiWriter(w, hasher), resp.Body)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return &transientError{fmt.Errorf("lft download: %w", err)}
	}
	if err := want.verifySize(n); err != nil {
		return &transientError{fmt.Errorf("lft download: %w", err)}
	}
	if err := want.verifyHash(hasher.Sum(), n); err != nil {
		return &transientError{fmt.Errorf("lft download: %w", err)}
	}
	return nil
}

// isTextContent reports whether a Content-Type denotes text (plain or HTML),
// which the LFT service never uses for file content.
func isTextContent(contentType string) bool {
	return strings.HasPrefix(contentType, "text/")
}

// errorPageSummary returns the first non-empty line of an error body (e.g.
// "Error 503: Service Unavailable"), truncated, or "" if the body is empty.
func errorPageSummary(body []byte) string {
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if r := []rune(line); len(r) > 120 {
			line = string(r[:120]) + "…"
		}
		return line
	}
	return ""
}

// uploadLFT streams size bytes from body to the LFT HTTP service.
func (c *Client) uploadLFT(ctx context.Context, path string, body io.Reader, size int64) error {
	encodedPath := base64.StdEncoding.EncodeToString([]byte(path))
	params := fmt.Sprintf("path=%s&token=%s&iat=%d",
		encodedPath, c.lftToken(), time.Now().UnixMilli())
	url := fmt.Sprintf("%s/path/?%s", c.Info().LFTAddress, params)

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
		Transport: httpx.WithUserAgent(&http.Transport{
			ResponseHeaderTimeout: 60 * time.Second,
		}),
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
