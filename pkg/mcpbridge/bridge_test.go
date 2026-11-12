package mcpbridge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeServer answers like NovaFlow's control plane (plain JSON) and, per method,
// like other Streamable HTTP servers and the ingress.
type fakeServer struct {
	mu      sync.Mutex
	headers []http.Header
}

func (f *fakeServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.headers = append(f.headers, r.Header.Clone())
	f.mu.Unlock()
	body, _ := io.ReadAll(r.Body)
	var m struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
	}
	_ = json.Unmarshal(body, &m)
	result := func(v string) {
		w.Header().Set("Content-Type", "application/json")
		id := string(m.ID)
		if id == `"a"` {
			id = `"\u0061"` // Python re-encodes ids
		}
		_, _ = io.WriteString(w, `{"jsonrpc": "2.0",
  "id": `+id+`, "result": `+v+"}")
	}
	switch m.Method {
	case "initialize":
		w.Header().Set("Mcp-Session-Id", "s1")
		result(`{"protocolVersion": "2025-06-18"}`)
	case "notifications/initialized":
		w.WriteHeader(http.StatusAccepted)
	case "slow":
		time.Sleep(300 * time.Millisecond)
		result(`"slow"`)
	case "sse":
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/progress\"}\n\n")
		_, _ = io.WriteString(w, "data: {\"jsonrpc\":\"2.0\",\"id\":"+string(m.ID)+",\"result\":{}}\n\n")
	case "bad":
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":`+string(m.ID)+`,"error":{"code":-32602,"message":"bad params"}}`)
	case "expired":
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, "Error 401: Unauthorized")
	case "login":
		http.Redirect(w, r, "https://auth.example/oauth2/start?rd=x", http.StatusFound)
	case "accepted":
		w.WriteHeader(http.StatusAccepted)
	case "empty":
		w.Header().Set("Content-Type", "application/json")
	case "sse-noanswer":
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, ": comment\r\nevent: message\r\ndata: {\"jsonrpc\":\"2.0\",\r\ndata:  \"method\":\"notifications/progress\"}\r\n\r\n")
	case "wrong-id":
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":null,"error":{"code":-32600,"message":"invalid"}}`)
	case "moved":
		http.Redirect(w, r, "https://elsewhere.example/mcp", http.StatusPermanentRedirect)
	case "hang":
		<-r.Context().Done()
		return
	case "missing":
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, "<html>404 Not Found</html>")
	default:
		result(`{"method": "` + m.Method + `"}`)
	}
}

// logBuf collects the bridge's log of the last run.
var logBuf bytes.Buffer

func run(t *testing.T, in string, auth func(context.Context, *http.Request) error) ([]map[string]any, *fakeServer, time.Duration) {
	t.Helper()
	f := &fakeServer{}
	s := httptest.NewServer(f)
	t.Cleanup(s.Close)
	hc := *s.Client()
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	b := &Bridge{Endpoint: s.URL + "/cell/controlplane/api/v1/mcp", HTTP: &hc, Auth: auth, Hint: "is it installed?", Log: &logBuf}
	logBuf.Reset()
	var out bytes.Buffer
	start := time.Now()
	if err := b.Serve(context.Background(), strings.NewReader(in), &out); err != nil {
		t.Fatal(err)
	}
	took := time.Since(start)
	var msgs []map[string]any
	for _, line := range strings.Split(strings.TrimRight(out.String(), "\n"), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("stdout line is no JSON message: %q", line)
		}
		msgs = append(msgs, m)
	}
	return msgs, f, took
}

func byID(msgs []map[string]any) map[string]map[string]any {
	m := map[string]map[string]any{}
	for _, msg := range msgs {
		if id, ok := msg["id"]; ok {
			b, _ := json.Marshal(id)
			m[string(b)] = msg
		}
	}
	return m
}

func errMessage(msg map[string]any) string {
	e, _ := msg["error"].(map[string]any)
	s, _ := e["message"].(string)
	return s
}

func TestBridgeForwardsMessages(t *testing.T) {
	in := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}
{"jsonrpc":"2.0","method":"notifications/initialized"}

{"jsonrpc":"2.0","id":"two","method":"tools/list"}
{"jsonrpc":"2.0","id":3,"method":"sse"}
{"jsonrpc":"2.0","id":4,"method":"bad"}
`
	msgs, f, _ := run(t, in, func(_ context.Context, r *http.Request) error {
		r.Header.Set("Authorization", "Bearer fresh")
		return nil
	})
	got := byID(msgs)
	if r, _ := got["1"]["result"].(map[string]any); r["protocolVersion"] != "2025-06-18" {
		t.Errorf("initialize: %v", got["1"])
	}
	if r, _ := got[`"two"`]["result"].(map[string]any); r["method"] != "tools/list" {
		t.Errorf("tools/list: %v", got[`"two"`])
	}
	if _, ok := got["3"]["result"]; !ok {
		t.Errorf("sse answer missing: %v", msgs)
	}
	if errMessage(got["4"]) != "bad params" {
		t.Errorf("JSON-RPC error not passed on: %v", got["4"])
	}
	// the progress notification of the SSE answer, 4 answers, nothing for the notification
	if len(msgs) != 5 {
		t.Errorf("%d messages: %v", len(msgs), msgs)
	}
	for _, h := range f.headers {
		if h.Get("Authorization") != "Bearer fresh" || h.Get("Content-Type") != "application/json" {
			t.Errorf("headers %v", h)
		}
	}
}

// After initialize, requests carry the session id and the negotiated protocol
// version (Streamable HTTP).
func TestBridgeSessionHeaders(t *testing.T) {
	// sequential: initialize must be answered before the next line is read,
	// which an MCP client guarantees by waiting for the answer
	f := &fakeServer{}
	s := httptest.NewServer(f)
	defer s.Close()
	b := &Bridge{Endpoint: s.URL, HTTP: s.Client()}
	var out bytes.Buffer
	for _, msg := range []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize"}`,
		`{"jsonrpc":"2.0","id":2,"method":"ping"}`,
		`{"jsonrpc":"2.0","id":3,"method":"initialize"}`,
	} {
		if err := b.Serve(context.Background(), strings.NewReader(msg+"\n"), &out); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.headers) != 3 {
		t.Fatalf("%d requests", len(f.headers))
	}
	for _, i := range []int{0, 2} {
		if h := f.headers[i]; h.Get("Mcp-Session-Id") != "" || h.Get("MCP-Protocol-Version") != "" {
			t.Errorf("initialize sent with session headers: %v", h)
		}
	}
	last := f.headers[1]
	if last.Get("Mcp-Session-Id") != "s1" || last.Get("MCP-Protocol-Version") != "2025-06-18" {
		t.Errorf("headers %v", last)
	}
}

// Every request gets an answer, also when the endpoint fails: an MCP client
// would otherwise wait forever.
func TestBridgeAnswersFailures(t *testing.T) {
	in := `{"jsonrpc":"2.0","id":1,"method":"expired"}
{"jsonrpc":"2.0","id":2,"method":"login"}
{"jsonrpc":"2.0","id":3,"method":"missing"}
not json
`
	msgs, _, _ := run(t, in, nil)
	got := byID(msgs)
	for _, id := range []string{"1", "2"} {
		if !strings.Contains(errMessage(got[id]), "nova login") {
			t.Errorf("%s: %v", id, got[id])
		}
	}
	if m := errMessage(got["3"]); !strings.Contains(m, "HTTP 404 (is it installed?)") || strings.Contains(m, "html") {
		t.Errorf("3: %q", m)
	}
	if m := errMessage(got["null"]); !strings.Contains(m, "parse error") {
		t.Errorf("parse error: %v", msgs)
	}
	if len(msgs) != 4 {
		t.Errorf("%d messages: %v", len(msgs), msgs)
	}
}

func TestBridgeAuthFailureAnswersRequests(t *testing.T) {
	in := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}
{"jsonrpc":"2.0","method":"notifications/initialized"}
`
	msgs, f, _ := run(t, in, func(context.Context, *http.Request) error {
		return errors.New("not logged in, run 'nova login'")
	})
	if len(msgs) != 1 || !strings.Contains(errMessage(msgs[0]), "nova login") {
		t.Errorf("messages %v", msgs)
	}
	if len(f.headers) != 0 {
		t.Errorf("sent without credentials")
	}
}

// A slow tool call doesn't hold up the others.
func TestBridgeIsConcurrent(t *testing.T) {
	in := `{"jsonrpc":"2.0","id":1,"method":"slow"}
{"jsonrpc":"2.0","id":2,"method":"slow"}
{"jsonrpc":"2.0","id":3,"method":"slow"}
`
	msgs, _, took := run(t, in, nil)
	// sequential would take 900 ms
	if len(msgs) != 3 || took > 850*time.Millisecond {
		t.Errorf("%d answers in %s", len(msgs), took)
	}
	var ids []string
	for id := range byID(msgs) {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	if strings.Join(ids, ",") != "1,2,3" {
		t.Errorf("ids %v", ids)
	}
}

func TestBridgeLargeMessage(t *testing.T) {
	big := strings.Repeat("x", 1<<20)
	msgs, _, _ := run(t, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"v":"`+big+`"}}`, nil)
	if len(msgs) != 1 || msgs[0]["result"] == nil {
		t.Errorf("messages %d", len(msgs))
	}
}

// A message over the limit is answered with an error and skipped; the bridge
// keeps serving the next one.
func TestBridgeTooLargeMessage(t *testing.T) {
	old := maxMessage
	maxMessage = 1 << 10
	defer func() { maxMessage = old }()
	big := strings.Repeat("x", 1<<17) // larger than bufio's buffer too
	in := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"v":"` + big + `"}}
{"jsonrpc":"2.0","id":2,"method":"tools/list"}
`
	msgs, _, _ := run(t, in, nil)
	if len(msgs) != 2 {
		t.Fatalf("messages %d: %v", len(msgs), msgs)
	}
	got := byID(msgs)
	if got["2"]["result"] == nil {
		t.Errorf("2: %v", got["2"])
	}
	var skipped bool
	for _, m := range msgs {
		if m["id"] == nil && strings.Contains(errMessage(m), "larger than") {
			skipped = true
		}
	}
	if !skipped {
		t.Errorf("no error for the large message: %v", msgs)
	}
}

// A request the server doesn't answer still gets an answer: an MCP client would
// otherwise wait forever.
func TestBridgeAnswersEveryRequest(t *testing.T) {
	in := `{"jsonrpc":"2.0","id":1,"method":"accepted"}
{"jsonrpc":"2.0","id":2,"method":"empty"}
{"jsonrpc":"2.0","id":3,"method":"sse-noanswer"}
{"jsonrpc":"2.0","id":4,"method":"wrong-id"}
{"jsonrpc":"2.0","id":5,"method":"moved"}
`
	msgs, _, _ := run(t, in, nil)
	got := byID(msgs)
	for _, id := range []string{"1", "2", "3", "4"} {
		if !strings.Contains(errMessage(got[id]), "sent no answer") {
			t.Errorf("%s: %v", id, got[id])
		}
	}
	if m := errMessage(got["5"]); !strings.Contains(m, "HTTP 308 (redirect to https://elsewhere.example/mcp, not followed)") {
		t.Errorf("5: %q", m)
	}
	// passed on: the progress event (multi-line data, CRLF) and the id-null error
	var progress, idNull int
	for _, m := range msgs {
		if m["method"] == "notifications/progress" {
			progress++
		}
		if v, ok := m["id"]; ok && v == nil {
			idNull++
		}
	}
	if progress != 1 || idNull != 1 || len(msgs) != 7 {
		t.Errorf("%d messages (progress %d, id null %d): %v", len(msgs), progress, idNull, msgs)
	}
}

// A batch is split: every request in it gets its own answer.
func TestBridgeSplitsBatches(t *testing.T) {
	msgs, f, _ := run(t, `[{"jsonrpc":"2.0","id":1,"method":"a"},{"jsonrpc":"2.0","method":"notifications/x"},{"jsonrpc":"2.0","id":2,"method":"b"}]`+"\n", nil)
	got := byID(msgs)
	if len(msgs) != 2 || got["1"]["result"] == nil || got["2"]["result"] == nil || len(f.headers) != 3 {
		t.Errorf("messages %v, %d requests", msgs, len(f.headers))
	}
}

// notifications/cancelled cancels the request; it gets no answer (MCP).
func TestBridgeCancel(t *testing.T) {
	in := `{"jsonrpc":"2.0","id":"h","method":"hang"}
{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":"h"}}
{"jsonrpc":"2.0","id":2,"method":"ping"}
`
	done := make(chan []map[string]any)
	go func() {
		msgs, _, _ := run(t, in, nil)
		done <- msgs
	}()
	select {
	case msgs := <-done:
		if len(msgs) != 1 || byID(msgs)["2"] == nil {
			t.Errorf("messages %v", msgs)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the cancelled request is still running")
	}
}

// A failed notification can't be answered: it is logged, stdout stays clean.
func TestBridgeLogsNotificationFailures(t *testing.T) {
	msgs, _, _ := run(t, `{"jsonrpc":"2.0","method":"missing"}`+"\n", nil)
	if len(msgs) != 0 || !strings.Contains(logBuf.String(), "missing: ") || !strings.Contains(logBuf.String(), "HTTP 404") {
		t.Errorf("messages %v, log %q", msgs, logBuf.String())
	}
}

// Positional params, ids the server spells differently, and a non-string
// method: each request gets exactly one answer with its id.
func TestBridgeOddMessages(t *testing.T) {
	in := `{"jsonrpc":"2.0","id":1,"method":"x","params":[1,2]}
{"jsonrpc":"2.0","id":"a","method":"y"}
{"jsonrpc":"2.0","id":3,"method":5}
`
	msgs, _, _ := run(t, in, nil)
	if len(msgs) != 3 {
		t.Fatalf("%d messages: %v", len(msgs), msgs)
	}
	got := byID(msgs)
	if got["1"]["result"] == nil || got["3"]["error"] == nil {
		t.Errorf("messages %v", msgs)
	}
	for _, m := range msgs {
		if m["error"] != nil && m["id"] != float64(3) {
			t.Errorf("unexpected error: %v", m)
		}
	}
}
