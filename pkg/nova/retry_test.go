package nova

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCreateWithRetry(t *testing.T) {
	drop := errors.New(`Post "https://x/api/v2/cells": http2: server sent GOAWAY and closed the connection; LastStreamID=5`)
	answer := func(status int) (*http.Response, []byte, error) {
		return &http.Response{StatusCode: status, Status: http.StatusText(status)}, []byte("body"), nil
	}
	cases := []struct {
		name    string
		answers []func() (*http.Response, []byte, error)
		wantErr bool
		calls   int
	}{
		{"ok", []func() (*http.Response, []byte, error){func() (*http.Response, []byte, error) { return answer(201) }}, false, 1},
		{"conflict without drop fails", []func() (*http.Response, []byte, error){func() (*http.Response, []byte, error) { return answer(409) }}, true, 1},
		{"drop then conflict means created", []func() (*http.Response, []byte, error){
			func() (*http.Response, []byte, error) { return nil, nil, drop },
			func() (*http.Response, []byte, error) { return answer(409) }}, false, 2},
		{"drop then created", []func() (*http.Response, []byte, error){
			func() (*http.Response, []byte, error) { return nil, nil, drop },
			func() (*http.Response, []byte, error) { return answer(201) }}, false, 2},
		{"drop twice fails", []func() (*http.Response, []byte, error){
			func() (*http.Response, []byte, error) { return nil, nil, drop },
			func() (*http.Response, []byte, error) { return nil, nil, drop }}, true, 2},
		{"other error not retried", []func() (*http.Response, []byte, error){
			func() (*http.Response, []byte, error) { return nil, nil, errors.New("no such host") }}, true, 1},
	}
	old := resendDelay
	resendDelay = func() time.Duration { return 0 }
	t.Cleanup(func() { resendDelay = old })
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			calls, waited := 0, 0
			err := createWithRetry("cell x", func() (*http.Response, []byte, error) {
				calls++
				return c.answers[calls-1]()
			}, func() error { waited++; return nil }, "failed to create cell")
			if (err != nil) != c.wantErr || calls != c.calls {
				t.Errorf("err=%v calls=%d", err, calls)
			}
			if wantWait := c.name == "drop then conflict means created"; (waited == 1) != wantWait {
				t.Errorf("waited for the rollout %d times", waited)
			}
		})
	}
	// the rollout wait decides the result
	calls := 0
	err := createWithRetry("cell x", func() (*http.Response, []byte, error) {
		calls++
		if calls == 1 {
			return nil, nil, drop
		}
		return answer(409)
	}, func() error { return errors.New("cell x not ready") }, "failed")
	if err == nil || err.Error() != "cell x not ready" {
		t.Errorf("expected the rollout error, got %v", err)
	}
}

func TestDeletedByEarlierAttempt(t *testing.T) {
	retried := &http.Response{StatusCode: 404, Header: http.Header{"X-Nova-Cli-Retried": {"1"}}}
	first := &http.Response{StatusCode: 404, Header: http.Header{}}
	if !deletedByEarlierAttempt(retried) || deletedByEarlierAttempt(first) {
		t.Error("only a 404 to a resent DELETE counts as deleted")
	}
}

func TestWaitForCellRunning(t *testing.T) {
	old := cellStatusPoll
	cellStatusPoll = time.Millisecond
	t.Cleanup(func() { cellStatusPoll = old })
	polls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/cells/cell/status" {
			http.NotFound(w, r)
			return
		}
		polls++
		code := "Running"
		if polls < 3 {
			code = "Pending"
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"operating_state":"ACTIVE","service_status":[{"group":"CellService","service":"ApiGateway","status":{"code":"Running","severity":"INFO"}},{"group":"App","service":"myapp","status":{"code":%q,"severity":"INFO"}}]}`, code)
	}))
	defer srv.Close()
	if err := waitForCellRunning(srv.URL, "cell", "myapp"); err != nil {
		t.Fatal(err)
	}
	if polls != 3 {
		t.Errorf("expected to poll until the app runs, got %d polls", polls)
	}
}
