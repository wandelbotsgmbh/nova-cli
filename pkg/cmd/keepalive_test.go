package cmd

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/wandelbotsgmbh/nova-cli/pkg/portal"
)

type fakeLifetime struct {
	status  portal.UserInstanceStatus
	expires time.Time
	step    time.Duration
	getErr  error
	extErr  error
	extends int
}

func (f *fakeLifetime) lifetime() (lifetimeState, error) {
	if f.getErr != nil {
		return lifetimeState{}, f.getErr
	}
	return lifetimeState{running: f.status == portal.UserInstanceStatusRunning, status: string(f.status), expires: f.expires}, nil
}

func (f *fakeLifetime) extend() error {
	if f.extErr != nil {
		return f.extErr
	}
	f.extends++
	f.expires = f.expires.Add(f.step)
	return nil
}

func newTestKeeper(f lifetimeClient, now time.Time) (*keeper, *bytes.Buffer) {
	var log bytes.Buffer
	return &keeper{
		client: f,
		name:   "cloud instance abc",
		margin: 15 * time.Minute,
		log:    &log,
		now:    func() time.Time { return now },
	}, &log
}

func mustCheck(t *testing.T, k *keeper) time.Duration {
	t.Helper()
	d, err := k.check()
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestKeeperWaitsUntilMargin(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	f := &fakeLifetime{status: portal.UserInstanceStatusRunning, expires: now.Add(18 * time.Minute), step: time.Hour}
	k, log := newTestKeeper(f, now)

	if d := mustCheck(t, k); d != 3*time.Minute || f.extends != 0 {
		t.Errorf("delay %s, extends %d; want 3m and no extension", d, f.extends)
	}
	f.expires = now.Add(3 * time.Hour)
	if d := mustCheck(t, k); d != keepAliveMaxCheck {
		t.Errorf("delay %s, want the maximum %s", d, keepAliveMaxCheck)
	}
	if log.Len() != 0 {
		t.Errorf("nothing to report, got %q", log)
	}
}

func TestKeeperExtendsWithinMargin(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	f := &fakeLifetime{status: portal.UserInstanceStatusRunning, expires: now.Add(10 * time.Minute), step: 10 * time.Minute}
	k, log := newTestKeeper(f, now)

	if d := mustCheck(t, k); f.extends != 1 || d != 5*time.Minute {
		t.Errorf("delay %s, extends %d; want 5m after one extension", d, f.extends)
	}
	if !strings.Contains(log.String(), "extended cloud instance abc until") {
		t.Errorf("unexpected log %q", log)
	}
}

func TestKeeperMarginBeyondExtension(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	f := &fakeLifetime{status: portal.UserInstanceStatusRunning, expires: now.Add(time.Hour), step: time.Hour}
	k, _ := newTestKeeper(f, now)
	k.margin = 25 * time.Hour

	if d := mustCheck(t, k); f.extends != 1 || d != keepAliveMaxCheck {
		t.Errorf("delay %s, extends %d; want %s after one extension", d, f.extends, keepAliveMaxCheck)
	}
}

func TestKeeperReportsUnmovedExpiry(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	f := &fakeLifetime{status: portal.UserInstanceStatusRunning, expires: now.Add(time.Minute)}
	k, log := newTestKeeper(f, now)

	if d := mustCheck(t, k); d != keepAliveMaxCheck {
		t.Errorf("delay %s, want %s", d, keepAliveMaxCheck)
	}
	if !strings.Contains(log.String(), "maximum lifetime") {
		t.Errorf("unexpected log %q", log)
	}
}

func TestKeeperDoesNotExtendStoppedInstance(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	f := &fakeLifetime{status: portal.UserInstanceStatusStopped, expires: now.Add(time.Minute), step: time.Hour}
	k, log := newTestKeeper(f, now)

	mustCheck(t, k)
	mustCheck(t, k)
	if f.extends != 0 {
		t.Error("a stopped instance must not be extended")
	}
	if n := strings.Count(log.String(), "is stopped"); n != 1 {
		t.Errorf("status reported %d times, want once: %q", n, log)
	}

	f.status = portal.UserInstanceStatusRunning
	mustCheck(t, k)
	if f.extends != 1 || !strings.Contains(log.String(), "running again") {
		t.Errorf("extends %d, log %q", f.extends, log)
	}
}

func TestKeeperRetriesFailures(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	f := &fakeLifetime{status: portal.UserInstanceStatusRunning, expires: now.Add(time.Minute), extErr: errors.New("boom")}
	k, log := newTestKeeper(f, now)
	if d := mustCheck(t, k); d != keepAliveMinCheck || !strings.Contains(log.String(), "boom") {
		t.Errorf("extend failure: delay %s, log %q", d, log)
	}

	f.getErr = errors.New("login expired")
	if d := mustCheck(t, k); d != keepAliveMinCheck || !strings.Contains(log.String(), "login expired") {
		t.Errorf("read failure: delay %s, log %q", d, log)
	}
}

func TestKeeperStopsWhenGone(t *testing.T) {
	k, _ := newTestKeeper(&fakeLifetime{getErr: errInstanceGone}, time.Now())
	if err := k.run(t.Context()); !errors.Is(err, errInstanceGone) {
		t.Errorf("err = %v, want errInstanceGone", err)
	}
}

func TestKeeperRunWithPassesExitCode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs sh")
	}
	now := time.Now()
	f := &fakeLifetime{status: portal.UserInstanceStatusRunning, expires: now.Add(time.Minute), step: time.Hour}
	k, _ := newTestKeeper(f, now)
	var out bytes.Buffer
	c := &cobra.Command{}
	c.SetOut(&out)

	err := k.runWith(c, []string{"sh", "-c", "echo hi; exit 3"})
	var exit exitCodeError
	if !errors.As(err, &exit) || exit.code != 3 {
		t.Fatalf("err = %v, want exit code 3", err)
	}
	if out.String() != "hi\n" || f.extends != 1 {
		t.Errorf("output %q, extends %d; want the command's output and an extension before it ran", out.String(), f.extends)
	}
	if err := k.runWith(c, []string{"true"}); err != nil {
		t.Errorf("exit code 0 must succeed, got %v", err)
	}
}

func TestK8sRunLifetime(t *testing.T) {
	var puts []string
	gone := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/instance/c1/kubeconfig":
			if gone {
				w.WriteHeader(http.StatusNotFound)
			}
		case r.Method == http.MethodPut && r.URL.Path == "/instance":
			puts = append(puts, r.URL.RawQuery)
		default:
			w.WriteHeader(http.StatusTeapot)
		}
	}))
	defer srv.Close()
	old := clusterServiceURL
	clusterServiceURL = srv.URL + "/instance"
	defer func() { clusterServiceURL = old }()

	now := time.Unix(1_800_000_000, 0)
	c := &k8srunLifetime{id: "c1", now: func() time.Time { return now }}
	k, _ := newTestKeeper(c, now)

	// the expiry is unknown at first: set it to the maximum right away
	if d := mustCheck(t, k); d != keepAliveMaxCheck || len(puts) != 1 || puts[0] != "duration=720&instance_id=c1" {
		t.Fatalf("delay %s, puts %v", d, puts)
	}
	mustCheck(t, k)
	if len(puts) != 1 {
		t.Errorf("extended again long before the margin: %v", puts)
	}

	gone = true
	if _, err := k.check(); !errors.Is(err, errInstanceGone) {
		t.Errorf("err = %v, want errInstanceGone", err)
	}
}

func TestKeepAliveArgs(t *testing.T) {
	withConfig(t, "")
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"instance", "keep-alive", "a", "b"}, "at most 1 instance name"},
		{[]string{"instance", "keep-alive", "a", "--"}, "no command after '--'"},
		{[]string{"instance", "keep-alive", "--margin", "0", "a"}, "--margin must be positive"},
	} {
		_, _, err := runWithConfig(t, tc.args...)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("nova %s: err = %v, want %q", strings.Join(tc.args, " "), err, tc.want)
		}
	}
}
