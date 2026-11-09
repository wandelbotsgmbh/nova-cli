package novaapi

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/wandelbotsgmbh/nova-cli/pkg/httpx"
	"github.com/wandelbotsgmbh/nova-cli/pkg/ingress"
	"github.com/wandelbotsgmbh/nova-cli/pkg/nova"
	"github.com/wandelbotsgmbh/nova-cli/pkg/oauth"
	"github.com/wandelbotsgmbh/nova-cli/pkg/version"
)

// ErrUnauthorized is the ingress' answer to a missing or expired login: a 401
// page (often with HTTP 200) or the redirect to the login page.
var ErrUnauthorized = errors.New("the instance rejected the request as unauthenticated: run 'nova login' (cloud instances) or check 'nova config basic-auth'")

// Client sends requests to one instance. Credentials only go to that instance
// (ADR-0006): every request url is built from its base, and a path that would
// leave the host is rejected.
type Client struct {
	base *url.URL // instance url with protocol, no trailing slash
	http *http.Client
}

// NewClient refreshes the login for cloud instances and returns a client for host.
func NewClient(host string) (*Client, error) {
	if host == "" {
		return nil, nova.ErrNoInstance
	}
	if err := oauth.RefreshTokenOrLogin(host); err != nil {
		return nil, err
	}
	return newClient(nova.HostWithProtocol(host), httpx.Client())
}

func newClient(base string, hc *http.Client) (*Client, error) {
	u, err := url.Parse(strings.TrimSuffix(base, "/"))
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("invalid instance url %q", base)
	}
	// Unauthenticated paths redirect to the login page; following that would
	// turn "not logged in" into an unrelated answer.
	noRedirect := *hc
	noRedirect.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{base: u, http: &noRedirect}, nil
}

// Host returns the instance's host (with port, if any).
func (c *Client) Host() string { return c.base.Host }

// URL returns the absolute url of path (which must start with "/" and may carry a
// query) on the instance.
func (c *Client) URL(path string) (*url.URL, error) {
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") {
		return nil, fmt.Errorf("path must start with a single /: %q", path)
	}
	u, err := url.Parse(c.base.String() + path)
	if err != nil {
		return nil, fmt.Errorf("invalid path %q: %w", path, err)
	}
	if u.Scheme != c.base.Scheme || u.Host != c.base.Host {
		return nil, fmt.Errorf("path %q leaves the instance %s", path, c.base.Host)
	}
	return u, nil
}

// NewRequest builds a request to path with the instance's credentials.
func (c *Client) NewRequest(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	u, err := c.URL(path)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return nil, err
	}
	if err := oauth.SetAuthForRequest(req); err != nil {
		return nil, err
	}
	if err := version.SetUserAgent(ctx, req); err != nil {
		return nil, err
	}
	return req, nil
}

// Do sends req. The answer's body is wrapped so its start can be inspected
// without consuming it; an answer that means "not logged in" is ErrUnauthorized.
func (c *Client) Do(req *http.Request) (*http.Response, error) {
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	br := bufio.NewReaderSize(resp.Body, 4096)
	head := PeekStart(br)
	if ingress.IsUnauthorized(resp, head) {
		resp.Body.Close()
		return nil, ErrUnauthorized
	}
	resp.Body = struct {
		io.Reader
		io.Closer
	}{br, resp.Body}
	return resp, nil
}

// StatusError is a non-2xx answer (including the ingress' error pages that
// arrive with HTTP 200).
type StatusError struct {
	Method string
	URL    string
	Status int
	Detail string
}

func (e *StatusError) Error() string {
	if e.Detail == "" {
		return fmt.Sprintf("%s %s: HTTP %d", e.Method, e.URL, e.Status)
	}
	return fmt.Sprintf("%s %s: HTTP %d: %s", e.Method, e.URL, e.Status, e.Detail)
}

// get sends a GET and returns the body of a 2xx answer, else a *StatusError.
func (c *Client) get(ctx context.Context, path, accept string) ([]byte, http.Header, error) {
	req, err := c.NewRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Accept", accept)
	resp, err := c.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("GET %s: reading response: %w", req.URL, err)
	}
	status := resp.StatusCode
	if code, ok := ingress.ErrorPageCode(resp.Header, b); ok {
		status = code
	}
	if status < 200 || status >= 300 {
		return nil, nil, &StatusError{Method: http.MethodGet, URL: req.URL.String(), Status: status, Detail: ingress.ErrorDetail(b)}
	}
	return b, resp.Header, nil
}
