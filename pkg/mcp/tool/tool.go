// Package tool holds what every toolset of `nova mcp` shares: toolset names, the
// registration type, annotations, the result format (ADR-0010), pagination and the
// human approval for writes that need one.
//
// Rules for tools (ADR-0008, the instance MCP service's roadmap):
//   - Curated: a tool does one task, takes a few validated arguments and returns a small
//     projection, never a raw API body. Response structs declare only the fields handed
//     on, never connection details, credentials, environment values or tokens.
//   - Results: tool.OK with a "CODE: sentence; k=v" line plus structuredContent;
//     failures are tool.Fail (isError), never Go errors.
//   - Lists are paginated with tool.Page and say how to narrow the query.
//   - Motion, I/O writes and mode changes go through novaapi.Client.RequireVirtual.
//   - Every tool sets all four annotations explicitly.
package tool

import (
	"fmt"
	"sort"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/novaapi"
)

// Toolset names (the roadmap of the instance MCP service).
const (
	Context            = "context"
	Cells              = "cells"
	Controllers        = "controllers"
	VirtualControllers = "virtual-controllers"
	MotionGroupModels  = "motion-group-models"
	System             = "system"
	Apps               = "apps"
	Diagnosis          = "diagnosis"
	Guides             = "guides"
	Kinematics         = "kinematics"
	Datasets           = "datasets"
	Planning           = "planning"
	Stores             = "stores"
	Motion             = "motion"
	Programs           = "programs"
	Maintenance        = "maintenance"
	// Instances switches the target of the session; local to nova mcp (the instance
	// service is the instance) and only served when the instance isn't pinned.
	Instances = "instances"
)

// DefaultToolsets are served without --toolsets: mostly reads.
func DefaultToolsets() []string {
	return []string{Instances, Context, Cells, Controllers, VirtualControllers, MotionGroupModels, System, Apps,
		Diagnosis, Guides, Kinematics, Datasets}
}

// OptInToolsets must be requested with --toolsets.
func OptInToolsets() []string {
	return []string{Planning, Stores, Motion, Programs, Maintenance}
}

// AllToolsets is every toolset in a fixed order.
func AllToolsets() []string {
	return append(DefaultToolsets(), OptInToolsets()...)
}

// Registration binds one tool to its toolset and says whether it is safe under
// --read-only (it doesn't change the instance or the local machine).
type Registration struct {
	Toolset  string
	ReadOnly bool
	Add      func(s *sdk.Server)
}

func ptr[T any](v T) *T { return &v }

// Annotations sets all four hints explicitly (MCP defaults destructive and openWorld to
// true).
func Annotations(title string, readOnly, idempotent, destructive, openWorld bool) *sdk.ToolAnnotations {
	return &sdk.ToolAnnotations{
		Title:           title,
		ReadOnlyHint:    readOnly,
		IdempotentHint:  idempotent,
		DestructiveHint: ptr(destructive),
		OpenWorldHint:   ptr(openWorld),
	}
}

// Read is the annotation set of a read tool.
func Read(title string) *sdk.ToolAnnotations { return Annotations(title, true, true, false, false) }

// Write is the annotation set of a non-destructive write.
func Write(title string, idempotent bool) *sdk.ToolAnnotations {
	return Annotations(title, false, idempotent, false, false)
}

// Destructive is the annotation set of a write that deletes or overwrites.
func Destructive(title string) *sdk.ToolAnnotations {
	return Annotations(title, false, true, true, false)
}

// Line renders "CODE: sentence; key=value; ..." with sorted keys.
func Line(code, sentence string, fields map[string]any) string {
	var b strings.Builder
	b.WriteString(code)
	b.WriteString(": ")
	b.WriteString(sentence)
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&b, "; %s=%v", k, fields[k])
	}
	return b.String()
}

// OK builds a successful result: the line as text, out as structuredContent.
func OK[Out any](sentence string, fields map[string]any, out Out) (*sdk.CallToolResult, Out, error) {
	return &sdk.CallToolResult{
		Content: []sdk.Content{&sdk.TextContent{Text: Line("OK", sentence, fields)}},
	}, out, nil
}

// Fail builds a failed result (isError, not a protocol error).
func Fail[Out any](err *novaapi.Error) (*sdk.CallToolResult, Out, error) {
	var zero Out
	return &sdk.CallToolResult{
		IsError: true,
		Content: []sdk.Content{&sdk.TextContent{Text: Line(err.Code, err.Message, nil)}},
	}, zero, nil
}

// Invalid is Fail with INVALID_ARGUMENT.
func Invalid[Out any](toolName, format string, args ...any) (*sdk.CallToolResult, Out, error) {
	return Fail[Out](&novaapi.Error{Code: novaapi.CodeInvalidArgument, Message: toolName + ": " + fmt.Sprintf(format, args...)})
}

// AsAPIError narrows err to *novaapi.Error; anything else is INTERNAL.
func AsAPIError(err error) *novaapi.Error {
	if apiErr, ok := err.(*novaapi.Error); ok {
		return apiErr
	}
	return &novaapi.Error{Code: novaapi.CodeInternal, Message: "internal error, details are in the log of 'nova mcp' (stderr)"}
}

// Required returns INVALID_ARGUMENT naming the first empty argument, nil if all are
// set. Clients don't enforce inputSchema, so tools check their arguments themselves.
func Required(toolName string, args ...string) *novaapi.Error {
	for i := 0; i+1 < len(args); i += 2 {
		if strings.TrimSpace(args[i+1]) == "" {
			return &novaapi.Error{Code: novaapi.CodeInvalidArgument, Message: fmt.Sprintf("%s: '%s' is required", toolName, args[i])}
		}
	}
	return nil
}

// PageInput is embedded in the input of list tools.
type PageInput struct {
	Offset int `json:"offset,omitempty" jsonschema:"number of items to skip (default 0)"`
	Limit  int `json:"limit,omitempty" jsonschema:"maximum number of items to return (default 50, max 200)"`
}

// PageInfo is embedded in the output of list tools.
type PageInfo struct {
	Total    int  `json:"total"`
	Offset   int  `json:"offset"`
	Returned int  `json:"returned"`
	More     bool `json:"more" jsonschema:"true if more items follow; call again with offset=offset+returned"`
}

// Page cuts items to the requested window (default 50, max 200).
func Page[T any](items []T, in PageInput) ([]T, PageInfo) {
	limit := in.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	offset := max(in.Offset, 0)
	offset = min(offset, len(items))
	end := min(offset+limit, len(items))
	window := items[offset:end]
	if window == nil {
		window = []T{}
	}
	return window, PageInfo{Total: len(items), Offset: offset, Returned: len(window), More: end < len(items)}
}

// Fields returns the fields of a PageInfo for the result line.
func (p PageInfo) Fields(extra map[string]any) map[string]any {
	fields := map[string]any{"total": p.Total, "returned": p.Returned}
	if p.More {
		fields["next_offset"] = p.Offset + p.Returned
	}
	for k, v := range extra {
		fields[k] = v
	}
	return fields
}

// Env is what a toolset package gets to build its tools.
type Env struct {
	// Client calls the NOVA v2 API of the targeted instance.
	Client *novaapi.Client
	// DownloadDir is where tools save files they fetch (diagnosis packages, backups);
	// results name the absolute path. Created on first use.
	DownloadDir string
	// Instances lists and switches the target instance of this session. Nil when the
	// instance is pinned (--instance, NOVA_INSTANCE): then the instances tools aren't
	// served at all.
	Instances InstanceSwitcher
}

// InstanceInfo describes a configured instance, without secrets (no tokens,
// kubeconfig paths or ssh users).
type InstanceInfo struct {
	Name    string `json:"name"`
	Kind    string `json:"kind" jsonschema:"cloud, host or k8srun"`
	Host    string `json:"host"`
	Cell    string `json:"cell" jsonschema:"default cell of the entry"`
	Env     string `json:"env,omitempty" jsonschema:"portal environment of a cloud instance"`
	Current bool   `json:"current" jsonschema:"true for the instance this session targets"`
}

// InstanceSwitcher is implemented by the CLI: the registry is in the user's nova config.
type InstanceSwitcher interface {
	// List returns the configured instances, read fresh from the config.
	List() ([]InstanceInfo, error)
	// Use makes name the target of this session (never written to the config) and
	// returns it. The error message is user-facing.
	Use(name string) (InstanceInfo, error)
}
