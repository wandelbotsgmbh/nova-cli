// Package connlib implements the connlib JSON-over-WebSocket protocol
// used by the Nucleus API service.
//
// Messages are JSON objects with "id" and "command" fields. Binary payloads
// are appended after a null byte separator. Responses are matched to requests
// by their "id" field. Streaming responses end with a message where status="DONE".
package connlib

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/coder/websocket"
)

// RawFrame holds a connlib response that may include binary data after the JSON part.
type RawFrame struct {
	JSON   json.RawMessage
	Binary []byte // nil if no binary payload
}

// ErrClosed is returned (possibly wrapped) by Send, Call and CallStream once
// the underlying WebSocket has died, and by callers that find their response
// channel closed for that reason. Callers may re-dial and retry.
var ErrClosed = errors.New("connlib: connection closed")

// Conn is a connlib protocol connection over a WebSocket.
type Conn struct {
	ws            *websocket.Conn
	mu            sync.Mutex
	requestID     atomic.Uint32
	pending       map[uint32]chan json.RawMessage
	pendingBinary map[uint32]chan RawFrame
	closed        atomic.Bool // set once the read loop has exited
}

// Closed reports whether the connection is dead (read loop exited or a
// write failed). A closed Conn never recovers; dial a new one.
func (c *Conn) Closed() bool {
	return c.closed.Load()
}

// write sends a WebSocket message, translating failures into ErrClosed
// (unless the caller's context is what ended the write).
func (c *Conn) write(ctx context.Context, data []byte) error {
	if c.closed.Load() {
		return ErrClosed
	}
	if err := c.ws.Write(ctx, websocket.MessageBinary, data); err != nil {
		if ctx.Err() != nil {
			return err
		}
		c.closed.Store(true)
		return fmt.Errorf("%w: %v", ErrClosed, err)
	}
	return nil
}

// NewConn wraps an existing WebSocket connection with the connlib protocol.
// Call StartReadLoop to begin dispatching responses.
func NewConn(ws *websocket.Conn) *Conn {
	return &Conn{
		ws:            ws,
		pending:       make(map[uint32]chan json.RawMessage),
		pendingBinary: make(map[uint32]chan RawFrame),
	}
}

// StartReadLoop runs the background reader that dispatches responses to
// waiting callers. It blocks until the connection is closed or errors.
// Typically called as: go conn.StartReadLoop(ctx)
func (c *Conn) StartReadLoop(ctx context.Context) {
	for {
		_, data, err := c.ws.Read(ctx)
		if err != nil {
			// Mark closed before waking waiters so they can tell a dead
			// connection from a normal end of stream.
			c.closed.Store(true)
			c.mu.Lock()
			for _, ch := range c.pending {
				close(ch)
			}
			c.pending = make(map[uint32]chan json.RawMessage)
			for _, ch := range c.pendingBinary {
				close(ch)
			}
			c.pendingBinary = make(map[uint32]chan RawFrame)
			c.mu.Unlock()
			return
		}

		// Split on null byte (connlib framing)
		jsonPart := data
		var binaryPart []byte
		if idx := bytes.IndexByte(data, 0); idx >= 0 {
			jsonPart = data[:idx]
			if idx+1 < len(data) {
				binaryPart = data[idx+1:]
			}
		}

		var header struct {
			ID uint32 `json:"id"`
		}
		if err := json.Unmarshal(jsonPart, &header); err != nil {
			continue
		}

		// Check status for DONE handling
		var status struct {
			Status string `json:"status"`
		}
		_ = json.Unmarshal(jsonPart, &status)

		c.mu.Lock()
		ch, ok := c.pending[header.ID]
		bCh, bOk := c.pendingBinary[header.ID]
		c.mu.Unlock()

		if bOk {
			select {
			case bCh <- RawFrame{JSON: json.RawMessage(jsonPart), Binary: binaryPart}:
			default:
			}
			if status.Status == "DONE" {
				c.mu.Lock()
				if ch2, ok2 := c.pendingBinary[header.ID]; ok2 {
					close(ch2)
					delete(c.pendingBinary, header.ID)
				}
				c.mu.Unlock()
			}
		} else if ok {
			select {
			case ch <- json.RawMessage(jsonPart):
			default:
			}
			if status.Status == "DONE" {
				c.mu.Lock()
				if ch2, ok2 := c.pending[header.ID]; ok2 {
					close(ch2)
					delete(c.pending, header.ID)
				}
				c.mu.Unlock()
			}
		}
	}
}

// Call sends a connlib command and waits for the first response.
func (c *Conn) Call(ctx context.Context, command string, params map[string]any) (json.RawMessage, error) {
	id := c.NextID()

	msg := make(map[string]any, len(params)+2)
	for k, v := range params {
		msg[k] = v
	}
	msg["id"] = id
	msg["command"] = command

	data, err := json.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	ch := c.Register(id)
	defer c.Unregister(id)

	if err := c.write(ctx, data); err != nil {
		return nil, fmt.Errorf("send: %w", err)
	}

	select {
	case resp, ok := <-ch:
		if !ok {
			return nil, ErrClosed
		}
		return resp, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// CallStream sends a connlib command and collects all streamed responses
// until a DONE message is received. It also terminates early on error statuses.
func (c *Conn) CallStream(ctx context.Context, command string, params map[string]any) ([]json.RawMessage, error) {
	id := c.NextID()

	msg := make(map[string]any, len(params)+2)
	for k, v := range params {
		msg[k] = v
	}
	msg["id"] = id
	msg["command"] = command

	data, err := json.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	ch := c.RegisterBuffered(id, 32)
	defer c.Unregister(id)

	if err := c.write(ctx, data); err != nil {
		return nil, fmt.Errorf("send: %w", err)
	}

	var results []json.RawMessage
	for {
		select {
		case resp, ok := <-ch:
			if !ok {
				// Closed either after DONE (normal) or because the
				// connection died (partial results are not trustworthy).
				if c.closed.Load() {
					return results, ErrClosed
				}
				return results, nil
			}
			var status struct {
				Status string `json:"status"`
			}
			_ = json.Unmarshal(resp, &status)
			if status.Status == "DONE" {
				return results, nil
			}
			// Terminate early on error statuses (non-OK, non-DONE, non-empty).
			if status.Status != "" && status.Status != "OK" {
				results = append(results, resp)
				return results, nil
			}
			results = append(results, resp)
		case <-ctx.Done():
			return results, ctx.Err()
		}
	}
}

// NextID returns the next unique request ID.
func (c *Conn) NextID() uint32 {
	return c.requestID.Add(1)
}

// Register creates a response channel for the given request ID (buffer size 1).
func (c *Conn) Register(id uint32) chan json.RawMessage {
	return c.RegisterBuffered(id, 1)
}

// RegisterBuffered creates a response channel with the given buffer size.
func (c *Conn) RegisterBuffered(id uint32, bufSize int) chan json.RawMessage {
	ch := make(chan json.RawMessage, bufSize)
	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()
	return ch
}

// Unregister removes the response channel for the given request ID.
func (c *Conn) Unregister(id uint32) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

// RegisterBinary creates a binary-aware response channel for the given request ID.
// Responses on this channel include both the JSON and any binary payload.
func (c *Conn) RegisterBinary(id uint32, bufSize int) chan RawFrame {
	ch := make(chan RawFrame, bufSize)
	c.mu.Lock()
	c.pendingBinary[id] = ch
	c.mu.Unlock()
	return ch
}

// UnregisterBinary removes the binary response channel for the given request ID.
func (c *Conn) UnregisterBinary(id uint32) {
	c.mu.Lock()
	delete(c.pendingBinary, id)
	c.mu.Unlock()
}

// Send writes raw bytes to the underlying WebSocket as a binary message.
// It returns an error wrapping ErrClosed once the connection is dead.
func (c *Conn) Send(ctx context.Context, data []byte) error {
	return c.write(ctx, data)
}

// Close cleanly shuts down the WebSocket connection.
func (c *Conn) Close() error {
	return c.ws.Close(websocket.StatusNormalClosure, "bye")
}
