package novaapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
)

// RequestEditor prepares every outbound request (login, User-Agent). An error from it
// is reported to the client as UNAUTHENTICATED with the error's message, so it should
// say how to log in.
type RequestEditor func(ctx context.Context, req *http.Request) error

// Client sends requests to the NOVA v2 API of one instance and maps every failure to a
// safe *Error. Toolsets use it directly with their own small response structs: the
// structs only declare the fields a tool hands on, which keeps connection details and
// credentials out by construction.
type Client struct {
	// origin is the base URL the client was created with; requests built against it
	// (by the generated client) are redirected to the current target in edit.
	origin  string
	mu      sync.RWMutex
	current Target
	http    *http.Client
	editor  RequestEditor
	logger  *slog.Logger
}

// NewClient returns a Client for the NOVA v2 API at baseURL ("https://<host>/api/v2").
// Failure details that must not reach the MCP client are written to logger (nil means
// slog.Default).
func NewClient(baseURL string, httpClient *http.Client, editor RequestEditor, logger *slog.Logger) *Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	withIngress := *httpClient
	withIngress.Transport = ingressTransport{base: httpClient.Transport}
	if logger == nil {
		logger = slog.Default()
	}
	baseURL = strings.TrimSuffix(baseURL, "/")
	origin := baseURL
	if origin == "" {
		origin = noTargetOrigin
	}
	return &Client{origin: origin, current: Target{BaseURL: baseURL}, http: &withIngress, editor: editor, logger: logger}
}

// Target is the instance requests go to: the API root and the portal environment of its
// login ("" for the process default).
type Target struct {
	BaseURL string
	Env     string
}

type targetKey struct{}

// Pin fixes the current target for everything done with ctx: one tool call sends all
// its requests (gate checks, the write, WebSocket dials, background watchers) to the
// same instance even if use_instance switches meanwhile. A ctx that is already pinned
// keeps its target.
func (c *Client) Pin(ctx context.Context) context.Context {
	if _, ok := ctx.Value(targetKey{}).(Target); ok {
		return ctx
	}
	return context.WithValue(ctx, targetKey{}, c.Current())
}

// Detach returns a background context (not cancelled with ctx) pinned to ctx's target,
// for work that outlives a tool call, like watching a movement.
func (c *Client) Detach(ctx context.Context) context.Context {
	return context.WithValue(context.Background(), targetKey{}, c.TargetOf(ctx))
}

// TargetOf returns the target pinned in ctx, or the current one.
func (c *Client) TargetOf(ctx context.Context) Target {
	if t, ok := ctx.Value(targetKey{}).(Target); ok {
		return t
	}
	return c.Current()
}

// PinnedTarget returns the target pinned in ctx, if any (e.g. for the login environment
// in a RequestEditor).
func PinnedTarget(ctx context.Context) (Target, bool) {
	t, ok := ctx.Value(targetKey{}).(Target)
	return t, ok
}

// noTargetOrigin stands in for the base URL of a client created without a target.
const noTargetOrigin = "http://no-instance.invalid/api/v2"

// SwitchTarget points later tool calls at another instance (base URL
// "https://<host>/api/v2" and portal environment). Calls already running keep the target
// they pinned.
func (c *Client) SwitchTarget(t Target) {
	t.BaseURL = strings.TrimSuffix(t.BaseURL, "/")
	c.mu.Lock()
	defer c.mu.Unlock()
	c.current = t
}

// Current is the target new tool calls pin.
func (c *Client) Current() Target {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.current
}

// noTargetError is returned for requests while no instance is targeted.
type noTargetError struct{}

func (noTargetError) Error() string {
	return "no instance selected: call list_instances and use_instance first"
}

// BaseURL is the API root of the target pinned in ctx (or the current one), e.g.
// "https://host/api/v2". Build every URL from the ctx of the tool call.
func (c *Client) BaseURL(ctx context.Context) string { return c.TargetOf(ctx).BaseURL }

// HTTPClient is the client used for all requests (debug logging, ingress handling).
func (c *Client) HTTPClient() *http.Client { return c.http }

// Logger receives details that must not reach the MCP client.
func (c *Client) Logger() *slog.Logger { return c.logger }

// Authorize applies the request editor (login, User-Agent) to req, for requests a
// toolset builds itself (e.g. WebSocket dials). A failure is UNAUTHENTICATED.
func (c *Client) Authorize(ctx context.Context, tool string, req *http.Request) *Error {
	if err := c.edit(ctx, req); err != nil {
		return c.transportError(ctx, tool, err)
	}
	return nil
}

// authError marks a failure of the request editor (login missing or expired).
type authError struct{ err error }

func (e *authError) Error() string { return e.err.Error() }
func (e *authError) Unwrap() error { return e.err }

func (c *Client) edit(ctx context.Context, req *http.Request) error {
	base := c.BaseURL(ctx)
	if base == "" {
		return noTargetError{}
	}
	if base != c.origin {
		if rest, ok := strings.CutPrefix(req.URL.String(), c.origin); ok {
			u, err := url.Parse(base + rest)
			if err != nil {
				return err
			}
			req.URL, req.Host = u, u.Host
		}
	}
	if c.editor == nil {
		return nil
	}
	if err := c.editor(ctx, req); err != nil {
		return &authError{err}
	}
	return nil
}

// Call is one request to the API.
type Call struct {
	// Tool names the MCP tool in error messages and logs.
	Tool   string
	Method string
	// Path below the API root, e.g. "/cells/" + url.PathEscape(cell). Escape every
	// segment that comes from tool arguments.
	Path  string
	Query url.Values
	// Body is sent as JSON when not nil.
	Body any
	// NotFound is the message for a 404, built from the identifiers in the request
	// (never echo the answer). Empty means "not found".
	NotFound string
	// AcceptStatus lists non-2xx statuses that are an answer, not an error, for this
	// call (e.g. 403 with the license status in the body).
	AcceptStatus []int
}

// Result of a successful call.
type Result struct {
	Status int
	Header http.Header
	Body   []byte
}

// Do sends call and returns the answer for any 2xx status. Everything else is mapped
// to an *Error: 404 NOT_FOUND (call.NotFound), 400/422 INVALID_ARGUMENT and 403
// FORBIDDEN with the API's own message, 401 UNAUTHENTICATED, 409 ALREADY_EXISTS,
// unreachable UNAVAILABLE, anything else INTERNAL (details to the log).
func (c *Client) Do(ctx context.Context, call Call) (*Result, *Error) {
	resp, apiErr := c.send(ctx, call)
	if apiErr != nil {
		return nil, apiErr
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, c.transportError(ctx, call.Tool, err)
	}
	if apiErr := c.checkStatus(ctx, call, resp.StatusCode, raw); apiErr != nil {
		return nil, apiErr
	}
	return &Result{Status: resp.StatusCode, Header: resp.Header, Body: raw}, nil
}

// Download is Do for answers too large to hold in memory: a 2xx body is copied to w,
// at most max bytes (more is an error). It returns the number of bytes written.
func (c *Client) Download(ctx context.Context, call Call, w io.Writer, max int64) (int64, *Error) {
	resp, apiErr := c.send(ctx, call)
	if apiErr != nil {
		return 0, apiErr
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		return 0, c.checkStatus(ctx, call, resp.StatusCode, raw)
	}
	n, err := io.Copy(w, io.LimitReader(resp.Body, max+1))
	if err != nil {
		return n, c.transportError(ctx, call.Tool, err)
	}
	if n > max {
		return n, &Error{Code: CodeInternal, Message: fmt.Sprintf("%s: the answer is larger than %d MiB", call.Tool, max>>20)}
	}
	return n, nil
}

// send builds and sends the request of call.
func (c *Client) send(ctx context.Context, call Call) (*http.Response, *Error) {
	var body io.Reader
	if call.Body != nil {
		raw, err := json.Marshal(call.Body)
		if err != nil {
			return nil, c.transportError(ctx, call.Tool, err)
		}
		body = bytes.NewReader(raw)
	}
	base := c.BaseURL(ctx)
	if base == "" {
		return nil, c.transportError(ctx, call.Tool, noTargetError{})
	}
	u := base + call.Path
	if len(call.Query) > 0 {
		u += "?" + call.Query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, call.Method, u, body)
	if err != nil {
		return nil, c.transportError(ctx, call.Tool, err)
	}
	req.Header.Set("Accept", "application/json, */*")
	if call.Body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if err := c.edit(ctx, req); err != nil {
		return nil, c.transportError(ctx, call.Tool, err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, c.transportError(ctx, call.Tool, err)
	}
	return resp, nil
}

// checkStatus maps a status that is no answer for call to an *Error.
func (c *Client) checkStatus(ctx context.Context, call Call, status int, raw []byte) *Error {
	switch {
	case status >= 200 && status <= 299, slices.Contains(call.AcceptStatus, status):
		return nil
	case status == http.StatusConflict:
		return &Error{Code: CodeAlreadyExists, Message: call.Tool + ": " + apiMessage(raw, "the resource already exists or is in a conflicting state")}
	}
	return c.statusError(ctx, call.Tool, status, raw, call.NotFound)
}

// JSON sends call and decodes a 2xx JSON answer into out (may be nil to ignore the body).
// It returns the status, e.g. to tell 200 (done) from 202 (pending).
func (c *Client) JSON(ctx context.Context, call Call, out any) (int, *Error) {
	res, apiErr := c.Do(ctx, call)
	if apiErr != nil {
		return 0, apiErr
	}
	if out != nil && len(bytes.TrimSpace(res.Body)) > 0 {
		if err := json.Unmarshal(res.Body, out); err != nil {
			return 0, c.UnexpectedAnswer(ctx, call.Tool, res.Status, res.Body)
		}
	}
	return res.Status, nil
}

// Get is JSON for a GET without query.
func (c *Client) Get(ctx context.Context, tool, path, notFound string, out any) *Error {
	_, err := c.JSON(ctx, Call{Tool: tool, Method: http.MethodGet, Path: path, NotFound: notFound}, out)
	return err
}

// DeployStatus maps the status of an asynchronous write: 202 is "pending", any other
// 2xx "completed".
func DeployStatus(status int) string {
	if status == http.StatusAccepted {
		return StatusPending
	}
	return StatusCompleted
}

// UnexpectedAnswer logs an answer a tool can't use and returns INTERNAL with a pointer.
func (c *Client) UnexpectedAnswer(ctx context.Context, tool string, status int, body []byte) *Error {
	c.logger.ErrorContext(ctx, "NOVA API returned an unexpected answer",
		"tool", tool, "status", status, "body", truncate(body, 512))
	return &Error{Code: CodeInternal, Message: fmt.Sprintf(
		"%s: NOVA API returned an unexpected answer (HTTP %d), details are in the log of 'nova mcp' (stderr)", tool, status)}
}

// ControllerKind looks up the configuration discriminator ("kind") of a controller on
// the instance.
func (c *Client) ControllerKind(ctx context.Context, tool, cell, controller string) (string, *Error) {
	var robot struct {
		Configuration struct {
			Kind string `json:"kind"`
		} `json:"configuration"`
	}
	if err := c.Get(ctx, tool, "/cells/"+url.PathEscape(cell)+"/controllers/"+url.PathEscape(controller),
		fmt.Sprintf("controller '%s' not found in cell '%s'", controller, cell), &robot); err != nil {
		return "", err
	}
	return robot.Configuration.Kind, nil
}

// RequireVirtual is the physical-target gate (ADR-0009): motion, I/O writes and mode
// changes run only on virtual controllers. A physical controller, an unknown kind or a
// failed lookup is refused with APPROVAL_REQUIRED: `nova mcp` has no approval flow for
// physical motion, a human runs it through an app or the NOVA UI.
func (c *Client) RequireVirtual(ctx context.Context, tool, cell, controller string) *Error {
	kind, err := c.ControllerKind(ctx, tool, cell, controller)
	if err != nil {
		if err.Code == CodeNotFound || err.Code == CodeUnauthenticated || err.Code == CodeUnavailable {
			return err
		}
		return &Error{Code: CodeApprovalRequired, Message: fmt.Sprintf(
			"%s: could not confirm that controller '%s' is virtual; on physical controllers this needs a human through an app or the NOVA UI", tool, controller)}
	}
	if !IsVirtualKind(kind) {
		return &Error{Code: CodeApprovalRequired, Message: fmt.Sprintf(
			"%s: controller '%s' in cell '%s' is a physical %s; nova mcp only runs this on virtual controllers, a human has to do it through an app or the NOVA UI", tool, controller, cell, kind)}
	}
	return nil
}

// RequireAllVirtual applies RequireVirtual to every controller of a cell, for actions
// that may move any of them (e.g. starting a program).
func (c *Client) RequireAllVirtual(ctx context.Context, tool, cell string) *Error {
	var controllers []string
	if err := c.Get(ctx, tool, "/cells/"+url.PathEscape(cell)+"/controllers", fmt.Sprintf("cell '%s' not found", cell), &controllers); err != nil {
		return err
	}
	for _, controller := range controllers {
		if err := c.RequireVirtual(ctx, tool, cell, controller); err != nil {
			return err
		}
	}
	return nil
}

// transportError handles a failure to get any HTTP response at all: a missing login,
// DNS, connection, timeout. Running locally, the message only carries what the user
// configured (host, error text), so it is passed on: the agent can't read our stderr.
func (c *Client) transportError(ctx context.Context, tool string, err error) *Error {
	var noTarget noTargetError
	if errors.As(err, &noTarget) {
		return &Error{Code: CodeUnavailable, Message: tool + ": " + noTarget.Error()}
	}
	var auth *authError
	if errors.As(err, &auth) {
		return &Error{Code: CodeUnauthenticated, Message: tool + ": " + auth.Error()}
	}
	c.logger.ErrorContext(ctx, "NOVA API call failed", "tool", tool, "error", err)
	return &Error{Code: CodeUnavailable, Message: fmt.Sprintf("%s: cannot reach the NOVA instance: %v", tool, err)}
}

// statusError maps a non-success answer to a safe Error.
//
// 404 uses notFoundMessage, built by the caller from the identifiers it sent. 400/403
// are the NOVA API's deliberately user-facing errors ({code, message}), so their message
// is passed through. Everything else is unexpected: the status and a bounded slice of
// the body go to the log, the client gets a pointer.
func (c *Client) statusError(ctx context.Context, tool string, status int, body []byte, notFoundMessage string) *Error {
	switch status {
	case http.StatusNotFound:
		if notFoundMessage == "" {
			notFoundMessage = "not found"
		}
		return &Error{Code: CodeNotFound, Message: tool + ": " + notFoundMessage}
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return &Error{Code: CodeInvalidArgument, Message: tool + ": " + apiMessage(body, "the NOVA API rejected the request")}
	case http.StatusUnauthorized:
		return &Error{Code: CodeUnauthenticated, Message: tool + ": the instance rejected the login, run 'nova login' (cloud) or check the basic auth of the instance"}
	case http.StatusForbidden:
		return &Error{Code: CodeForbidden, Message: tool + ": " + apiMessage(body, "this operation is not allowed")}
	}

	return c.UnexpectedAnswer(ctx, tool, status, body)
}

// ingressTransport restores the status of error pages the instance ingress serves as
// HTTP 200, so a login page is never decoded as data (a 401 page once decoded as "all
// flags false" in NovaFlow).
type ingressTransport struct{ base http.RoundTripper }

func (t ingressTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	resp, err := base.RoundTrip(req)
	if err != nil || !mayBeIngressPage(req, resp) {
		return resp, err
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxIngressPage+1))
	if err != nil {
		resp.Body.Close()
		return nil, err
	}
	if len(body) > maxIngressPage {
		// not a page; hand on the read part plus the rest
		resp.Body = struct {
			io.Reader
			io.Closer
		}{io.MultiReader(bytes.NewReader(body), resp.Body), resp.Body}
		return resp, nil
	}
	resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(body))
	if status := ingressErrorStatus(resp.Header.Get("Content-Type"), body); status != 0 {
		resp.StatusCode = status
		resp.Status = fmt.Sprintf("%d %s", status, http.StatusText(status))
	}
	return resp, nil
}

// maxIngressPage is the largest body checked for an ingress error page; real pages are
// a few dozen bytes.
const maxIngressPage = 2048

// mayBeIngressPage reports whether resp could be an error page of the instance ingress:
// HTTP 200 with a text or JSON content type. Object contents (GET of a store object) are
// user data and never checked: an object may well start with "Error 404:". Writes to
// object paths are checked, so a login page never reads as "saved".
func mayBeIngressPage(req *http.Request, resp *http.Response) bool {
	if resp.StatusCode != http.StatusOK || req.Method == http.MethodGet && strings.Contains(req.URL.Path, "/store/objects/") {
		return false
	}
	ct := resp.Header.Get("Content-Type")
	return strings.HasPrefix(ct, "text/plain") || strings.HasPrefix(ct, "text/html") || strings.HasPrefix(ct, "application/json")
}

// ingressErrorStatus returns the status of an error page the instance ingress serves as
// HTTP 200 (plain text "Error 401: Unauthorized", or {"error": true, "code": 401, …}
// for clients accepting JSON), 0 for anything else.
func ingressErrorStatus(contentType string, body []byte) int {
	s := bytes.TrimSpace(body)
	if !strings.HasPrefix(contentType, "application/json") {
		if rest, ok := bytes.CutPrefix(s, []byte("Error ")); ok {
			code, _, _ := bytes.Cut(rest, []byte(":"))
			if n, err := strconv.Atoi(string(bytes.TrimSpace(code))); err == nil && n >= 400 {
				return n
			}
		}
		return 0
	}
	var page struct {
		Error bool `json:"error"`
		Code  int  `json:"code"`
	}
	if json.Unmarshal(s, &page) == nil && page.Error && page.Code >= 400 {
		return page.Code
	}
	return 0
}

// apiMessage extracts the "message" of a NOVA API error body, falling back to fallback.
func apiMessage(body []byte, fallback string) string {
	var apiErr struct {
		Message string `json:"message"`
		// validation errors of the planning and kinematics services:
		// {"detail": [{"loc": [...], "msg": "..."}]}
		Detail json.RawMessage `json:"detail"`
	}
	if err := json.Unmarshal(body, &apiErr); err != nil {
		return fallback
	}
	if apiErr.Message != "" {
		return truncate([]byte(apiErr.Message), 500)
	}
	var details []struct {
		Msg string `json:"msg"`
	}
	if json.Unmarshal(apiErr.Detail, &details) == nil {
		msgs := make([]string, 0, len(details))
		for _, d := range details {
			if d.Msg != "" {
				msgs = append(msgs, d.Msg)
			}
		}
		if len(msgs) > 0 {
			return truncate([]byte(strings.Join(msgs, "; ")), 500)
		}
	}
	var detail string
	if json.Unmarshal(apiErr.Detail, &detail) == nil && detail != "" {
		return truncate([]byte(detail), 500)
	}
	return fallback
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "…"
}
