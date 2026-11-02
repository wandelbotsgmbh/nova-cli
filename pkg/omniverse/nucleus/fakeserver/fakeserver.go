// Package fakeserver provides a fake Nucleus server for testing nucleus clients.
//
// The server handles the SOWS auth handshake and connlib API protocol over
// local WebSockets. It is intended for unit and integration tests that need
// a Nucleus-compatible endpoint without a real server.
//
// Usage:
//
//	fake := fakeserver.New()
//	defer fake.Close()
//	fake.SetEntries("/", []nucleus.Entry{
//	    {Path: "/Projects", PathType: "folder"},
//	})
//	client, err := nucleus.Dial(ctx, nucleus.ClientConfig{
//	    Host: fake.Host(), APIToken: "any", TLS: false,
//	})
package fakeserver

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"

	"github.com/coder/websocket"

	"github.com/wandelbotsgmbh/nova-cli/pkg/omniverse/nucleus"
	"github.com/wandelbotsgmbh/nova-cli/pkg/omniverse/nucleus/sows"
)

// Server is a fake Nucleus server for testing.
type Server struct {
	httpServer *httptest.Server

	mu      sync.Mutex
	entries map[string][]nucleus.Entry // dir path -> entries
	files   map[string][]byte          // file path -> content

	useLFT      bool           // redirect reads to the fake LFT endpoint
	lftFailures map[string]int // file path -> error pages still to serve
	lftRequests map[string]int // file path -> LFT GETs received

	apiConns    map[*websocket.Conn]struct{} // live API WebSockets
	apiAccepted int                          // API WebSockets accepted so far
}

// New creates and starts a fake Nucleus server.
func New() *Server {
	s := &Server{
		entries:     make(map[string][]nucleus.Entry),
		files:       make(map[string][]byte),
		lftFailures: make(map[string]int),
		lftRequests: make(map[string]int),
		apiConns:    make(map[*websocket.Conn]struct{}),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/omni/auth", s.handleAuth)
	mux.HandleFunc("/omni/api", s.handleAPI)
	mux.HandleFunc("/omni/lft/content/", s.handleLFTContent)
	s.httpServer = httptest.NewServer(mux)
	return s
}

// lftHashBlockSize is the hash_bsize real servers report (1 MiB); kept small
// here so tests exercise multi-block hashing with tiny files.
const lftHashBlockSize = 4

// UseLFT makes read responses redirect to the fake LFT HTTP endpoint (with
// size and sha-256-flat hash) instead of inlining the content, mirroring
// what real servers do for most files.
func (s *Server) UseLFT(on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.useLFT = on
}

// FailLFT makes the next n LFT downloads of path return what the real
// ingress serves when the LFT service is down: a plain-text error page
// with HTTP 200. Later requests serve the real content.
func (s *Server) FailLFT(path string, n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lftFailures[path] = n
}

// LFTRequests returns how many LFT downloads were attempted for path.
func (s *Server) LFTRequests(path string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lftRequests[path]
}

// handleLFTContent serves GET /omni/lft/content/?ticket=<base64 path>.
func (s *Server) handleLFTContent(w http.ResponseWriter, r *http.Request) {
	pathBytes, err := base64.StdEncoding.DecodeString(r.URL.Query().Get("ticket"))
	if err != nil {
		http.Error(w, `{"code": 6, "message": "Invalid ticket."}`, http.StatusBadRequest)
		return
	}
	path := string(pathBytes)

	s.mu.Lock()
	s.lftRequests[path]++
	content, ok := s.files[path]
	fail := s.lftFailures[path] > 0
	if fail {
		s.lftFailures[path]--
	}
	s.mu.Unlock()

	if !ok {
		http.Error(w, `{"code": 6, "message": "Invalid ticket."}`, http.StatusBadRequest)
		return
	}
	if fail {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "Error 503: Service Unavailable\nThe server is temporarily overloading or down\n\nHost: fake\nOriginal URI: %s\nIngress Name: nucleus-lft\n", r.URL.RequestURI())
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Write(content)
}

// Close shuts down the fake server.
func (s *Server) Close() {
	s.httpServer.Close()
}

// Host returns the "host:port" address suitable for [nucleus.ClientConfig.Host].
func (s *Server) Host() string {
	return s.httpServer.Listener.Addr().String()
}

// SetEntries configures the directory listing returned for a given path.
func (s *Server) SetEntries(dir string, entries []nucleus.Entry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[dir] = entries
}

// SetFile stores file content for a given path.
func (s *Server) SetFile(path string, content []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.files[path] = content
}

// GetFile returns stored file content.
func (s *Server) GetFile(path string) ([]byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, ok := s.files[path]
	return data, ok
}

// getEntries returns the entries for a path, or nil if not configured.
func (s *Server) getEntries(dir string) []nucleus.Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.entries[dir]
}

// addEntry adds an entry to a directory listing.
func (s *Server) addEntry(dir string, entry nucleus.Entry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[dir] = append(s.entries[dir], entry)
}

// removeEntry removes an entry from a directory listing.
func (s *Server) removeEntry(dir string, path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries := s.entries[dir]
	for i, e := range entries {
		if strings.TrimRight(e.Path, "/") == strings.TrimRight(path, "/") {
			s.entries[dir] = append(entries[:i], entries[i+1:]...)
			return
		}
	}
}

// handleAuth fakes the SOWS auth handshake.
func (s *Server) handleAuth(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer conn.CloseNow()

	_, data, err := conn.Read(r.Context())
	if err != nil {
		return
	}

	var requestID uint32
	if len(data) >= 5 {
		requestID = uint32(data[1]) | uint32(data[2])<<8 | uint32(data[3])<<16 | uint32(data[4])<<24
	}

	resp, _ := sows.BuildResponse(requestID, true, map[string]string{
		"status":       "OK",
		"access_token": "fake-access-token",
		"username":     "testuser",
	})

	conn.Write(r.Context(), websocket.MessageBinary, resp)
	conn.Close(websocket.StatusNormalClosure, "")
}

// DropConnections abruptly closes every live API WebSocket, simulating the
// server (or a proxy) dropping the connection mid-session. Clients see the
// read loop end and subsequent writes fail.
func (s *Server) DropConnections() {
	s.mu.Lock()
	conns := make([]*websocket.Conn, 0, len(s.apiConns))
	for c := range s.apiConns {
		conns = append(conns, c)
	}
	s.mu.Unlock()
	for _, c := range conns {
		c.CloseNow()
	}
}

// APIConnections returns how many API WebSockets the server has accepted,
// i.e. 1 + the number of client reconnects.
func (s *Server) APIConnections() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.apiAccepted
}

// handleAPI fakes the connlib API WebSocket.
func (s *Server) handleAPI(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	s.mu.Lock()
	s.apiConns[conn] = struct{}{}
	s.apiAccepted++
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.apiConns, conn)
		s.mu.Unlock()
		conn.CloseNow()
	}()

	ctx := r.Context()
	var lastID uint32
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return
		}

		// Split on null byte (connlib framing).
		jsonPart := data
		var binaryPart []byte
		if idx := bytes.IndexByte(data, 0); idx >= 0 {
			jsonPart = data[:idx]
			if idx+1 < len(data) {
				binaryPart = data[idx+1:]
			}
		}

		var req map[string]any
		if err := json.Unmarshal(jsonPart, &req); err != nil {
			continue
		}

		id := uint32(req["id"].(float64))
		command, _ := req["command"].(string)

		// Real servers reject a request whose id is not higher than the last
		// one seen on the connection with INVALID_COMMAND (measured live).
		if id <= lastID {
			s.reply(ctx, conn, id, map[string]any{"status": "INVALID_COMMAND"})
			continue
		}
		lastID = id

		switch command {
		case "authorize_token":
			s.reply(ctx, conn, id, map[string]any{
				"status":         "OK",
				"username":       "testuser",
				"token":          "fake-session-token",
				"connection_id":  "conn-test-001",
				"max_chunk_size": 1048576,
				"lft_threshold":  0,
				"version":        "2.7.0",
			})

		case "list2":
			path, _ := req["path"].(string)
			entries := s.getEntries(path)
			if entries == nil {
				s.reply(ctx, conn, id, map[string]any{"status": "INVALID_URI"})
				continue
			}
			s.reply(ctx, conn, id, map[string]any{
				"status":  "OK",
				"entries": entries,
			})
			s.reply(ctx, conn, id, map[string]any{"status": "DONE"})

		case "create_directory":
			pathObj, _ := req["path"].(map[string]any)
			path, _ := pathObj["path"].(string)
			if path == "" {
				s.reply(ctx, conn, id, map[string]any{"status": "INVALID_PARAMETERS"})
				continue
			}
			// Auto-register the directory as having entries.
			s.mu.Lock()
			if _, exists := s.entries[path]; !exists {
				s.entries[path] = []nucleus.Entry{}
			}
			s.mu.Unlock()
			// Add to parent listing.
			parent := parentDir(path)
			if parent != "" {
				s.addEntry(parent, nucleus.Entry{
					Path:     strings.TrimRight(path, "/"),
					PathType: "folder",
				})
			}
			s.reply(ctx, conn, id, map[string]any{"status": "OK"})

		case "delete2":
			pathsRaw, _ := req["paths_to_delete"].([]any)
			responses := make([]string, len(pathsRaw))
			for i, p := range pathsRaw {
				pm, _ := p.(map[string]any)
				path, _ := pm["path"].(string)
				// Check if non-empty directory.
				dirPath := path
				if !strings.HasSuffix(dirPath, "/") {
					dirPath += "/"
				}
				entries := s.getEntries(dirPath)
				if entries != nil && len(entries) > 0 {
					responses[i] = "FOLDER_NOT_EMPTY"
				} else {
					// Remove from parent listing and clean up.
					parent := parentDir(path)
					if parent != "" {
						s.removeEntry(parent, path)
					}
					s.mu.Lock()
					delete(s.entries, dirPath)
					delete(s.files, path)
					s.mu.Unlock()
					responses[i] = "OK"
				}
			}
			s.reply(ctx, conn, id, map[string]any{
				"status":    "OK",
				"responses": responses,
			})

		case "copy2":
			pathsRaw, _ := req["paths_to_copy"].([]any)
			responses := make([]string, len(pathsRaw))
			for i, p := range pathsRaw {
				pm, _ := p.(map[string]any)
				srcObj, _ := pm["src"].(map[string]any)
				dstObj, _ := pm["dst"].(map[string]any)
				srcPath, _ := srcObj["path"].(string)
				dstPath, _ := dstObj["path"].(string)
				// Only file copy is supported (like real Nucleus).
				s.mu.Lock()
				content, ok := s.files[srcPath]
				s.mu.Unlock()
				if !ok {
					responses[i] = "INVALID_URI"
				} else {
					s.mu.Lock()
					s.files[dstPath] = append([]byte{}, content...)
					s.mu.Unlock()
					parent := parentDir(dstPath)
					if parent != "" {
						s.addEntry(parent, nucleus.Entry{
							Path:     dstPath,
							PathType: "file",
							Size:     int64(len(content)),
						})
					}
					responses[i] = "OK"
				}
			}
			s.reply(ctx, conn, id, map[string]any{
				"status":    "OK",
				"responses": responses,
			})

		case "rename2":
			pathsRaw, _ := req["paths_to_rename"].([]any)
			responses := make([]string, len(pathsRaw))
			for i, p := range pathsRaw {
				pm, _ := p.(map[string]any)
				srcObj, _ := pm["src"].(map[string]any)
				dstObj, _ := pm["dst"].(map[string]any)
				srcPath, _ := srcObj["path"].(string)
				dstPath, _ := dstObj["path"].(string)
				_ = srcPath
				_ = dstPath
				responses[i] = "OK"
			}
			s.reply(ctx, conn, id, map[string]any{
				"status":    "OK",
				"responses": responses,
			})
			s.reply(ctx, conn, id, map[string]any{"status": "DONE"})

		case "create":
			uri, _ := req["uri"].(string)
			s.mu.Lock()
			s.files[uri] = append([]byte{}, binaryPart...)
			s.mu.Unlock()
			parent := parentDir(uri)
			if parent != "" {
				s.addEntry(parent, nucleus.Entry{
					Path:     uri,
					PathType: "file",
					Size:     int64(len(binaryPart)),
				})
			}
			s.reply(ctx, conn, id, map[string]any{"status": "OK"})

		case "read":
			uri, _ := req["uri"].(string)
			s.mu.Lock()
			content, ok := s.files[uri]
			s.mu.Unlock()
			if !ok {
				s.reply(ctx, conn, id, map[string]any{"status": "INVALID_URI"})
				continue
			}
			s.mu.Lock()
			useLFT := s.useLFT
			s.mu.Unlock()
			if useLFT {
				ticket := base64.StdEncoding.EncodeToString([]byte(uri))
				s.reply(ctx, conn, id, map[string]any{
					"status":          "OK",
					"uri_redirection": s.httpServer.URL + "/omni/lft/content/?ticket=" + ticket,
					"size":            len(content),
					"hash_type":       "sha-256-flat",
					"hash_value":      nucleus.FlatHash(content, lftHashBlockSize),
					"hash_bsize":      lftHashBlockSize,
				})
				continue
			}
			// Send response with binary data.
			respJSON, _ := json.Marshal(map[string]any{
				"status": "LATEST",
				"id":     id,
				"size":   len(content),
			})
			frame := bytes.Join([][]byte{respJSON, content}, []byte{0})
			conn.Write(ctx, websocket.MessageBinary, frame)

		default:
			s.reply(ctx, conn, id, map[string]any{"status": "UNKNOWN_COMMAND"})
		}
	}
}

// reply sends a connlib JSON response.
func (s *Server) reply(ctx context.Context, conn *websocket.Conn, id uint32, msg map[string]any) {
	msg["id"] = id
	data, _ := json.Marshal(msg)
	conn.Write(ctx, websocket.MessageBinary, data)
}

// parentDir returns the parent directory path for a file or directory.
// e.g. "/Projects/foo/bar.txt" -> "/Projects/foo/"
//
//	"/Projects/foo/" -> "/Projects/"
func parentDir(path string) string {
	clean := strings.TrimRight(path, "/")
	idx := strings.LastIndex(clean, "/")
	if idx <= 0 {
		return "/"
	}
	return clean[:idx+1]
}
