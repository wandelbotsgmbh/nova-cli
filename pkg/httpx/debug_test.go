package httpx

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wandelbotsgmbh/nova-cli/pkg/debug"
)

func captureDebug(t *testing.T) *strings.Builder {
	t.Helper()
	var log strings.Builder
	debug.Enable(true)
	debug.SetOutput(&log)
	t.Cleanup(func() {
		debug.Enable(false)
		debug.SetOutput(nil)
	})
	return &log
}

func TestDebugLogsRequestAndResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if string(body) != `{"name":"x","password":"p1"}` {
			t.Errorf("server got body %q, the logger must not consume it", body)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = io.WriteString(w, `{"error":"exists","access_token":"t2"}`)
	}))
	defer srv.Close()
	log := captureDebug(t)

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/cells?token=t3", strings.NewReader(`{"name":"x","password":"p1"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer t4")
	resp, err := Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(got) != `{"error":"exists","access_token":"t2"}` {
		t.Errorf("caller got body %q, the logger must not alter it", got)
	}

	out := log.String()
	for _, want := range []string{
		"debug: http: > POST " + srv.URL + "/cells?token=[redacted]",
		"debug: http: > Authorization: Bearer [redacted]",
		`debug: http: > {"name":"x","password":"[redacted]"}`,
		"debug: http: < 409 Conflict (POST",
		"debug: http: < Content-Type: application/json",
		`debug: http: < {"error":"exists","access_token":"[redacted]"}`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("debug output lacks %q:\n%s", want, out)
		}
	}
	for _, secret := range []string{"p1", "t2", "t3", "t4"} {
		if strings.Contains(out, `"`+secret+`"`) || strings.Contains(out, "="+secret) || strings.Contains(out, " "+secret) {
			t.Errorf("debug output leaks %q:\n%s", secret, out)
		}
	}
}

func TestDebugTruncatesLargeAndSkipsBinaryBodies(t *testing.T) {
	big := strings.Repeat("x", 3*maxLoggedBody)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/bin" {
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write([]byte{0, 1, 2, 3})
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, big)
	}))
	defer srv.Close()
	log := captureDebug(t)

	for _, path := range []string{"/text", "/bin"} {
		resp, err := Client().Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		got, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if path == "/text" && string(got) != big {
			t.Errorf("caller got %d bytes, want %d", len(got), len(big))
		}
	}

	out := log.String()
	if strings.Contains(out, strings.Repeat("x", maxLoggedBody+1)) {
		t.Error("body was not truncated")
	}
	if !strings.Contains(out, "[truncated, 12288 bytes in total]") {
		t.Errorf("truncation not marked:\n%s", out)
	}
	if !strings.Contains(out, "debug: http: < [binary body, 4 bytes read]") {
		t.Errorf("binary body not summarized:\n%s", out)
	}
}

func TestDebugLogsTransportErrors(t *testing.T) {
	fastRetries(t, time.Millisecond) // a refused connection is retried
	log := captureDebug(t)
	_, err := Client().Get("http://127.0.0.1:1/x")
	if err == nil {
		t.Skip("port 1 accepted a connection")
	}
	if !strings.Contains(log.String(), "debug: http: < GET http://127.0.0.1:1/x failed after") {
		t.Errorf("transport error not logged:\n%s", log.String())
	}
}

func TestNoDebugOutputWhenDisabled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	var log strings.Builder
	debug.SetOutput(&log)
	t.Cleanup(func() { debug.SetOutput(nil) })

	resp, err := Client().Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if log.Len() != 0 {
		t.Errorf("unexpected debug output:\n%s", log.String())
	}
}

// Secrets never reach the log in clear: a value cut off by the body limit, and a
// base64 kubeconfig as the k8srun service sends it.
func TestDebugRedactsCutOffAndKubeconfigBodies(t *testing.T) {
	secret := "SECRETSECRET" + strings.Repeat("s", 200)
	// the value of refresh_token straddles the 4 KiB cut
	prefix := `{"pad":"` + strings.Repeat("p", maxLoggedBody-40) + `","refresh_token":"`
	cutOff := prefix + secret + `"}`
	kubeconfig := `{"id":"abc","kubeconfig":"YXBpVmVyc2lvbjogdjEKdXNlcnM6Ci0gdXNlcjoKICAgIGNsaWVudC1rZXktZGF0YTogU0VDUkVU"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/kubeconfig" {
			_, _ = io.WriteString(w, kubeconfig)
			return
		}
		_, _ = io.WriteString(w, cutOff)
	}))
	defer srv.Close()
	log := captureDebug(t)

	for _, path := range []string{"/token", "/kubeconfig"} {
		resp, err := Client().Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.ReadAll(resp.Body)
		resp.Body.Close()
	}

	out := log.String()
	if len(prefix) >= maxLoggedBody || len(prefix)+len(secret) <= maxLoggedBody {
		t.Fatalf("test setup: the secret must straddle the cut (prefix %d bytes)", len(prefix))
	}
	if strings.Contains(out, "SECRET") || strings.Contains(out, "YXBpVmVyc2lvbjog") {
		t.Errorf("secret logged:\n%s", out)
	}
	if !strings.Contains(out, `"refresh_token":"[redacted]"`) || !strings.Contains(out, `"kubeconfig":"[redacted]"`) {
		t.Errorf("fields not redacted:\n%s", out)
	}
}
