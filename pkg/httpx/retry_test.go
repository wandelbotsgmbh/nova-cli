package httpx

import (
	"io"
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
	old := retryDelays
	retryDelays = []time.Duration{time.Millisecond, time.Millisecond}
	t.Cleanup(func() { retryDelays = old })
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
