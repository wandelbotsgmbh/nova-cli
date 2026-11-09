package httpx

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"syscall"
	"time"
)

// RetriedHeader is set on responses to requests that were sent more than once because
// the connection dropped. A 404 on a retried DELETE means the first attempt went through.
const RetriedHeader = "X-Nova-Cli-Retried"

// Retried reports whether resp answers a request that was resent (see RetriedHeader).
func Retried(resp *http.Response) bool {
	return resp != nil && resp.Header.Get(RetriedHeader) != ""
}

// retryDelays are the waits before the retries of a request whose connection dropped.
var retryDelays = []time.Duration{time.Second, 3 * time.Second}

// IsConnectionDrop reports whether err means the connection was closed while the
// request was in flight: an HTTP/2 GOAWAY for a stream the server had already
// started, a reset or an unexpected EOF. The instance ingress does that when it reloads
// its configuration, e.g. while a cell is deployed, so the server may well have
// received and applied the request.
func IsConnectionDrop(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EPIPE) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "GOAWAY") || strings.Contains(msg, "server closed the connection") ||
		strings.HasSuffix(msg, ": EOF")
}

// idempotent reports whether sending req again can't do more than sending it once.
func idempotent(req *http.Request) bool {
	switch req.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodPut, http.MethodDelete:
		return req.Body == nil || req.Body == http.NoBody || req.GetBody != nil
	}
	return false
}

// retryRoundTrip sends req and resends idempotent requests whose connection dropped.
// Go itself only retries requests the server never started on; a long-running PUT
// with completion_timeout is often cut off mid-way by an ingress reload.
func retryRoundTrip(send func(*http.Request) (*http.Response, error), req *http.Request) (*http.Response, error) {
	resp, err := send(req)
	if !idempotent(req) {
		return resp, err
	}
	for _, delay := range retryDelays {
		if !IsConnectionDrop(err) || req.Context().Err() != nil {
			return resp, err
		}
		fmt.Fprintf(os.Stderr, "note: the instance closed the connection during %s %s, retrying in %s\n", req.Method, req.URL.Path, delay)
		select {
		case <-time.After(delay):
		case <-req.Context().Done():
			return nil, req.Context().Err()
		}
		retry := req.Clone(req.Context())
		if req.GetBody != nil {
			body, bodyErr := req.GetBody()
			if bodyErr != nil {
				return nil, err
			}
			retry.Body = body
		}
		resp, err = send(retry)
		if resp != nil {
			resp.Header.Set(RetriedHeader, "1")
		}
	}
	return resp, err
}
