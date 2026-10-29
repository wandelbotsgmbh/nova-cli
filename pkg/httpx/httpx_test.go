package httpx

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"
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

			tr, ok := Transport().(*http.Transport)
			if !ok {
				t.Fatalf("Transport() is not *http.Transport")
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
