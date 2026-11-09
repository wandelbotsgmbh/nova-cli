// Package novaapi is the only part of `nova mcp` that knows the shape of the NOVA v2 API.
// It hands the MCP tools small, curated types instead of raw API responses.
//
// This is a hardening boundary, not just a convenience: raw controller configurations
// carry IP addresses and, for some vendors, credentials (AbbController.ControllerIp,
// KukaController.RsiServer, ...). Nothing in the API interface returns those types, so a
// tool has no way to hand them to a model, even by accident.
//
// Ported from the instance MCP service (service-manager MR !2924), which is meant to
// replace this local server once it ships (ADR-0019).
package novaapi

import "context"

// CellInfo is the curated projection of a cell: identity and membership only.
type CellInfo struct {
	Name        string
	Controllers []string
	Apps        []string
}

// ControllerInfo is the curated projection of a robot controller: identity and whether
// it is physical, never connection details or credentials.
type ControllerInfo struct {
	Name         string
	Manufacturer string
	// Physical is true for every controller kind except VirtualController, including
	// kinds this package does not recognize. See IsVirtualKind.
	Physical bool
	// VirtualType is the configuration preset (e.g. "universalrobots-ur5e"), set only
	// when Physical is false.
	VirtualType string
}

// VirtualControllerSpec describes a simulated controller to create. There is
// deliberately no equivalent for physical controllers: no tool asks for an IP, port or
// credentials.
type VirtualControllerSpec struct {
	Name         string
	Manufacturer string
	Type         string
}

// DeployResult reports the outcome of an asynchronous deployment (add/delete
// controller). Status is "completed" or "pending".
type DeployResult struct {
	Name   string
	Status string
}

const (
	StatusCompleted = "completed"
	StatusPending   = "pending"
)

// API is the slice of the NOVA v2 API the MCP tools need. It is narrow so tests can fake
// it, and its return types are curated (see the package doc). Every method returns nil
// or an *Error.
type API interface {
	GetSystemVersion(ctx context.Context) (string, error)
	ListCells(ctx context.Context) ([]string, error)
	DescribeCell(ctx context.Context, cellID string) (CellInfo, error)
	ListControllers(ctx context.Context, cellID string) ([]string, error)
	DescribeController(ctx context.Context, cellID, controllerID string) (ControllerInfo, error)
	ListRobotConfigurations(ctx context.Context) ([]string, error)
	CreateVirtualController(ctx context.Context, cellID string, spec VirtualControllerSpec) (DeployResult, error)
	DeleteController(ctx context.Context, cellID, controllerID string) (DeployResult, error)
}

// virtualControllerKind is the only discriminator value that counts as virtual.
// Everything else (every physical vendor kind, a kind added in a later NOVA version, a
// failed lookup) counts as physical: an allow-list, not a deny-list of vendors (ADR-0009).
const virtualControllerKind = "VirtualController"

// IsVirtualKind reports whether a controller's configuration discriminator (the "kind"
// returned by getRobotController) identifies a virtual controller. kind is always looked
// up on the instance, never taken from a tool's arguments.
func IsVirtualKind(kind string) bool {
	return kind == virtualControllerKind
}
