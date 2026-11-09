package novaapi

import "fmt"

// Error codes of the result format (ADR-0010): a small, fixed, UPPER_SNAKE_CASE set,
// shared with the instance MCP service. UNAUTHENTICATED and UNAVAILABLE are local
// additions: unlike the instance service, `nova mcp` handles the login and the network
// path to the instance itself.
const (
	CodeInvalidArgument  = "INVALID_ARGUMENT"
	CodeNotFound         = "NOT_FOUND"
	CodeAlreadyExists    = "ALREADY_EXISTS"
	CodeForbidden        = "FORBIDDEN"
	CodeUnauthenticated  = "UNAUTHENTICATED"
	CodeUnavailable      = "UNAVAILABLE"
	CodeApprovalRequired = "APPROVAL_REQUIRED"
	CodeInternal         = "INTERNAL"
)

// Error is a tool failure that is safe to show an MCP client verbatim: the message
// never carries raw API bodies. Unexpected answers are logged to stderr instead.
type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Message) }
