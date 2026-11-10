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

// RetriedHeader is set on responses to requests that were sent again after a dropped
// connection (not after a refused one, which never reached the instance). A 404 on a retried DELETE means the first attempt went through.
const RetriedHeader = "X-Nova-Cli-Retried"

// Retried reports whether resp answers a request that was resent (see RetriedHeader).
func Retried(resp *http.Response) bool {
	return resp != nil && resp.Header.Get(RetriedHeader) != ""
}

// retryDelays are the waits before the retries of a dropped or refused request; the
// last one repeats until retryBudget is used up.
var retryDelays = []time.Duration{time.Second, 3 * time.Second, 5 * time.Second, 10 * time.Second}

// retryBudget limits how long a request is retried. A cell install reloads the
// instance's ingress several times in a row (each controller and service adds
// routes), each reload cutting the long-running PUT again (seen in e2e: drop, refusal,
// drop ~25 s later), so a fixed number of retries isn't enough.
var retryBudget = 3 * time.Minute

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

// FirstRetryDelay is the wait before resending a request whose connection dropped: the
// ingress is usually still reloading right after the drop.
func FirstRetryDelay() time.Duration { return retryDelays[0] }

// IsConnectionRefused reports whether err means no connection was made at all: the
// request never reached the instance, so resending is safe for every method. The
// instance's load balancer refuses connections for a moment while an ingress or app
// rolls out.
func IsConnectionRefused(err error) bool {
	return err != nil && errors.Is(err, syscall.ECONNREFUSED)
}

// rewindable reports whether req's body can be sent again.
func rewindable(req *http.Request) bool {
	return req.Body == nil || req.Body == http.NoBody || req.GetBody != nil
}

// idempotent reports whether sending req again can't do more than sending it once.
// Conditional requests (If-Match, ...) are excluded: if the dropped attempt went
// through, the resend fails its precondition and reads as someone else's change.
func idempotent(req *http.Request) bool {
	for _, h := range []string{"If-Match", "If-None-Match", "If-Unmodified-Since", "If-Modified-Since"} {
		if req.Header.Get(h) != "" {
			return false
		}
	}
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
	// only a dropped attempt may have reached the instance: then a 404 on a resent
	// DELETE means it went through (Retried); a refused one never arrived
	dropped := false
	start := time.Now()
	for attempt := 0; ; attempt++ {
		delay := retryDelays[min(attempt, len(retryDelays)-1)]
		if time.Since(start)+delay > retryBudget {
			return resp, err
		}
		if req.Context().Err() != nil || !rewindable(req) {
			return resp, err
		}
		switch {
		case IsConnectionRefused(err):
			fmt.Fprintf(os.Stderr, "note: the instance refused the connection for %s %s, retrying in %s (attempt %d)\n", req.Method, req.URL.Path, delay, attempt+2)
		case IsConnectionDrop(err) && idempotent(req):
			dropped = true
			fmt.Fprintf(os.Stderr, "note: the instance closed the connection during %s %s, retrying in %s (attempt %d)\n", req.Method, req.URL.Path, delay, attempt+2)
		default:
			return resp, err
		}
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
		if resp != nil && dropped {
			resp.Header.Set(RetriedHeader, "1")
		}
	}
}
