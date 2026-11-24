package portal

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeCreate answers the first `busy` create requests with the portal's 409 and then
// hands out an instance.
func fakeCreate(t *testing.T, busy int32) (*Portal, *atomic.Int32, *bytes.Buffer) {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		if calls.Add(1) <= busy {
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"message":"An instance creation request is already in progress. Wait for it to finish and then try again."}`))
			return
		}
		_, _ = w.Write([]byte(`{"instance_id":"abcdefgh","host":"abcdefgh.instance.wandelbots.io"}`))
	}))
	t.Cleanup(srv.Close)

	var log bytes.Buffer
	oldInterval, oldTimeout, oldLog := createRetryInterval, createRetryTimeout, createRetryLog
	createRetryInterval, createRetryTimeout, createRetryLog = time.Millisecond, 200*time.Millisecond, &log
	t.Cleanup(func() { createRetryInterval, createRetryTimeout, createRetryLog = oldInterval, oldTimeout, oldLog })

	p, err := newPortal(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	return p, &calls, &log
}

func TestCreateInstanceRetriesWhileAnotherIsInProgress(t *testing.T) {
	p, calls, log := fakeCreate(t, 2)
	inst, err := p.CreateInstance("ci")
	if err != nil {
		t.Fatal(err)
	}
	if inst.InstanceId != "abcdefgh" || calls.Load() != 3 {
		t.Errorf("instance %+v after %d requests, want abcdefgh after 3", inst, calls.Load())
	}
	if !strings.Contains(log.String(), "in progress") {
		t.Errorf("no retry note: %q", log.String())
	}

	p, calls, _ = fakeCreate(t, 1)
	if _, err := p.CreateInstanceDev("ci", nil, nil, nil); err != nil || calls.Load() != 2 {
		t.Errorf("dev create: err %v after %d requests", err, calls.Load())
	}
}

func TestCreateInstanceGivesUpAfterTimeout(t *testing.T) {
	p, _, _ := fakeCreate(t, 1<<30)
	_, err := p.CreateInstance("ci")
	if err == nil || !strings.Contains(err.Error(), "409") {
		t.Fatalf("err = %v, want the portal's 409 after the timeout", err)
	}
}
