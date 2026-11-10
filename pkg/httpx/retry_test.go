package httpx

import (
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fastRetries shortens the retry waits for a test.
func fastRetries(t *testing.T, delay time.Duration) {
	t.Helper()
	old := retryDelays
	retryDelays = []time.Duration{delay, delay, delay, delay, delay, delay}
	t.Cleanup(func() { retryDelays = old })
}

// dropping answers like handler, but closes the connection without an answer on the
// first drops requests (after reading them), like an ingress reload mid-request.
func dropping(t *testing.T, drops int32, handler http.HandlerFunc) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) <= drops {
			_, _ = io.ReadAll(r.Body)
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			conn.Close()
			return
		}
		handler(w, r)
	}))
	t.Cleanup(srv.Close)
	fastRetries(t, time.Millisecond)
	return srv, &calls
}

// A cell install reloads the ingress several times: the PUT is resent with the same
// body until it gets through.
func TestRetry_PutResentAfterRepeatedDrops(t *testing.T) {
	var got string
	srv, calls := dropping(t, 4, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = string(b)
		w.WriteHeader(http.StatusOK)
	})
	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/cells/cell", strings.NewReader(`{"name":"cell"}`))
	resp, err := Client().Do(req)
	if err != nil {
		t.Fatalf("expected the PUT to get through after 4 drops, got %v", err)
	}
	resp.Body.Close()
	if calls.Load() != 5 || got != `{"name":"cell"}` {
		t.Errorf("calls=%d body=%q", calls.Load(), got)
	}
}

// What the instance may have applied is not sent again unless that is harmless.
func TestRetry_NotResentAfterDrop(t *testing.T) {
	cases := map[string]func() *http.Request{
		"POST": func() *http.Request {
			req, _ := http.NewRequest(http.MethodPost, "/cells", strings.NewReader(`{}`))
			return req
		},
		"DELETE": func() *http.Request {
			req, _ := http.NewRequest(http.MethodDelete, "/cells/cell", nil)
			return req
		},
		"conditional PUT": func() *http.Request {
			req, _ := http.NewRequest(http.MethodPut, "/flows/x", strings.NewReader(`{}`))
			req.Header.Set("If-Match", `"rev-1"`)
			return req
		},
	}
	for name, build := range cases {
		t.Run(name, func(t *testing.T) {
			srv, calls := dropping(t, 1, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
			req := build()
			full, _ := http.NewRequest(req.Method, srv.URL+req.URL.Path, req.Body)
			full.Header = req.Header
			if req.GetBody != nil {
				full.GetBody = req.GetBody
			}
			if _, err := Client().Do(full); err == nil || !IsConnectionDrop(err) {
				t.Fatalf("expected the drop to surface, got %v", err)
			}
			if calls.Load() != 1 {
				t.Errorf("must not be resent, got %d calls", calls.Load())
			}
		})
	}
}

func TestRetry_GivesUpAfterTheLastRetry(t *testing.T) {
	srv, calls := dropping(t, 1<<30, nil)
	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/cells/cell", strings.NewReader(`{}`))
	if _, err := Client().Do(req); err == nil || !IsConnectionDrop(err) {
		t.Fatalf("expected the drop, got %v", err)
	}
	if want := int32(len(retryDelays) + 1); calls.Load() != want {
		t.Errorf("got %d calls, want %d", calls.Load(), want)
	}
}

func TestRetry_AnsweredRequestNotResent(t *testing.T) {
	srv, calls := dropping(t, 0, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusInternalServerError) })
	req, _ := http.NewRequest(http.MethodPut, srv.URL, strings.NewReader("x"))
	resp, err := Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if calls.Load() != 1 {
		t.Errorf("an answered request must not be resent, got %d calls", calls.Load())
	}
}

// A refused connection never reached the instance: every method is resent, a POST too.
func TestRetry_RefusedConnectionResentForEveryMethod(t *testing.T) {
	fastRetries(t, 20*time.Millisecond)

	// a port nothing listens on yet; the server starts during the first retry delay
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	var got string
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = string(b)
		w.WriteHeader(http.StatusCreated)
	})}
	go func() {
		time.Sleep(5 * time.Millisecond)
		l, err := net.Listen("tcp", addr)
		if err != nil {
			return
		}
		_ = srv.Serve(l)
	}()
	t.Cleanup(func() { _ = srv.Close() })

	req, _ := http.NewRequest(http.MethodPost, "http://"+addr+"/cells/cell/apps", strings.NewReader(`{"name":"app"}`))
	resp, err := Client().Do(req)
	if err != nil {
		t.Fatalf("expected the POST to be resent after the refusal, got %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated || got != `{"name":"app"}` {
		t.Errorf("status=%d body=%q", resp.StatusCode, got)
	}
}

func TestIsConnectionDrop(t *testing.T) {
	for msg, want := range map[string]bool{
		`Put "https://x/api/v2/cells/cell": http2: server sent GOAWAY and closed the connection; LastStreamID=5, ErrCode=NO_ERROR, debug=""`: true,
		`Post "https://x/api/v2/cells/cell/apps": unexpected EOF`:                                                                            true,
		`Put "https://x": EOF`:                              true,
		`Get "https://x": dial tcp: lookup x: no such host`: false,
		`Get "https://x": context deadline exceeded`:        false,
	} {
		if got := IsConnectionDrop(errString(msg)); got != want {
			t.Errorf("IsConnectionDrop(%q) = %v, want %v", msg, got, want)
		}
	}
}

type errString string

func (e errString) Error() string { return string(e) }

// Clients with their own transport and retry policy (Nucleus downloads) and shell
// completion get the first error.
func TestRetry_OnlyTransportRetries(t *testing.T) {
	fastRetries(t, time.Millisecond)
	calls := 0
	failing := roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, io.ErrUnexpectedEOF
	})
	req, _ := http.NewRequest(http.MethodGet, "http://lft/file", nil)
	if _, err := WithUserAgent(failing).RoundTrip(req); !errors.Is(err, io.ErrUnexpectedEOF) || calls != 1 {
		t.Fatalf("WithUserAgent: expected one attempt, got %d (%v)", calls, err)
	}

	calls = 0
	retriesDisabled = true
	t.Cleanup(func() { retriesDisabled = false })
	if _, err := (userAgentTransport{next: failing, retry: true}).RoundTrip(req); err == nil || calls != 1 {
		t.Fatalf("DisableRetries: expected one attempt, got %d (%v)", calls, err)
	}
}
