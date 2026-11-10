package nova

import (
	"errors"
	"net/http"
	"testing"
	"time"
)

func TestCreateWithRetry(t *testing.T) {
	old := resendDelay
	resendDelay = func(int) time.Duration { return 0 }
	t.Cleanup(func() { resendDelay = old })

	drop := errors.New(`Post "https://x/api/v2/cells/cell/apps": http2: server sent GOAWAY and closed the connection; LastStreamID=7`)
	dropped := func() (*http.Response, []byte, error) { return nil, nil, drop }
	status := func(code int) func() (*http.Response, []byte, error) {
		return func() (*http.Response, []byte, error) {
			return &http.Response{StatusCode: code, Status: http.StatusText(code)}, []byte("body"), nil
		}
	}
	type answers = []func() (*http.Response, []byte, error)
	cases := []struct {
		name               string
		answers            answers
		updateErr          error
		wantErr            bool
		wantCalls, updates int
	}{
		{"created", answers{status(201)}, nil, false, 1, 0},
		{"conflict without a drop is an error", answers{status(409)}, nil, true, 1, 0},
		{"drop, then created", answers{dropped, status(201)}, nil, false, 2, 0},
		{"drop, then conflict: update waits for the rollout", answers{dropped, status(409)}, nil, false, 2, 1},
		{"several drops, then conflict", answers{dropped, dropped, dropped, status(409)}, nil, false, 4, 1},
		{"the update decides the result", answers{dropped, status(409)}, errors.New("rollout failed"), true, 2, 1},
		{"gives up after the resends", answers{dropped, dropped, dropped, dropped, dropped, dropped}, nil, true, 1 + createResends, 0},
		{"other errors are not retried", answers{func() (*http.Response, []byte, error) { return nil, nil, errors.New("no such host") }}, nil, true, 1, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			calls, updates := 0, 0
			err := createWithRetry("app x", func() (*http.Response, []byte, error) {
				calls++
				return c.answers[calls-1]()
			}, func() error { updates++; return c.updateErr }, "failed to create new app")
			if (err != nil) != c.wantErr || calls != c.wantCalls || updates != c.updates {
				t.Errorf("err=%v calls=%d updates=%d", err, calls, updates)
			}
		})
	}
}
