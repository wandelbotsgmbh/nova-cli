// Package httpx provides shared HTTP client construction for the CLI so that
// TLS verification behaviour can be controlled globally (e.g. via the
// --insecure-skip-tls-verify flag).
package httpx

import (
	"crypto/tls"
	"net/http"
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

// Transport returns an http.RoundTripper honouring the global TLS setting. It
// clones http.DefaultTransport so that connection pooling defaults are kept.
func Transport() http.RoundTripper {
	dt, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return http.DefaultTransport
	}
	t := dt.Clone()
	if insecureSkipTLSVerify {
		if t.TLSClientConfig == nil {
			t.TLSClientConfig = &tls.Config{}
		}
		t.TLSClientConfig.InsecureSkipVerify = true
	}
	return t
}

// Client returns a new *http.Client honouring the global TLS setting.
func Client() *http.Client {
	return &http.Client{Transport: Transport()}
}
