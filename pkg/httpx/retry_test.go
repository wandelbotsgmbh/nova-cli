package httpx

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// dropFirst answers like handler, but closes the connection without an answer on the
// first request (after reading it), like an ingress reload mid-request.
func dropFirst(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			_, _ = io.ReadAll(r.Body)
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Fatal(err)
			}
			conn.Close()
			return
		}
		handler(w, r)
	}))
	t.Cleanup(srv.Close)
	fastRetries(t, time.Millisecond, time.Second)
	return srv, &calls
}

func TestRetry_PutResentWithSameBody(t *testing.T) {
	var got string
	srv, calls := dropFirst(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = string(b)
		w.WriteHeader(http.StatusOK)
	})
	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/cells/cell", strings.NewReader(`{"name":"cell"}`))
	resp, err := Client().Do(req)
	if err != nil {
		t.Fatalf("expected the PUT to be resent, got %v", err)
	}
	resp.Body.Close()
	if calls.Load() != 2 || got != `{"name":"cell"}` || !Retried(resp) {
		t.Errorf("calls=%d body=%q retried=%v", calls.Load(), got, Retried(resp))
	}
}

func TestRetry_PostNotResent(t *testing.T) {
	srv, calls := dropFirst(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusCreated) })
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/cells", strings.NewReader(`{}`))
	_, err := Client().Do(req)
	if err == nil || !IsConnectionDrop(err) {
		t.Fatalf("expected a connection drop error, got %v", err)
	}
	if calls.Load() != 1 {
		t.Errorf("a POST must not be resent by the transport, got %d calls", calls.Load())
	}
}

func TestRetry_DeleteNotFoundAfterRetry(t *testing.T) {
	srv, _ := dropFirst(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNotFound) })
	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/cells/cell", nil)
	resp, err := Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound || !Retried(resp) {
		t.Errorf("status=%d retried=%v", resp.StatusCode, Retried(resp))
	}
}

func TestRetry_NoRetryOnAnswer(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	req, _ := http.NewRequest(http.MethodPut, srv.URL, strings.NewReader("x"))
	resp, err := Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if calls.Load() != 1 || Retried(resp) {
		t.Errorf("an answered request must not be resent, got %d calls", calls.Load())
	}
}

func TestIsConnectionDrop(t *testing.T) {
	for msg, want := range map[string]bool{
		`Put "https://x/api/v2/cells/cell": http2: server sent GOAWAY and closed the connection; LastStreamID=5, ErrCode=NO_ERROR, debug=""`: true,
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

func TestRetry_ConditionalRequestNotResent(t *testing.T) {
	srv, calls := dropFirst(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusPreconditionFailed) })
	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/flows/x", strings.NewReader(`{}`))
	req.Header.Set("If-Match", `"rev-1"`)
	if _, err := Client().Do(req); err == nil || !IsConnectionDrop(err) {
		t.Fatalf("expected the drop to surface, got %v", err)
	}
	if calls.Load() != 1 {
		t.Errorf("a conditional PUT must not be resent, got %d calls", calls.Load())
	}
}

// A refused connection never reached the instance: every method is resent, a POST too,
// and the answer isn't marked as a resend (a 404 is a real 404).
func TestRetry_RefusedConnectionResentForEveryMethod(t *testing.T) {
	fastRetries(t, 20*time.Millisecond, time.Second)

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
	if resp.StatusCode != http.StatusCreated || got != `{"name":"app"}` || Retried(resp) {
		t.Errorf("status=%d body=%q retried=%v", resp.StatusCode, got, Retried(resp))
	}
}

// fastRetries shortens the retry waits and budget for a test.
func fastRetries(t *testing.T, delay, budget time.Duration) {
	t.Helper()
	oldDelays, oldBudget := retryDelays, retryBudget
	retryDelays, retryBudget = []time.Duration{delay}, budget
	t.Cleanup(func() { retryDelays, retryBudget = oldDelays, oldBudget })
}

// A cell install reloads the ingress several times: every drop is retried until the
// budget is used up.
func TestRetry_RepeatedDropsWithinBudget(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) <= 4 {
			_, _ = io.ReadAll(r.Body)
			conn, _, _ := w.(http.Hijacker).Hijack()
			conn.Close()
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	fastRetries(t, time.Millisecond, time.Second)
	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/cells/cell", strings.NewReader(`{}`))
	resp, err := Client().Do(req)
	if err != nil {
		t.Fatalf("expected the PUT to get through after 4 drops, got %v", err)
	}
	resp.Body.Close()
	if calls.Load() != 5 {
		t.Errorf("calls=%d, want 5", calls.Load())
	}
}

func TestRetry_GivesUpAfterBudget(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		conn, _, _ := w.(http.Hijacker).Hijack()
		conn.Close()
	}))
	defer srv.Close()
	fastRetries(t, 20*time.Millisecond, 100*time.Millisecond)
	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/cells/cell", strings.NewReader(`{}`))
	start := time.Now()
	if _, err := Client().Do(req); err == nil || !IsConnectionDrop(err) {
		t.Fatalf("expected the drop after the budget, got %v", err)
	}
	if took := time.Since(start); took > time.Second || calls.Load() < 3 {
		t.Errorf("took %s with %d calls; want a few calls within the budget", took, calls.Load())
	}
}
