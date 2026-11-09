// Package novaflow talks to NovaFlow on a NOVA cell: the service-manager
// feature flag that deploys it, the Forest flow store, the control plane
// (globals, validation) and the cell object store flows use.
package novaflow

import (
	"bytes"
	"context"
	"encoding/json"
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

// App names (and url paths below /<cell>/) of the NovaFlow chart.
const (
	ForestApp       = "forest"
	ControlPlaneApp = "controlplane"
	EditorApp       = "flow"
	// InstallerApp is the standalone catalog installer. It deploys the same
	// chart, so it must not be combined with the feature flag on one cell.
	InstallerApp = "novaflow-installer"
)

// Client is bound to one instance and cell.
type Client struct {
	base string // instance url with protocol, no trailing slash
	cell string
	http *http.Client
}

// NewClient refreshes the login for cloud instances and returns a client for
// the given host and cell.
func NewClient(host, cell string) (*Client, error) {
	if host == "" {
		return nil, nova.ErrNoInstance
	}
	if cell == "" {
		cell = "cell"
	}
	if err := oauth.RefreshTokenOrLogin(host); err != nil {
		return nil, err
	}
	return newClient(nova.HostWithProtocol(host), cell, httpx.Client()), nil
}

func newClient(base, cell string, hc *http.Client) *Client {
	// Unauthenticated app paths redirect to the login page; following that
	// would turn "not logged in" into some unrelated answer of the login page.
	noRedirect := *hc
	noRedirect.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{base: strings.TrimSuffix(base, "/"), cell: cell, http: &noRedirect}
}

// Cell returns the cell the client is bound to.
func (c *Client) Cell() string { return c.cell }

// AppURL returns the url of a NovaFlow app on the cell, e.g. the editor.
func (c *Client) AppURL(app string) string {
	return c.base + "/" + url.PathEscape(c.cell) + "/" + app
}

// APIError is a non-2xx answer of the instance.
type APIError struct {
	Method string
	URL    string
	Status int
	Detail string
}

func (e *APIError) Error() string {
	if e.Detail == "" {
		return fmt.Sprintf("%s %s: HTTP %d", e.Method, e.URL, e.Status)
	}
	return fmt.Sprintf("%s %s: HTTP %d: %s", e.Method, e.URL, e.Status, e.Detail)
}

// IsStatus reports whether err is an APIError with the given status.
func IsStatus(err error, status int) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.Status == status
}

// ErrUnreachable means the path answered with something other than the
// service's JSON: a 404 from the ingress, or a login/error page. The ingress
// answers a missing or expired token with a plain-text "Error 401" page and
// HTTP 200, so the status code alone can't be trusted.
var ErrUnreachable = errors.New("no NovaFlow API at this path")

// ErrUnauthorized is the ingress' plain-text 401 page (served with HTTP 200).
var ErrUnauthorized = errors.New("the instance rejected the access token, run 'nova login' (or check 'nova config basic-auth')")

type request struct {
	method      string
	path        string // absolute path below the instance url, already escaped
	query       url.Values
	header      http.Header
	body        io.Reader
	contentType string
	jsonBody    any
}

type response struct {
	status int
	header http.Header
	body   []byte
}

func (c *Client) send(ctx context.Context, r request) (*response, error) {
	u := c.base + r.path
	if len(r.query) > 0 {
		u += "?" + r.query.Encode()
	}
	body := r.body
	contentType := r.contentType
	if r.jsonBody != nil {
		b, err := json.Marshal(r.jsonBody)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(b)
		contentType = "application/json"
	}
	req, err := http.NewRequestWithContext(ctx, r.method, u, body)
	if err != nil {
		return nil, err
	}
	for k, vs := range r.header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if req.Header.Get("Accept") == "" {
		req.Header.Set("Accept", "application/json")
	}
	if err := oauth.SetAuthForRequest(req); err != nil {
		return nil, err
	}
	if err := version.SetUserAgent(ctx, req); err != nil {
		return nil, err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("%s %s: reading response: %w", r.method, u, err)
	}
	if ingress.IsUnauthorizedPage(b) || ingress.IsLoginRedirect(resp) {
		return nil, ErrUnauthorized
	}
	if code, ok := ingress.ErrorPageCode(resp.Header, b); ok {
		if code == http.StatusUnauthorized {
			return nil, ErrUnauthorized
		}
		return nil, &APIError{Method: r.method, URL: u, Status: code, Detail: ingress.ErrorDetail(b)}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if resp.StatusCode == http.StatusNotFound && !ingress.IsJSON(resp.Header) {
			return nil, fmt.Errorf("%s %s: %w", r.method, u, ErrUnreachable)
		}
		return nil, &APIError{Method: r.method, URL: u, Status: resp.StatusCode, Detail: ingress.ErrorDetail(b)}
	}
	return &response{status: resp.StatusCode, header: resp.Header, body: b}, nil
}

// do sends a JSON request and decodes a JSON answer into out (if not nil).
func (c *Client) do(ctx context.Context, r request, out any) (*response, error) {
	resp, err := c.send(ctx, r)
	if err != nil {
		return nil, err
	}
	if out == nil {
		return resp, nil
	}
	if !ingress.IsJSON(resp.header) {
		return nil, fmt.Errorf("%s %s: unexpected %q answer: %w", r.method, c.base+r.path, resp.header.Get("Content-Type"), ErrUnreachable)
	}
	if err := json.Unmarshal(resp.body, out); err != nil {
		return nil, fmt.Errorf("%s %s: decoding answer: %w", r.method, c.base+r.path, err)
	}
	return resp, nil
}

func (c *Client) appPath(app, path string) string {
	return "/" + url.PathEscape(c.cell) + "/" + app + path
}
