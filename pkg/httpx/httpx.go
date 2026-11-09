// Package httpx provides shared HTTP client construction for the CLI so that
// TLS verification behaviour can be controlled globally (e.g. via the
// --insecure-skip-tls-verify flag) and every request carries the CLI's
// User-Agent.
package httpx

import (
	"crypto/tls"
	"net/http"

	"github.com/wandelbotsgmbh/nova-cli/pkg/debug"
	"github.com/wandelbotsgmbh/nova-cli/pkg/version"
)

// insecureSkipTLSVerify controls whether HTTP clients created via this package
// skip server certificate verification. It is toggled once at startup by the
// root command based on the --insecure-skip-tls-verify flag / config value.
var insecureSkipTLSVerify bool

// SetInsecureSkipTLSVerify enables or disables TLS certificate verification for
// all clients created via this package. When enabled it also patches the
// shared http.DefaultTransport so that code relying on http.DefaultClient or
// http.DefaultTransport (e.g. the oauth2 library) inherits the same behaviour.
func SetInsecureSkipTLSVerify(skip bool) {
	insecureSkipTLSVerify = skip
	if !skip {
		return
	}
	if dt, ok := http.DefaultTransport.(*http.Transport); ok {
		if dt.TLSClientConfig == nil {
			dt.TLSClientConfig = &tls.Config{}
		}
		dt.TLSClientConfig.InsecureSkipVerify = true
	}
}

// InsecureSkipTLSVerify reports whether TLS verification is currently disabled.
func InsecureSkipTLSVerify() bool {
	return insecureSkipTLSVerify
}

// Transport returns an http.RoundTripper that sets the CLI's User-Agent on top of
// http.DefaultTransport. All clients share its connection pool on purpose: a
// transport per client opens a new connection per request, and a freshly created
// instance's ingress refuses or drops new connections for a while (GOAWAY on the
// first stream, which Go can't retry for a PUT/POST). --insecure-skip-tls-verify
// patches http.DefaultTransport itself (SetInsecureSkipTLSVerify).
func Transport() http.RoundTripper {
	return WithUserAgent(http.DefaultTransport)
}

// WithUserAgent wraps rt so that requests without a User-Agent get the CLI's.
// A User-Agent set by the caller is kept.
func WithUserAgent(rt http.RoundTripper) http.RoundTripper {
	return userAgentTransport{rt}
}

type userAgentTransport struct {
	next http.RoundTripper
}

func (t userAgentTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Header.Get("User-Agent") == "" {
		// a RoundTripper must not modify the caller's request
		req = req.Clone(req.Context())
		req.Header.Set("User-Agent", version.UserAgent())
	}
	return retryRoundTrip(t.send, req)
}

func (t userAgentTransport) send(req *http.Request) (*http.Response, error) {
	// checked per request: some clients are built before the flags are parsed
	if debug.Enabled() {
		return debugRoundTrip(t.next, req)
	}
	return t.next.RoundTrip(req)
}

// Client returns a new *http.Client honouring the global TLS setting.
func Client() *http.Client {
	return &http.Client{Transport: Transport()}
}
