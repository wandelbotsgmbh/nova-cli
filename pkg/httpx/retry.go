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

// The instance ingress reloads while cells and apps are deployed. A reload closes open
// connections (HTTP/2 GOAWAY, EOF), which cuts off long-running requests with
// completion_timeout, and for a moment the load balancer refuses new connections. Go
// only retries requests the server never started on, so the transport retries itself.

// retryDelays are the waits before the retries; the last one repeats.
var retryDelays = []time.Duration{time.Second, 3 * time.Second, 5 * time.Second, 10 * time.Second}

// RetryDelay is the wait before retry number attempt (0-based).
func RetryDelay(attempt int) time.Duration {
	return retryDelays[min(attempt, len(retryDelays)-1)]
}

var (
	// dropBudget limits how long a dropped request is retried. A cell install reloads
	// the ingress several times in a row (seen in e2e: drop, refusal, another drop 25 s
	// later), so a fixed number of retries isn't enough.
	dropBudget = 3 * time.Minute
	// refusedBudget is shorter: a refusal during a reload passes within seconds, and a
	// host that is really down should fail soon.
	refusedBudget = 30 * time.Second
)

// IsConnectionDrop reports whether err means the connection was closed while the
// request was in flight: an HTTP/2 GOAWAY for a stream the server had already started,
// a reset or an unexpected EOF. The server may well have received and applied the
// request.
func IsConnectionDrop(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EPIPE) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "GOAWAY") || strings.Contains(msg, "server closed the connection") ||
		strings.HasSuffix(msg, ": EOF") || strings.HasSuffix(msg, "unexpected EOF")
}

// IsConnectionRefused reports whether err means no connection was made at all: the
// request never reached the instance, so resending is safe for every method.
func IsConnectionRefused(err error) bool {
	return err != nil && errors.Is(err, syscall.ECONNREFUSED)
}

// rewindable reports whether req's body can be sent again.
func rewindable(req *http.Request) bool {
	return req.Body == nil || req.Body == http.NoBody || req.GetBody != nil
}

// resendableAfterDrop reports whether req may be sent again although the instance may
// have applied it: reads and PUT. Not POST (callers that create handle it, see
// nova.createWithRetry), not DELETE (a resend would answer 404 for a delete that
// worked), and no conditional request (If-Match, ...): if the dropped attempt went
// through, the resend fails its precondition and reads as someone else's change.
func resendableAfterDrop(req *http.Request) bool {
	for _, h := range []string{"If-Match", "If-None-Match", "If-Unmodified-Since", "If-Modified-Since"} {
		if req.Header.Get(h) != "" {
			return false
		}
	}
	switch req.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodPut:
		return true
	}
	return false
}

// retryRoundTrip sends req and resends it while the connection is refused (every
// method) or dropped (see resendableAfterDrop), within the budgets.
func retryRoundTrip(send func(*http.Request) (*http.Response, error), req *http.Request) (*http.Response, error) {
	resp, err := send(req)
	start := time.Now()
	for attempt := 0; ; attempt++ {
		if req.Context().Err() != nil || !rewindable(req) {
			return resp, err
		}
		delay := RetryDelay(attempt)
		var what string
		var budget time.Duration
		switch {
		case IsConnectionRefused(err):
			what, budget = "refused the connection for", refusedBudget
		case IsConnectionDrop(err) && resendableAfterDrop(req):
			what, budget = "closed the connection during", dropBudget
		default:
			return resp, err
		}
		if time.Since(start)+delay > budget {
			return resp, err
		}
		fmt.Fprintf(os.Stderr, "note: the instance %s %s %s, retrying in %s\n", what, req.Method, req.URL.Path, delay)
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
	}
}
