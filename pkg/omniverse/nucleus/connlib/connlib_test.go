package connlib

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// echoServer is a minimal WebSocket server that echoes back connlib-style responses.
func echoServer(t *testing.T) *http.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()

		ctx := r.Context()
		for {
			_, data, err := conn.Read(ctx)
			if err != nil {
				return
			}
			var req struct {
				ID      uint32 `json:"id"`
				Command string `json:"command"`
			}
			json.Unmarshal(data, &req)

			resp, _ := json.Marshal(map[string]any{
				"id":      req.ID,
				"status":  "OK",
				"command": req.Command,
			})
			conn.Write(ctx, websocket.MessageBinary, resp)
		}
	})
	return &http.Server{Handler: mux}
}

func setupTestConn(t *testing.T) (*Conn, func()) {
	t.Helper()

	// Use httptest-style server
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()

		ctx := r.Context()
		for {
			_, data, err := conn.Read(ctx)
			if err != nil {
				return
			}
			var req struct {
				ID      uint32 `json:"id"`
				Command string `json:"command"`
			}
			json.Unmarshal(data, &req)

			if req.Command == "stream" {
				// Send two batches + DONE
				for _, val := range []string{"a", "b"} {
					r, _ := json.Marshal(map[string]any{
						"id":     req.ID,
						"status": "OK",
						"value":  val,
					})
					conn.Write(ctx, websocket.MessageBinary, r)
				}
				done, _ := json.Marshal(map[string]any{
					"id":     req.ID,
					"status": "DONE",
				})
				conn.Write(ctx, websocket.MessageBinary, done)
			} else {
				resp, _ := json.Marshal(map[string]any{
					"id":      req.ID,
					"status":  "OK",
					"command": req.Command,
				})
				conn.Write(ctx, websocket.MessageBinary, resp)
			}
		}
	})

	srv := &http.Server{Handler: mux}
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(ln)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	url := "ws://" + ln.Addr().String() + "/ws"
	ws, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		srv.Close()
		t.Fatalf("dial: %v", err)
	}

	conn := NewConn(ws)
	go conn.StartReadLoop(context.Background())

	cleanup := func() {
		conn.Close()
		srv.Close()
	}
	return conn, cleanup
}

func TestCall(t *testing.T) {
	conn, cleanup := setupTestConn(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := conn.Call(ctx, "ping", map[string]any{"data": "hello"})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}

	var result struct {
		Status  string `json:"status"`
		Command string `json:"command"`
	}
	json.Unmarshal(resp, &result)
	if result.Status != "OK" {
		t.Errorf("status = %q, want OK", result.Status)
	}
	if result.Command != "ping" {
		t.Errorf("command = %q, want ping", result.Command)
	}
}

func TestCallStream(t *testing.T) {
	conn, cleanup := setupTestConn(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	responses, err := conn.CallStream(ctx, "stream", map[string]any{})
	if err != nil {
		t.Fatalf("CallStream: %v", err)
	}
	if len(responses) != 2 {
		t.Fatalf("got %d responses, want 2", len(responses))
	}

	var r struct {
		Value string `json:"value"`
	}
	json.Unmarshal(responses[0], &r)
	if r.Value != "a" {
		t.Errorf("responses[0].value = %q, want a", r.Value)
	}
	json.Unmarshal(responses[1], &r)
	if r.Value != "b" {
		t.Errorf("responses[1].value = %q, want b", r.Value)
	}
}

func TestNextID(t *testing.T) {
	conn := &Conn{pending: make(map[uint32]chan json.RawMessage)}
	id1 := conn.NextID()
	id2 := conn.NextID()
	if id2 != id1+1 {
		t.Errorf("ids not sequential: %d, %d", id1, id2)
	}
}
