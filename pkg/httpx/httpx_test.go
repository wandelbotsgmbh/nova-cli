package httpx

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wandelbotsgmbh/nova-cli/pkg/version"
)

// reset restores global TLS state so tests don't leak into each other.
// SetInsecureSkipTLSVerify mutates the shared http.DefaultTransport, so we
// snapshot and restore it.
func reset(t *testing.T) {
	t.Helper()
	prevFlag := insecureSkipTLSVerify
	var prevTLS *tls.Config
	if dt, ok := http.DefaultTransport.(*http.Transport); ok {
		prevTLS = dt.TLSClientConfig.Clone()
	}
	t.Cleanup(func() {
		insecureSkipTLSVerify = prevFlag
		if dt, ok := http.DefaultTransport.(*http.Transport); ok {
			dt.TLSClientConfig = prevTLS
		}
	})
}

func TestTransportTLSConfig(t *testing.T) {
	tests := []struct {
		name     string
		insecure bool
		want     bool
	}{
		{"verification on by default", false, false},
		{"verification skipped when enabled", true, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reset(t)
			SetInsecureSkipTLSVerify(tt.insecure)

			ua, ok := Transport().(userAgentTransport)
			if !ok {
				t.Fatalf("Transport() doesn't set the User-Agent")
			}
			tr, ok := ua.next.(*http.Transport)
			if !ok {
				t.Fatalf("Transport() doesn't wrap an *http.Transport")
			}

			got := tr.TLSClientConfig != nil && tr.TLSClientConfig.InsecureSkipVerify
			if got != tt.want {
				t.Errorf("InsecureSkipVerify = %v, want %v", got, tt.want)
			}

			if InsecureSkipTLSVerify() != tt.insecure {
				t.Errorf("InsecureSkipTLSVerify() = %v, want %v", InsecureSkipTLSVerify(), tt.insecure)
			}
		})
	}
}

// TestClientAgainstSelfSignedServer is the end-to-end behaviour: a default
// client must reject the test server's self-signed certificate, while a client
// created after enabling insecure mode must accept it.
func TestClientAgainstSelfSignedServer(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	t.Run("rejects self-signed cert by default", func(t *testing.T) {
		reset(t)
		SetInsecureSkipTLSVerify(false)

		_, err := Client().Get(srv.URL)
		if err == nil {
			t.Fatal("expected TLS verification error, got nil")
		}
	})

	t.Run("accepts self-signed cert when insecure", func(t *testing.T) {
		reset(t)
		SetInsecureSkipTLSVerify(true)

		resp, err := Client().Get(srv.URL)
		if err != nil {
			t.Fatalf("expected request to succeed, got %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
		}
	})
}

// TestSetInsecurePatchesDefaultTransport verifies that enabling insecure mode
// also patches http.DefaultTransport, so code relying on http.DefaultClient
// (e.g. the oauth2 library) inherits the behaviour.
func TestSetInsecurePatchesDefaultTransport(t *testing.T) {
	reset(t)
	SetInsecureSkipTLSVerify(true)

	dt, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		t.Fatalf("http.DefaultTransport is not *http.Transport")
	}
	if dt.TLSClientConfig == nil || !dt.TLSClientConfig.InsecureSkipVerify {
		t.Error("expected http.DefaultTransport to skip TLS verification")
	}
}

func TestClientSetsUserAgent(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Header.Get("User-Agent"))
	}))
	defer srv.Close()

	resp, err := Client().Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	req.Header.Set("User-Agent", "custom/1.0")
	resp, err = Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	want := []string{version.UserAgent(), "custom/1.0"}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("User-Agent = %q, want %q", got, want)
	}
	if req.Header.Get("User-Agent") != "custom/1.0" {
		t.Error("the caller's request must not be modified")
	}
}

func TestWithUserAgentDoesNotModifyRequest(t *testing.T) {
	var seen string
	rt := WithUserAgent(roundTripFunc(func(r *http.Request) (*http.Response, error) {
		seen = r.Header.Get("User-Agent")
		return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
	}))
	req, _ := http.NewRequest(http.MethodGet, "http://example.invalid", nil)
	if _, err := rt.RoundTrip(req); err != nil {
		t.Fatal(err)
	}
	if seen != version.UserAgent() {
		t.Errorf("User-Agent = %q, want %q", seen, version.UserAgent())
	}
	if req.Header.Get("User-Agent") != "" {
		t.Error("the caller's request must not be modified")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
