package nova

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
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

// statusServer replays cell status answers (the last one repeats) and serves the cell
// configuration with the given controllers and apps.
func statusServer(t *testing.T, config string, answers ...string) (string, *int) {
	t.Helper()
	old := cellStatusPoll
	cellStatusPoll = time.Millisecond
	t.Cleanup(func() { cellStatusPoll = old })
	polls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v2/cells/cell":
			fmt.Fprint(w, config)
		case "/api/v2/cells/cell/status":
			answer := answers[min(polls, len(answers)-1)]
			polls++
			fmt.Fprintf(w, `{"operating_state":"ACTIVE","service_status":[%s]}`, answer)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL, &polls
}

func svc(name, code string) string {
	return fmt.Sprintf(`{"group":"CellService","service":%q,"status":{"code":%q,"severity":"INFO"}}`, name, code)
}

func list(s ...string) string { return strings.Join(s, ",") }

// The status of a rolling-out cell lists its services gradually (measured on 26.8:
// none, one, then all); a partial list where the listed ones run must not count.
func TestWaitForCellRunning_PartialListIsNotReady(t *testing.T) {
	full := list(svc("ApiGateway", "Running"), svc("Storage", "Running"), svc("robot", "Running"), svc("myapp", "Running"))
	url, polls := statusServer(t, `{"name":"cell","controllers":[{"name":"robot","configuration":{"kind":"VirtualController","manufacturer":"universalrobots"}}]}`,
		"",                                 // just accepted
		list(svc("ApiGateway", "Running")), // partial, but everything listed runs
		list(svc("ApiGateway", "Running"), svc("Storage", "Pending"), svc("robot", "Pending")),
		list(svc("ApiGateway", "Running"), svc("Storage", "Running"), svc("robot", "Running")), // app missing
		full)
	if err := waitForCellRunning(url, "cell", "myapp"); err != nil {
		t.Fatal(err)
	}
	// ready only from the 5th answer on, and then stablePolls times in a row
	if want := 4 + stablePolls; *polls != want {
		t.Errorf("returned after %d polls, want %d", *polls, want)
	}
}

func TestWaitForCellRunning_ExpectedControllerMissing(t *testing.T) {
	url, polls := statusServer(t, `{"name":"cell","controllers":[{"name":"robot","configuration":{"kind":"VirtualController","manufacturer":"universalrobots"}}]}`,
		list(svc("ApiGateway", "Running")),
		list(svc("ApiGateway", "Running")),
		list(svc("ApiGateway", "Running")),
		list(svc("ApiGateway", "Running"), svc("robot", "Running")))
	if err := waitForCellRunning(url, "cell", ""); err != nil {
		t.Fatal(err)
	}
	if want := 3 + stablePolls; *polls != want {
		t.Errorf("a configured controller must be listed before the cell counts as ready: %d polls, want %d", *polls, want)
	}
}
