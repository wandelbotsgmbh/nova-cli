// Package httpx provides shared HTTP client construction for the CLI so that
// TLS verification behaviour can be controlled globally (e.g. via the
// --insecure-skip-tls-verify flag) and every request carries the CLI's
// User-Agent.
package httpx

import (
	"crypto/tls"
	"net/http"

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

// Transport returns an http.RoundTripper honouring the global TLS setting and
// setting the CLI's User-Agent. It clones http.DefaultTransport so that
// connection pooling defaults are kept.
func Transport() http.RoundTripper {
	dt, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return WithUserAgent(http.DefaultTransport)
	}
	t := dt.Clone()
	if insecureSkipTLSVerify {
		if t.TLSClientConfig == nil {
			t.TLSClientConfig = &tls.Config{}
		}
		t.TLSClientConfig.InsecureSkipVerify = true
	}
	return WithUserAgent(t)
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
	if req.Header.Get("User-Agent") != "" {
		return t.next.RoundTrip(req)
	}
	// a RoundTripper must not modify the caller's request
	r := req.Clone(req.Context())
	r.Header.Set("User-Agent", version.UserAgent())
	return t.next.RoundTrip(r)
}

// Client returns a new *http.Client honouring the global TLS setting.
func Client() *http.Client {
	return &http.Client{Transport: Transport()}
}
