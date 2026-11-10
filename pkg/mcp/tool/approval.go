package tool

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/novaapi"
)

// approvalID is the key of the approval in inputRequests/inputResponses.
const approvalID = "approval"

// Approval asks the human, not the model, to approve a write through the MCP client
// (elicitation, ADR-0009 addendum): on MCP 2026-07-28 the tool answers input_required
// and the client retries the call with the answer; older clients get an
// elicitation/create request from the SDK.
//
// Use it at the top of a handler, after validating the arguments:
//
//	if res := tool.Approval(ctx, req, toolName, "Restore the configuration from ..."); res != nil {
//		return res, Out{}, nil
//	}
//
// It returns nil when the human accepted. Otherwise it returns the result to hand back
// unchanged: the input request, a decline, or APPROVAL_REQUIRED when the client can't
// ask (no elicitation support). The message must name exactly what will happen.
//
// The approval is bound to what the human was shown: the message, prefixed with the
// instance the call is pinned to (novaapi.Client.Pin). A retry with other arguments
// (and so another message), or one that arrives after use_instance switched elsewhere,
// is refused: a "yes" never covers another cell, controller, backup or instance.
func Approval(ctx context.Context, req *sdk.CallToolRequest, toolName, message string) *sdk.CallToolResult {
	if t, ok := novaapi.PinnedTarget(ctx); ok {
		if u, err := url.Parse(t.BaseURL); err == nil && u.Host != "" {
			message = "On " + u.Host + ": " + message
		}
	}
	digest := sha256.Sum256([]byte(message))
	state := toolName + " " + hex.EncodeToString(digest[:])
	if req != nil && req.Params != nil {
		if resp, ok := req.Params.InputResponses[approvalID]; ok {
			if req.Params.RequestState != state {
				res, _, _ := Fail[any](&novaapi.Error{Code: novaapi.CodeApprovalRequired,
					Message: toolName + ": the request or the instance changed since the user was asked; call the tool again to ask again"})
				return res
			}
			if r, ok := resp.(*sdk.ElicitResult); ok && r.Action == "accept" {
				if approved, _ := r.Content["approve"].(bool); approved || r.Content == nil {
					return nil
				}
			}
			res, _, _ := Fail[any](&novaapi.Error{Code: novaapi.CodeApprovalRequired,
				Message: toolName + ": the user declined; do not retry unless the user asks for it"})
			return res
		}
	}
	if !canElicit(req) {
		res, _, _ := Fail[any](&novaapi.Error{Code: novaapi.CodeApprovalRequired, Message: fmt.Sprintf(
			"%s: needs the user's approval, but this MCP client can't ask (no elicitation support); the user has to do it through the NOVA UI or the nova CLI", toolName)})
		return res
	}
	return &sdk.CallToolResult{
		InputRequests: sdk.InputRequestMap{approvalID: &sdk.ElicitParams{
			Message: message,
			RequestedSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"approve": map[string]any{"type": "boolean", "title": "Approve", "description": message},
				},
				"required": []string{"approve"},
			},
		}},
		RequestState: state,
	}
}

func canElicit(req *sdk.CallToolRequest) bool {
	if req == nil || req.Session == nil {
		return false
	}
	params := req.Session.InitializeParams()
	return params != nil && params.Capabilities != nil && params.Capabilities.Elicitation != nil
}
