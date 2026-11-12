package novaapi

import (
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
	// auth sets the credentials of a request; nil: the instance's login
	// (oauth.SetAuthForRequest)
	auth func(*http.Request) error
	// unauthorized replaces ErrUnauthorized's message for a bearer client
	unauthorized string
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
	// Redirects on the instance (e.g. FastAPI's trailing slash) are followed, like
	// gh api does. The login redirect and redirects to other hosts are not: the
	// first would turn "not logged in" into the login page, the second would
	// leave the instance (ADR-0006). Neither is a redirect that changes the
	// method: on 301/302/303 net/http resends a write as a GET without body, so
	// the write would report the GET's answer although nothing was written.
	h := *hc
	h.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 || ingress.Strict.IsLoginRedirect(req.Response) ||
			req.Method != via[0].Method ||
			req.URL.Scheme != u.Scheme || req.URL.Host != u.Host {
			return http.ErrUseLastResponse
		}
		return nil
	}
	return &Client{base: u, http: &h}, nil
}

// NewBearerClient returns a client for the API at base (a url that may end in a
// path, e.g. https://host:8080/omniservice/api/v2) that sends token as bearer
// token. Request paths are appended to base.
func NewBearerClient(base, token string) (*Client, error) {
	c, err := newClient(base, httpx.Client())
	if err != nil {
		return nil, err
	}
	c.auth = func(req *http.Request) error {
		req.Header.Set("Authorization", "Bearer "+token)
		return nil
	}
	c.unauthorized = "the API rejected the request as unauthenticated: the token is missing or expired"
	return c, nil
}

// UnauthorizedMessage describes ErrUnauthorized for this client's credentials.
func (c *Client) UnauthorizedMessage() string {
	if c.unauthorized != "" {
		return c.unauthorized
	}
	return ErrUnauthorized.Error()
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
	auth := c.auth
	if auth == nil {
		auth = oauth.SetAuthForRequest
	}
	if err := auth(req); err != nil {
		return nil, err
	}
	if err := version.SetUserAgent(ctx, req); err != nil {
		return nil, err
	}
	return req, nil
}

// Do sends req and checks the answer (see CheckAnswer).
func (c *Client) Do(req *http.Request) (*http.Response, error) {
	resp, err := c.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	return CheckAnswer(resp)
}

// RoundTrip sends req and returns as soon as the answer's headers are there,
// without looking at the body. Pass the answer to CheckAnswer.
func (c *Client) RoundTrip(req *http.Request) (*http.Response, error) {
	return c.http.Do(req)
}

// CheckAnswer turns an answer that means "not logged in" into ErrUnauthorized
// (and closes it). It waits for the first bytes of the body only; the body is
// wrapped so its start can be inspected again without consuming it.
func CheckAnswer(resp *http.Response) (*http.Response, error) {
	br := NewBodyReader(resp.Body)
	head := PeekStart(br, resp.Header)
	if ingress.Strict.IsUnauthorized(resp, head) {
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
	if code, ok := ingress.Strict.ErrorPageCode(resp.Header, b); ok {
		status = code
	}
	if status < 200 || status >= 300 {
		return nil, nil, &StatusError{Method: http.MethodGet, URL: req.URL.String(), Status: status, Detail: ingress.ErrorDetail(b)}
	}
	return b, resp.Header, nil
}
