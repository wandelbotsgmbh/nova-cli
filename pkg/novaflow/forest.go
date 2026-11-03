package novaflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Flow is a flow (behavior tree record) as Forest stores it.
type Flow struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Tree      json.RawMessage `json:"tree"`
	Orphans   json.RawMessage `json:"orphans,omitempty"`
	Layouts   json.RawMessage `json:"layouts,omitempty"`
	Metadata  json.RawMessage `json:"metadata,omitempty"`
	Revision  *int            `json:"revision,omitempty"`
	CreatedAt *time.Time      `json:"created_at,omitempty"`
	UpdatedAt *time.Time      `json:"updated_at,omitempty"`
}

// FlowSummary is a listed flow (without its tree).
type FlowSummary struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// CorruptedFlow is a stored flow Forest could not parse.
type CorruptedFlow struct {
	ID    string `json:"id"`
	Error string `json:"error"`
}

// FlowList is the complete flow listing of a cell.
type FlowList struct {
	Flows     []FlowSummary
	Corrupted []CorruptedFlow
}

const forestPageSize = 100

func (c *Client) forestPath(path string) string {
	return c.appPath(ForestApp, "/trees"+path)
}

// Probe checks that Forest answers on the cell. It returns ErrUnreachable
// (wrapped) when NovaFlow is not running there.
func (c *Client) Probe(ctx context.Context) error {
	var page struct {
		Total int `json:"total"`
	}
	_, err := c.do(ctx, request{method: http.MethodGet, path: c.forestPath(""), query: url.Values{"limit": {"1"}}}, &page)
	return err
}

// ListFlows returns all flows of the cell.
func (c *Client) ListFlows(ctx context.Context) (*FlowList, error) {
	var list FlowList
	seenCorrupted := map[string]bool{}
	for offset := 0; ; {
		var page struct {
			Items     []FlowSummary   `json:"items"`
			HasMore   bool            `json:"has_more"`
			Corrupted []CorruptedFlow `json:"corrupted"`
		}
		q := url.Values{"limit": {strconv.Itoa(forestPageSize)}, "offset": {strconv.Itoa(offset)}}
		if _, err := c.do(ctx, request{method: http.MethodGet, path: c.forestPath(""), query: q}, &page); err != nil {
			return nil, err
		}
		list.Flows = append(list.Flows, page.Items...)
		for _, cf := range page.Corrupted {
			if !seenCorrupted[cf.ID] {
				seenCorrupted[cf.ID] = true
				list.Corrupted = append(list.Corrupted, cf)
			}
		}
		if !page.HasMore || len(page.Items) == 0 {
			return &list, nil
		}
		offset += len(page.Items)
	}
}

// GetFlow returns a flow. Its Revision is the one to pass to UpdateFlow.
func (c *Client) GetFlow(ctx context.Context, id string) (*Flow, error) {
	var f Flow
	if _, err := c.do(ctx, request{method: http.MethodGet, path: c.forestPath("/" + url.PathEscape(id))}, &f); err != nil {
		return nil, err
	}
	return &f, nil
}

// GetFlowRaw returns a flow exactly as Forest sends it.
func (c *Client) GetFlowRaw(ctx context.Context, id string) (json.RawMessage, error) {
	var raw json.RawMessage
	if _, err := c.do(ctx, request{method: http.MethodGet, path: c.forestPath("/" + url.PathEscape(id))}, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// ErrFlowExists is returned by CreateFlow when the id is taken.
var ErrFlowExists = errors.New("flow already exists")

// ErrFlowChanged is returned by UpdateFlow when the stored flow is not at the
// expected revision any more.
var ErrFlowChanged = errors.New("the flow was changed on the instance since it was read")

// CreateFlow stores a new flow under f.ID.
func (c *Client) CreateFlow(ctx context.Context, f *Flow) error {
	body := map[string]any{"id": f.ID, "name": f.Name, "tree": f.Tree}
	addOptional(body, f)
	_, err := c.do(ctx, request{method: http.MethodPost, path: c.forestPath(""), jsonBody: body}, nil)
	if IsStatus(err, http.StatusConflict) {
		return fmt.Errorf("%s: %w", f.ID, ErrFlowExists)
	}
	return err
}

// UpdateFlow replaces name, tree, orphans, layouts and metadata of a stored
// flow. With ifRevision set, the update only succeeds while the stored flow is
// still at that revision.
func (c *Client) UpdateFlow(ctx context.Context, f *Flow, ifRevision *int) error {
	body := map[string]any{"name": f.Name, "tree": f.Tree}
	addOptional(body, f)
	h := http.Header{}
	if ifRevision != nil {
		h.Set("If-Match", strconv.Quote(strconv.Itoa(*ifRevision)))
	}
	_, err := c.do(ctx, request{method: http.MethodPut, path: c.forestPath("/" + url.PathEscape(f.ID)), header: h, jsonBody: body}, nil)
	if IsStatus(err, http.StatusPreconditionFailed) {
		return fmt.Errorf("%s: %w", f.ID, ErrFlowChanged)
	}
	return err
}

// DeleteFlow deletes a flow. With ifRevision set, it is only deleted while the
// stored flow is still at that revision.
func (c *Client) DeleteFlow(ctx context.Context, id string, ifRevision *int) error {
	h := http.Header{}
	if ifRevision != nil {
		h.Set("If-Match", strconv.Quote(strconv.Itoa(*ifRevision)))
	}
	_, err := c.do(ctx, request{method: http.MethodDelete, path: c.forestPath("/" + url.PathEscape(id)), header: h}, nil)
	if IsStatus(err, http.StatusPreconditionFailed) {
		return fmt.Errorf("%s: %w", id, ErrFlowChanged)
	}
	return err
}

func addOptional(body map[string]any, f *Flow) {
	for k, v := range map[string]json.RawMessage{"orphans": f.Orphans, "layouts": f.Layouts, "metadata": f.Metadata} {
		if len(v) > 0 && strings.TrimSpace(string(v)) != "null" {
			body[k] = v
		}
	}
}

// ValidationResult is the control plane's verdict on a tree.
type ValidationResult struct {
	Valid    bool             `json:"valid"`
	Errors   []ValidationItem `json:"errors"`
	Warnings []ValidationItem `json:"warnings"`
}

// ValidationItem is one validation finding.
type ValidationItem struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// ValidateTree runs the control plane's full validation (schema, action
// registry, globals, parser build) on a tree.
func (c *Client) ValidateTree(ctx context.Context, tree json.RawMessage) (*ValidationResult, error) {
	var res ValidationResult
	_, err := c.do(ctx, request{
		method:   http.MethodPost,
		path:     c.appPath(ControlPlaneApp, "/api/v1/validation/tree"),
		jsonBody: tree,
	}, &res)
	if err != nil {
		return nil, err
	}
	return &res, nil
}
