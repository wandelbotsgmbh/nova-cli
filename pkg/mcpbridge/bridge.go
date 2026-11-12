// Package mcpbridge serves a remote MCP server (Streamable HTTP) over stdio:
// every JSON-RPC message read from stdin is POSTed to the endpoint with fresh
// credentials, and the answers are written to stdout, one message per line.
//
// It lets an MCP client that only knows the server as a local command (claude
// mcp add x -- nova …) reach an HTTP endpoint behind an instance login whose
// tokens expire: the bridge authenticates every request itself.
package mcpbridge

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/wandelbotsgmbh/nova-cli/pkg/ingress"
)

// Bridge forwards JSON-RPC messages from stdio to one MCP endpoint. Run one
// Serve at a time.
type Bridge struct {
	// Endpoint is the url of the MCP server.
	Endpoint string
	// HTTP sends the requests. Redirects should not be followed: the login
	// redirect would turn "not logged in" into the login page's answer.
	HTTP *http.Client
	// Auth sets the credentials of a request. It must not start an interactive
	// login: stdin and stdout belong to the MCP client.
	Auth func(ctx context.Context, req *http.Request) error
	// Hint is appended to errors about an unreachable endpoint (e.g. how to
	// check that the server is installed).
	Hint string
	// Log receives failures that can't be answered (of notifications); default
	// os.Stderr.
	Log io.Writer

	mu              sync.Mutex
	sessionID       string
	protocolVersion string
	// the requests in flight, by id, for notifications/cancelled
	inFlight map[string]*pending

	// writeMu serializes writes to out, apart from mu: a client that is slow to
	// read stdout must not hold up cancellations
	writeMu sync.Mutex
	out     io.Writer
}

// pending is a request in flight.
type pending struct {
	cancel context.CancelFunc
	// byClient is set when the client cancelled it (no answer then)
	byClient atomic.Bool
}

// maxMessage bounds a single message read from stdin (a saved flow can be large).
// A larger one is answered with an error and skipped; the bridge keeps serving.
var maxMessage = 256 << 20

// JSON-RPC error codes used for answers the bridge makes up itself.
const (
	codeParseError     = -32700
	codeInvalidRequest = -32600
	codeInternalError  = -32603
)

// Serve forwards the messages read from in until it ends (EOF); requests still
// in flight are answered before it returns. Requests are forwarded concurrently
// (a long tool call doesn't block a ping); answers are written to out whole,
// one per line. Every request gets exactly one answer, unless the client
// cancels it (notifications/cancelled).
//
// A batch (JSON array) is split into its messages, each answered on its own:
// MCP 2025-06-18 has no batches, and servers may reject them.
func (b *Bridge) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	b.writeMu.Lock()
	b.out = out
	b.writeMu.Unlock()
	b.mu.Lock()
	b.inFlight = map[string]*pending{}
	b.mu.Unlock()
	r := bufio.NewReaderSize(in, 1<<16)
	var wg sync.WaitGroup
	defer wg.Wait()
	for {
		line, tooLarge, err := readLine(r)
		if tooLarge {
			// its id is unknown without reading it whole
			b.writeError(json.RawMessage("null"), codeInvalidRequest,
				fmt.Sprintf("invalid request: message larger than %d MiB, skipped", maxMessage>>20))
		} else if t := bytes.TrimSpace(line); len(t) > 0 {
			for _, msg := range split(t) {
				b.dispatch(ctx, &wg, msg)
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// readLine reads one line. A line longer than maxMessage is read to its end
// and dropped (tooLarge), so the next line starts clean.
func readLine(r *bufio.Reader) (line []byte, tooLarge bool, err error) {
	for {
		chunk, err := r.ReadSlice('\n')
		if !tooLarge {
			line = append(line, chunk...)
			if len(line) > maxMessage {
				line, tooLarge = nil, true
			}
		}
		if !errors.Is(err, bufio.ErrBufferFull) {
			return line, tooLarge, err
		}
	}
}

// split returns the messages of a batch, or msg itself.
func split(msg []byte) [][]byte {
	if !bytes.HasPrefix(msg, []byte("[")) {
		return [][]byte{append([]byte(nil), msg...)}
	}
	var batch []json.RawMessage
	if json.Unmarshal(msg, &batch) != nil || len(batch) == 0 {
		return [][]byte{append([]byte(nil), msg...)} // answered as a parse error
	}
	out := make([][]byte, len(batch))
	for i, m := range batch {
		out[i] = append([]byte(nil), m...)
	}
	return out
}

// message is the part of a JSON-RPC message the bridge looks at.
type message struct {
	ID     json.RawMessage
	Method string
	// RequestID is params.requestId of notifications/cancelled
	RequestID json.RawMessage
	// badMethod: method is there but no string
	badMethod bool
}

// parseMessage reads a message loosely: params of any shape (positional
// params are valid JSON-RPC and answered by the server). ok is false if msg is
// no JSON object.
func parseMessage(msg []byte) (m message, ok bool) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(msg, &fields) != nil || fields == nil {
		return m, false
	}
	m.ID = fields["id"]
	if raw, has := fields["method"]; has {
		if json.Unmarshal(raw, &m.Method) != nil || m.Method == "" {
			m.badMethod = true
		}
	}
	if m.Method == "notifications/cancelled" {
		var p struct {
			RequestID json.RawMessage `json:"requestId"`
		}
		if json.Unmarshal(fields["params"], &p) == nil {
			m.RequestID = p.RequestID
		}
	}
	return m, true
}

func (m message) isRequest() bool {
	return m.Method != "" && len(m.ID) > 0 && string(m.ID) != "null"
}

// idKey normalizes an id, so the server's re-encoding ("\u0061" for "a",
// 1e2 for 100) still matches.
func idKey(id json.RawMessage) string {
	d := json.NewDecoder(bytes.NewReader(id))
	d.UseNumber()
	var v any
	if d.Decode(&v) != nil {
		return "raw:" + string(id)
	}
	switch t := v.(type) {
	case string:
		return "s:" + t
	case json.Number:
		if f, err := strconv.ParseFloat(t.String(), 64); err == nil {
			return "n:" + strconv.FormatFloat(f, 'g', -1, 64)
		}
		return "n:" + t.String()
	}
	return "raw:" + string(id)
}

func (b *Bridge) dispatch(ctx context.Context, wg *sync.WaitGroup, msg []byte) {
	m, ok := parseMessage(msg)
	if !ok {
		b.writeError(json.RawMessage("null"), codeParseError, "parse error: not a JSON-RPC message")
		return
	}
	if m.badMethod {
		id := m.ID
		if len(id) == 0 {
			id = json.RawMessage("null")
		}
		b.writeError(id, codeInvalidRequest, "invalid request: method must be a non-empty string")
		return
	}
	if len(m.RequestID) > 0 {
		b.mu.Lock()
		p := b.inFlight[idKey(m.RequestID)]
		b.mu.Unlock()
		if p != nil {
			p.byClient.Store(true)
			p.cancel()
		}
	}
	reqCtx, cancel := context.WithCancel(ctx)
	p := &pending{cancel: cancel}
	key := ""
	if m.isRequest() {
		key = idKey(m.ID)
		b.mu.Lock()
		b.inFlight[key] = p
		b.mu.Unlock()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer cancel()
		b.forward(reqCtx, m, key, p, msg)
		if key != "" {
			b.mu.Lock()
			if b.inFlight[key] == p { // a reused id may have replaced it
				delete(b.inFlight, key)
			}
			b.mu.Unlock()
		}
	}()
}

func (b *Bridge) forward(ctx context.Context, m message, key string, p *pending, msg []byte) {
	answered, err := b.post(ctx, msg, m.Method == "initialize", key)
	switch {
	case key == "":
		if err != nil {
			b.logf("%s: %v", m.Method, err)
		}
	case answered:
		// a later failure (e.g. the rest of an SSE stream) must not answer twice
	case p.byClient.Load():
		// cancelled by the client: no answer (MCP cancellation)
	case err != nil:
		b.writeError(m.ID, codeInternalError, err.Error())
	default:
		b.writeError(m.ID, codeInternalError, fmt.Sprintf("%s: the server sent no answer to %s", b.Endpoint, m.Method))
	}
}

// post sends msg and writes the answers. answered tells whether the answer to
// the request with id key was written.
func (b *Bridge) post(ctx context.Context, msg []byte, isInitialize bool, key string) (answered bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.Endpoint, bytes.NewReader(msg))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if !isInitialize {
		b.mu.Lock()
		if b.sessionID != "" {
			req.Header.Set("Mcp-Session-Id", b.sessionID)
		}
		if b.protocolVersion != "" {
			req.Header.Set("MCP-Protocol-Version", b.protocolVersion)
		}
		b.mu.Unlock()
	}
	if b.Auth != nil {
		if err := b.Auth(ctx, req); err != nil {
			return false, err
		}
	}
	resp, err := b.HTTP.Do(req)
	if err != nil {
		return false, fmt.Errorf("%s: %w", b.Endpoint, err)
	}
	defer resp.Body.Close()

	ok := resp.StatusCode >= 200 && resp.StatusCode < 300
	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" && isInitialize && ok {
		b.mu.Lock()
		b.sessionID = sid
		b.mu.Unlock()
	}
	if ingress.Strict.IsLoginRedirect(resp) {
		return false, errors.New("the instance rejected the request as unauthenticated: run 'nova login'")
	}
	if resp.StatusCode == http.StatusAccepted || resp.StatusCode == http.StatusNoContent {
		return false, nil
	}
	if ok && strings.HasPrefix(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") {
		return b.copyEvents(resp.Body, isInitialize, key)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, int64(maxMessage)))
	if err != nil {
		return false, fmt.Errorf("%s: reading the answer: %w", b.Endpoint, err)
	}
	if ingress.Strict.IsUnauthorized(resp, body) {
		return false, errors.New("the instance rejected the request as unauthenticated: run 'nova login'")
	}
	status := resp.StatusCode
	if code, isPage := ingress.Strict.ErrorPageCode(resp.Header, body); isPage {
		status = code
	}
	// a JSON-RPC answer is passed on, also with an error status (servers answer
	// a malformed request with 400 and a JSON-RPC error)
	if isJSONRPC(body) {
		return b.writeAnswer(body, isInitialize, key)
	}
	if status >= 200 && status < 300 && len(bytes.TrimSpace(body)) == 0 {
		return false, nil
	}
	return false, b.statusError(resp, status, body)
}

func (b *Bridge) statusError(resp *http.Response, status int, body []byte) error {
	e := fmt.Sprintf("%s: HTTP %d", b.Endpoint, status)
	if t := bytes.TrimSpace(body); len(t) > 0 && !bytes.HasPrefix(t, []byte("<")) &&
		!strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "html") {
		e += ": " + ingress.ErrorDetail(body) // an HTML page (the ingress' 404) says nothing more
	}
	if loc := resp.Header.Get("Location"); status >= 300 && status < 400 && loc != "" {
		e += " (redirect to " + loc + ", not followed)"
	}
	if (status == http.StatusNotFound || status == http.StatusBadGateway || status == http.StatusServiceUnavailable) && b.Hint != "" {
		e += " (" + b.Hint + ")"
	}
	return errors.New(e)
}

// isJSONRPC reports whether body is a JSON-RPC message or batch.
func isJSONRPC(body []byte) bool {
	t := bytes.TrimSpace(body)
	var probe struct {
		JSONRPC string `json:"jsonrpc"`
	}
	if bytes.HasPrefix(t, []byte("[")) {
		var batch []json.RawMessage
		if json.Unmarshal(t, &batch) != nil || len(batch) == 0 {
			return false
		}
		t = batch[0]
	}
	return json.Unmarshal(t, &probe) == nil && probe.JSONRPC == "2.0"
}

// copyEvents writes the data of every event of an SSE answer.
func (b *Bridge) copyEvents(body io.Reader, isInitialize bool, key string) (answered bool, err error) {
	s := bufio.NewScanner(body)
	s.Buffer(make([]byte, 0, 1<<16), maxMessage)
	var data [][]byte
	flush := func() error {
		if len(data) == 0 {
			return nil
		}
		ev := bytes.Join(data, []byte("\n"))
		data = nil
		if !isJSONRPC(ev) {
			return nil
		}
		a, err := b.writeAnswer(ev, isInitialize, key)
		answered = answered || a
		return err
	}
	for s.Scan() {
		line := s.Bytes()
		switch {
		case len(line) == 0:
			if err := flush(); err != nil {
				return answered, err
			}
		case bytes.HasPrefix(line, []byte("data:")):
			data = append(data, bytes.TrimPrefix(bytes.TrimPrefix(line, []byte("data:")), []byte(" ")))
		}
	}
	if err := flush(); err != nil {
		return answered, err
	}
	if err := s.Err(); err != nil {
		return answered, fmt.Errorf("%s: reading the event stream: %w", b.Endpoint, err)
	}
	return answered, nil
}

// answer is the part of a server message writeAnswer looks at.
type answer struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Result struct {
		ProtocolVersion string `json:"protocolVersion"`
	} `json:"result"`
}

// writeAnswer writes a JSON-RPC message (or batch) on one line and remembers
// the protocol version the server chose in its initialize result. answered
// tells whether it carries the answer to the request with id key.
func (b *Bridge) writeAnswer(body []byte, isInitialize bool, key string) (answered bool, err error) {
	var line bytes.Buffer
	if err := json.Compact(&line, body); err != nil {
		return false, fmt.Errorf("%s: invalid JSON in the answer: %w", b.Endpoint, err)
	}
	var msgs []answer
	if bytes.HasPrefix(line.Bytes(), []byte("[")) {
		_ = json.Unmarshal(line.Bytes(), &msgs)
	} else {
		msgs = make([]answer, 1)
		_ = json.Unmarshal(line.Bytes(), &msgs[0])
	}
	for _, m := range msgs {
		if key == "" || m.Method != "" || len(m.ID) == 0 || idKey(m.ID) != key {
			continue
		}
		answered = true
		if isInitialize && m.Result.ProtocolVersion != "" {
			b.mu.Lock()
			b.protocolVersion = m.Result.ProtocolVersion
			b.mu.Unlock()
		}
	}
	line.WriteByte('\n')
	b.writeMu.Lock()
	defer b.writeMu.Unlock()
	if _, err := b.out.Write(line.Bytes()); err != nil {
		return false, err
	}
	return answered, nil
}

func (b *Bridge) writeError(id json.RawMessage, code int, msg string) {
	var e bytes.Buffer
	enc := json.NewEncoder(&e) // ends the line
	enc.SetEscapeHTML(false)
	_ = enc.Encode(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error":   map[string]any{"code": code, "message": msg},
	})
	b.writeMu.Lock()
	defer b.writeMu.Unlock()
	_, _ = b.out.Write(e.Bytes())
}

func (b *Bridge) logf(format string, args ...any) {
	w := b.Log
	if w == nil {
		w = os.Stderr
	}
	fmt.Fprintf(w, "nova flow mcp: "+format+"\n", args...)
}
