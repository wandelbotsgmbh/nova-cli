package cmd

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wandelbotsgmbh/nova-cli/pkg/debug"
)

const apiTestSpec = `openapi: 3.1.0
info: {title: NOVA, version: 2.8.0}
paths:
  /cells/{cell}/controllers/{controller}:
    get:
      operationId: getRobotController
      summary: Robot Controller
      responses: {'200': {description: ok}}
  /cells/{cell}/controllers/{controller}/ios/values:
    put:
      operationId: setOutputValues
      summary: Set Output Values
      responses: {'200': {description: ok}}
  /cells/{cell}/controllers/{controller}/state-stream:
    get:
      operationId: streamRobotControllerState
      x-serverstream: true
      responses: {'200': {description: ok}}
  /cells/{cell}/store/objects/{key}:
    put:
      operationId: storeObject
      summary: Store Object
      responses: {'200': {description: ok}}
  /system/diagnosis-package/zip:
    get:
      operationId: getDiagnosePackage
      responses: {'200': {description: ok}}
`

// apiInstance is a fake instance for `nova api`; it records the requests it got
// besides the spec and version lookups.
type apiInstance struct {
	// hangVersion makes /api/v2/system/version hang until the test ends.
	hangVersion atomic.Bool
	mu          sync.Mutex
	requests    []string
	bodies      []string
}

func (a *apiInstance) got() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.requests...)
}

func withAPIInstance(t *testing.T) *apiInstance {
	t.Helper()
	a := &apiInstance{}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/system/version":
			if a.hangVersion.Load() {
				<-r.Context().Done()
				return
			}
			_, _ = io.WriteString(w, "26.8.0")
			return
		case "/api/v2/ui/public.openapi.yaml":
			_, _ = io.WriteString(w, apiTestSpec)
			return
		}
		body, _ := io.ReadAll(r.Body)
		a.mu.Lock()
		a.requests = append(a.requests, r.Method+" "+r.URL.RequestURI())
		a.bodies = append(a.bodies, string(body))
		a.mu.Unlock()
		switch r.URL.Path {
		case "/api/v2/cells/cell/controllers/sim":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"name":"sim","configuration":{"kind":"VirtualController"}}`)
		case "/api/v2/cells/cell/controllers/kuka":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"name":"kuka","configuration":{"kind":"KukaController"}}`)
		case "/api/v2/cells/cell/controllers/sim/ios/values", "/api/v2/cells/cell/store/objects/k":
			w.WriteHeader(http.StatusOK)
		case "/api/v2/system/diagnosis-package/zip":
			w.Header().Set("Content-Type", "application/zip")
			_, _ = w.Write([]byte("PK\x03\x04\x00zip"))
		case "/api/v2/long-401":
			// the JSON page echoes the uri: long, and here sent in two pieces
			page := `{"error": true, "code": 401, "message": "Unauthorized", "details": {"original_uri": "` +
				strings.Repeat("x", 2000) + `", "ingress_name": "api-gateway"}}`
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, page[:100])
			w.(http.Flusher).Flush()
			time.Sleep(50 * time.Millisecond)
			_, _ = io.WriteString(w, page[100:])
		case "/api/v2/moved":
			http.Redirect(w, r, "/api/v2/cells/cell/controllers/kuka", http.StatusTemporaryRedirect)
		case "/api/v2/elsewhere":
			http.Redirect(w, r, "https://other.example/x", http.StatusTemporaryRedirect)
		case "/api/v2/slow-body":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			time.Sleep(300 * time.Millisecond)
			_, _ = io.WriteString(w, `{"late":true}`)
		case "/api/v2/cells/cell/store/objects/looks-like-error":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"error":true,"code":401,"message":"stored by a user"}`)
		case "/api/v2/slow":
			time.Sleep(300 * time.Millisecond)
			_, _ = io.WriteString(w, "late")
		case "/expired":
			_, _ = io.WriteString(w, "Error 401: Unauthorized")
		default:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"message":"not found"}`)
		}
	}))
	t.Cleanup(s.Close)
	withConfig(t, `current-instance: lab
instances:
  - name: lab
    kind: host
    host: `+s.URL+`
`)
	t.Setenv(debug.EnvVar, "")
	t.Cleanup(func() { debug.Enable(false) })
	return a
}

func TestAPIPrintsTheAnswerOnStdout(t *testing.T) {
	a := withAPIInstance(t)
	stdout, stderr, err := runAsProcess(t, "api", "/api/v2/cells/{cell}/controllers/kuka")
	if err != nil {
		t.Fatalf("%v\n%s", err, stderr)
	}
	if stdout != `{"name":"kuka","configuration":{"kind":"KukaController"}}` || stderr != "" {
		t.Errorf("stdout %q, stderr %q", stdout, stderr)
	}
	if got := a.got(); len(got) != 1 || got[0] != "GET /api/v2/cells/cell/controllers/kuka" {
		t.Errorf("requests %v", got)
	}
	cached, _ := filepath.Glob(filepath.Join(os.Getenv("HOME"), ".nova", "cache", "api", "*", "26.8.0.json"))
	if len(cached) != 1 {
		t.Errorf("spec not cached: %v", cached)
	}
}

func TestAPIHTTPErrorPrintsBodyAndFails(t *testing.T) {
	withAPIInstance(t)
	stdout, stderr, err := runAsProcess(t, "api", "/api/v2/cells/{cell}/nothing")
	if err == nil || !strings.Contains(stderr, "HTTP 404: not found") {
		t.Fatalf("err %v, stderr %q", err, stderr)
	}
	if stdout != `{"message":"not found"}` {
		t.Errorf("stdout %q", stdout)
	}
}

// A refused request is never sent, exits with exitRefused and carries a typed
// error on stderr (ADR-0010); stdout stays empty.
func TestAPIRefusesStreams(t *testing.T) {
	a := withAPIInstance(t)
	stdout, stderr, err := runAsProcess(t, "api", "/api/v2/cells/{cell}/controllers/sim/state-stream")
	var typed *typedError
	if !errors.As(err, &typed) || typed.Code != "STREAM_NOT_SUPPORTED" || typed.exit != exitRefused {
		t.Fatalf("err %v", err)
	}
	if stdout != "" {
		t.Errorf("stdout %q", stdout)
	}
	var line struct {
		Error struct{ Code, Hint string } `json:"error"`
	}
	lines := strings.Split(strings.TrimSpace(stderr), "\n")
	if json.Unmarshal([]byte(lines[len(lines)-1]), &line) != nil || line.Error.Code != "STREAM_NOT_SUPPORTED" || line.Error.Hint == "" {
		t.Errorf("no typed error on stderr: %q", stderr)
	}
	if got := a.got(); len(got) != 0 {
		t.Errorf("requests sent: %v", got)
	}
}

// nova api has no guard rails: writes go out as they are, without lookups.
func TestAPISendsWritesUnchecked(t *testing.T) {
	a := withAPIInstance(t)
	_, stderr, err := runAsProcess(t, "api", "/api/v2/cells/{cell}/controllers/kuka/ios/values", "-X", "PUT", "-F", "io=true")
	if err == nil {
		t.Fatalf("the fake answers 404 for kuka outputs; stderr %q", stderr)
	}
	got := a.got()
	if len(got) != 1 || got[0] != "PUT /api/v2/cells/cell/controllers/kuka/ios/values" || a.bodies[0] != `{"io":true}` {
		t.Errorf("requests %v, bodies %v", got, a.bodies)
	}
}

func TestAPIDryRunSendsNothing(t *testing.T) {
	a := withAPIInstance(t)
	stdout, stderr, err := runAsProcess(t, "api", "/api/v2/cells/{cell}/store/objects/k", "-X", "PUT", "-F", "n=1", "-f", "s=x", "--dry-run")
	if err != nil {
		t.Fatalf("%v\n%s", err, stderr)
	}
	var r struct {
		Method, URL, Auth string
		Operation         struct{ OperationID string }
		Body              map[string]any
	}
	if err := json.Unmarshal([]byte(stdout), &r); err != nil {
		t.Fatalf("%v: %q", err, stdout)
	}
	if r.Method != "PUT" || !strings.HasSuffix(r.URL, "/api/v2/cells/cell/store/objects/k") || r.Operation.OperationID != "storeObject" || r.Body["n"] != float64(1) || r.Body["s"] != "x" || r.Auth != "none" {
		t.Errorf("dry run %+v", r)
	}
	if got := a.got(); len(got) != 0 {
		t.Errorf("requests sent: %v", got)
	}
}

func TestAPIBinaryNeedsOutput(t *testing.T) {
	withAPIInstance(t)
	stdout, _, err := runAsProcess(t, "api", "/api/v2/system/diagnosis-package/zip")
	if err == nil || !strings.Contains(err.Error(), "--output") || stdout != "" {
		t.Fatalf("err %v, stdout %q", err, stdout)
	}
	file := filepath.Join(t.TempDir(), "d.zip")
	stdout, _, err = runAsProcess(t, "api", "/api/v2/system/diagnosis-package/zip", "--output", file)
	if err != nil || stdout != "" {
		t.Fatalf("err %v, stdout %q", err, stdout)
	}
	if b, _ := os.ReadFile(file); string(b) != "PK\x03\x04\x00zip" {
		t.Errorf("file %q", b)
	}
}

func TestAPIUnauthorizedPage(t *testing.T) {
	withAPIInstance(t)
	_, _, err := runAsProcess(t, "api", "/expired")
	var typed *typedError
	if !errors.As(err, &typed) || typed.Code != "UNAUTHORIZED" {
		t.Fatalf("want UNAUTHORIZED, got %v", err)
	}
}

func TestAPIPathArguments(t *testing.T) {
	withAPIInstance(t)
	for _, args := range [][]string{
		{"api", "cells"},
		{"api", "/api/v2/cells/{cell}/controllers/{controller}"},
		{"api", "//evil.example/x"},
	} {
		if _, _, err := runAsProcess(t, args...); err == nil {
			t.Errorf("%v: want an error", args)
		}
	}
}

// Long polls answer late: --timeout limits the wait for the headers, 0 lifts it.
func TestAPITimeout(t *testing.T) {
	withAPIInstance(t)
	if _, _, err := runAsProcess(t, "api", "/api/v2/slow", "--timeout", "50ms"); err == nil || !strings.Contains(err.Error(), "--timeout") {
		t.Fatalf("want a timeout, got %v", err)
	}
	stdout, _, err := runAsProcess(t, "api", "/api/v2/slow", "--timeout", "0")
	if err != nil || stdout != "late" {
		t.Fatalf("err %v, stdout %q", err, stdout)
	}
}

// A hanging instance must not block the spec lookup forever: it is bounded by
// --timeout, then the request is sent unchecked (here: only printed).
func TestAPISpecLookupIsBounded(t *testing.T) {
	a := withAPIInstance(t)
	a.hangVersion.Store(true)
	done := make(chan error, 1)
	var stdout, stderr string
	go func() {
		var err error
		stdout, stderr, err = runAsProcess(t, "api", "/api/v2/cells/{cell}/store/objects/k", "-X", "PUT", "-f", "a=b", "--dry-run", "--timeout", "200ms")
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil || !strings.Contains(stdout, `"method": "PUT"`) || !strings.Contains(stderr, "sending the request unchecked") {
			t.Fatalf("err %v, stdout %q, stderr %q", err, stdout, stderr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("nova api hangs while the instance doesn't answer the version")
	}
}

// --timeout limits the wait for the headers, not for the body.
func TestAPITimeoutStopsAtTheHeaders(t *testing.T) {
	withAPIInstance(t)
	stdout, stderr, err := runAsProcess(t, "api", "/api/v2/slow-body", "--timeout", "100ms")
	if err != nil || stdout != `{"late":true}` {
		t.Fatalf("err %v, stdout %q, stderr %q", err, stdout, stderr)
	}
}

// Answers of a service that look like an ingress error page (but lack its
// markers) are data, not errors.
func TestAPIServiceAnswerLikeAnErrorPage(t *testing.T) {
	withAPIInstance(t)
	stdout, stderr, err := runAsProcess(t, "api", "/api/v2/cells/{cell}/store/objects/looks-like-error")
	if err != nil || !strings.Contains(stdout, "stored by a user") {
		t.Fatalf("err %v, stdout %q, stderr %q", err, stdout, stderr)
	}
}

// The JSON error page is recognized even when it is longer than the first read.
func TestAPILongErrorPage(t *testing.T) {
	withAPIInstance(t)
	stdout, _, err := runAsProcess(t, "api", "/api/v2/long-401")
	var typed *typedError
	if !errors.As(err, &typed) || typed.Code != "UNAUTHORIZED" || stdout != "" {
		t.Fatalf("err %v, stdout %q", err, stdout)
	}
}

// Redirects on the instance are followed; others are not, and the error names
// the target.
func TestAPIRedirects(t *testing.T) {
	withAPIInstance(t)
	stdout, stderr, err := runAsProcess(t, "api", "/api/v2/moved")
	if err != nil || !strings.Contains(stdout, "KukaController") {
		t.Fatalf("same-host redirect: err %v, stdout %q, stderr %q", err, stdout, stderr)
	}
	_, _, err = runAsProcess(t, "api", "/api/v2/elsewhere")
	if err == nil || !strings.Contains(err.Error(), "https://other.example/x") {
		t.Fatalf("redirect to another host: %v", err)
	}
}

// An answer without body is no binary answer (HEAD, empty 2xx).
func TestAPIEmptyAnswerWithBinaryType(t *testing.T) {
	withAPIInstance(t)
	if _, stderr, err := runAsProcess(t, "api", "/api/v2/system/diagnosis-package/zip", "-X", "HEAD"); err != nil {
		t.Fatalf("%v\n%s", err, stderr)
	}
}

// -f and -F keep the order of the command line.
func TestAPIFieldOrder(t *testing.T) {
	withAPIInstance(t)
	stdout, stderr, err := runAsProcess(t, "api", "/x", "-X", "PUT", "--dry-run",
		"-f", "items[][id]=1", "-F", "items[][on]=true", "-f", "items[][id]=2", "-F", "items[][on]=false", "-F", "n=1", "-f", "n=2")
	if err != nil {
		t.Fatalf("%v\n%s", err, stderr)
	}
	var r struct{ Body json.RawMessage }
	if err := json.Unmarshal([]byte(stdout), &r); err != nil {
		t.Fatal(err)
	}
	var body any
	_ = json.Unmarshal(r.Body, &body)
	got, _ := json.Marshal(body)
	if string(got) != `{"items":[{"id":"1","on":true},{"id":"2","on":false}],"n":"2"}` {
		t.Errorf("body %s", got)
	}
}

func TestAPISearchTakesTimeout(t *testing.T) {
	withAPIInstance(t)
	if _, stderr, err := runAsProcess(t, "api", "search", "controller", "--timeout", "5s"); err != nil {
		t.Fatalf("%v\n%s", err, stderr)
	}
}
