// Package system serves the `system` toolset: instance-wide status, features, updates,
// license, network, cloud connection and the caller's session. All tools only read.
package system

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/novaapi"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/tool"
)

// Tools returns the tools of this package.
func Tools(env tool.Env) []tool.Registration {
	c := env.Client
	return []tool.Registration{
		getSystemStatus(c),
		getSystemFeatures(c),
		checkSystemUpdate(c),
		getLicenseStatus(c),
		getNetworkInterfaces(c),
		getCloudStatus(c),
		getSession(c),
	}
}

// Service is the projection of one entry of a service status list.
type Service struct {
	Service  string `json:"service"`
	Group    string `json:"group" jsonschema:"SystemService, CellService, RobotController or App"`
	Code     string `json:"code" jsonschema:"phase, e.g. Running, Pending, CrashLoopBackOff"`
	Severity string `json:"severity" jsonschema:"INFO, WARNING or ERROR"`
	Reason   string `json:"reason,omitempty"`
}

type rawService struct {
	Service string `json:"service"`
	Group   string `json:"group"`
	Status  struct {
		Severity string `json:"severity"`
		Code     string `json:"code"`
		Reason   string `json:"reason"`
	} `json:"status"`
}

// projectServices maps a raw service status list, sorted by group and name.
func projectServices(raw []rawService) []Service {
	out := make([]Service, 0, len(raw))
	for _, r := range raw {
		out = append(out, Service{Service: r.Service, Group: r.Group, Code: r.Status.Code, Severity: r.Status.Severity, Reason: r.Status.Reason})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Group != out[j].Group {
			return out[i].Group < out[j].Group
		}
		return out[i].Service < out[j].Service
	})
	return out
}

// Problems keeps the services that aren't INFO.
func Problems(services []Service) []Service {
	out := []Service{}
	for _, s := range services {
		if s.Severity != "INFO" {
			out = append(out, s)
		}
	}
	return out
}

// FetchSystemStatus returns the status of the instance's system services.
func FetchSystemStatus(ctx context.Context, c *novaapi.Client, toolName string) ([]Service, *novaapi.Error) {
	var raw []rawService
	if err := c.Get(ctx, toolName, "/system/status", "", &raw); err != nil {
		return nil, err
	}
	return projectServices(raw), nil
}

// CellStatus is the projection of a cell's status.
type CellStatus struct {
	OperatingState string    `json:"operating_state" jsonschema:"ACTIVE or INACTIVE"`
	Services       []Service `json:"services"`
}

// FetchCellStatus returns the operating state and the service status of a cell.
func FetchCellStatus(ctx context.Context, c *novaapi.Client, toolName, cell string) (CellStatus, *novaapi.Error) {
	var raw struct {
		OperatingState string       `json:"operating_state"`
		ServiceStatus  []rawService `json:"service_status"`
	}
	if err := c.Get(ctx, toolName, "/cells/"+url.PathEscape(cell)+"/status", "cell '"+cell+"' not found", &raw); err != nil {
		return CellStatus{}, err
	}
	return CellStatus{OperatingState: raw.OperatingState, Services: projectServices(raw.ServiceStatus)}, nil
}

// FetchFeatures returns whether the instance supports physical devices.
func FetchFeatures(ctx context.Context, c *novaapi.Client, toolName string) (bool, *novaapi.Error) {
	var raw struct {
		PhysicalDevicesEnabled bool `json:"physical_devices_enabled"`
	}
	if err := c.Get(ctx, toolName, "/system/features", "", &raw); err != nil {
		return false, err
	}
	return raw.PhysicalDevicesEnabled, nil
}

// FetchVersion returns the NOVA version of the instance.
func FetchVersion(ctx context.Context, c *novaapi.Client, toolName string) (string, *novaapi.Error) {
	res, err := c.Do(ctx, novaapi.Call{Tool: toolName, Method: http.MethodGet, Path: "/system/version"})
	if err != nil {
		return "", err
	}
	return parseTextOrJSON(res.Body), nil
}

// LicenseStatus is the projection of the license status (never the license itself).
type LicenseStatus struct {
	Status  string `json:"status" jsonschema:"OK, EXPIRED, SUSPENDED, GRACE_PERIOD_OVER or NOT_FOUND"`
	Message string `json:"message"`
}

// FetchLicenseStatus returns the status of the instance's license. The API answers
// 403 with the status in the body when the license is not valid: that is an answer.
func FetchLicenseStatus(ctx context.Context, c *novaapi.Client, toolName string) (LicenseStatus, *novaapi.Error) {
	var out LicenseStatus
	call := novaapi.Call{Tool: toolName, Method: http.MethodGet, Path: "/license/status",
		NotFound: "no license status on this instance", AcceptStatus: []int{http.StatusForbidden}}
	status, err := c.JSON(ctx, call, &out)
	if err == nil && status == http.StatusForbidden && out.Status == "" {
		// a 403 without a license status is a plain refusal
		err = &novaapi.Error{Code: novaapi.CodeForbidden, Message: toolName + ": this operation is not allowed"}
	}
	return out, err
}

// CheckUpdate asks for a newer version on channel at path ("/system/update" or
// "/cells/<cell>/update"). It returns "" when there is none (HTTP 204).
func CheckUpdate(ctx context.Context, c *novaapi.Client, toolName, path, channel, notFound string) (string, *novaapi.Error) {
	res, err := c.Do(ctx, novaapi.Call{Tool: toolName, Method: http.MethodGet, Path: path,
		Query: url.Values{"channel": {channel}}, NotFound: notFound})
	if err != nil {
		return "", err
	}
	if res.Status == http.StatusNoContent {
		return "", nil
	}
	return parseTextOrJSON(res.Body), nil
}

// ValidChannel checks a release channel argument; empty means "stable".
func ValidChannel(toolName, channel string) (string, *novaapi.Error) {
	switch channel {
	case "":
		return "stable", nil
	case "stable", "next":
		return channel, nil
	}
	return "", &novaapi.Error{Code: novaapi.CodeInvalidArgument, Message: toolName + ": channel must be 'stable' or 'next'"}
}

func parseTextOrJSON(body []byte) string {
	var s string
	if json.Unmarshal(body, &s) == nil {
		return s
	}
	return strings.TrimSpace(string(body))
}

// --- get_system_status ---

type statusInput struct {
	ProblemsOnly bool `json:"problems_only,omitempty" jsonschema:"only return services whose severity is not INFO"`
}

type systemStatusOutput struct {
	Services []Service `json:"services"`
	Problems int       `json:"problems" jsonschema:"number of services with severity WARNING or ERROR"`
}

func getSystemStatus(c *novaapi.Client) tool.Registration {
	const name = "get_system_status"
	add := func(s *sdk.Server) {
		sdk.AddTool(s, &sdk.Tool{
			Name:        name,
			Description: "Status of the instance's system services (phase and severity per service). Use problems_only to see only services that aren't healthy.",
			Annotations: tool.Read("Get system status"),
		}, func(ctx context.Context, _ *sdk.CallToolRequest, in statusInput) (*sdk.CallToolResult, systemStatusOutput, error) {
			services, err := FetchSystemStatus(ctx, c, name)
			if err != nil {
				return tool.Fail[systemStatusOutput](err)
			}
			problems := Problems(services)
			if in.ProblemsOnly {
				services = problems
			}
			return tool.OK("system status", map[string]any{"services": len(services), "problems": len(problems)},
				systemStatusOutput{Services: services, Problems: len(problems)})
		})
	}
	return tool.Registration{Toolset: tool.System, ReadOnly: true, Add: add}
}

// --- get_system_features ---

type featuresOutput struct {
	PhysicalDevicesEnabled bool `json:"physical_devices_enabled" jsonschema:"false: the instance rejects physical robot controllers and physical bus I/O (e.g. cloud instances)"`
}

func getSystemFeatures(c *novaapi.Client) tool.Registration {
	const name = "get_system_features"
	add := func(s *sdk.Server) {
		sdk.AddTool(s, &sdk.Tool{
			Name:        name,
			Description: "Features enabled on this instance, e.g. whether it accepts physical robot controllers at all.",
			Annotations: tool.Read("Get system features"),
		}, func(ctx context.Context, _ *sdk.CallToolRequest, _ struct{}) (*sdk.CallToolResult, featuresOutput, error) {
			physical, err := FetchFeatures(ctx, c, name)
			if err != nil {
				return tool.Fail[featuresOutput](err)
			}
			return tool.OK("system features", map[string]any{"physical_devices_enabled": physical},
				featuresOutput{PhysicalDevicesEnabled: physical})
		})
	}
	return tool.Registration{Toolset: tool.System, ReadOnly: true, Add: add}
}

// --- check_system_update ---

type channelInput struct {
	Channel string `json:"channel,omitempty" jsonschema:"release channel: 'stable' (newest patch of the current version, default) or 'next' (latest version)"`
}

type updateOutput struct {
	CurrentVersion   string `json:"current_version"`
	UpdateAvailable  bool   `json:"update_available"`
	AvailableVersion string `json:"available_version,omitempty"`
	Channel          string `json:"channel"`
}

func checkSystemUpdate(c *novaapi.Client) tool.Registration {
	const name = "check_system_update"
	add := func(s *sdk.Server) {
		sdk.AddTool(s, &sdk.Tool{
			Name:        name,
			Description: "Check whether a newer NOVA version is available for the instance on a release channel. Only checks, never updates.",
			Annotations: tool.Read("Check system update"),
		}, func(ctx context.Context, _ *sdk.CallToolRequest, in channelInput) (*sdk.CallToolResult, updateOutput, error) {
			channel, err := ValidChannel(name, in.Channel)
			if err != nil {
				return tool.Fail[updateOutput](err)
			}
			current, err := FetchVersion(ctx, c, name)
			if err != nil {
				return tool.Fail[updateOutput](err)
			}
			available, err := CheckUpdate(ctx, c, name, "/system/update", channel, "")
			if err != nil {
				return tool.Fail[updateOutput](err)
			}
			out := updateOutput{CurrentVersion: current, Channel: channel, AvailableVersion: available, UpdateAvailable: available != ""}
			sentence := "no update available"
			if out.UpdateAvailable {
				sentence = "update available"
			}
			return tool.OK(sentence, map[string]any{"current": current, "available": available, "channel": channel}, out)
		})
	}
	return tool.Registration{Toolset: tool.System, ReadOnly: true, Add: add}
}

// --- get_license_status ---

func getLicenseStatus(c *novaapi.Client) tool.Registration {
	const name = "get_license_status"
	add := func(s *sdk.Server) {
		sdk.AddTool(s, &sdk.Tool{
			Name:        name,
			Description: "Status of the instance's NOVA license (OK, EXPIRED, ...). Never returns license keys.",
			Annotations: tool.Read("Get license status"),
		}, func(ctx context.Context, _ *sdk.CallToolRequest, _ struct{}) (*sdk.CallToolResult, LicenseStatus, error) {
			status, err := FetchLicenseStatus(ctx, c, name)
			if err != nil {
				return tool.Fail[LicenseStatus](err)
			}
			return tool.OK("license status", map[string]any{"status": status.Status}, status)
		})
	}
	return tool.Registration{Toolset: tool.System, ReadOnly: true, Add: add}
}

// --- get_network_interfaces ---

type networkInterface struct {
	Name      string   `json:"name" jsonschema:"host interface name, e.g. enp0s31f6"`
	Interface string   `json:"interface" jsonschema:"stable identifier of the interface"`
	Node      string   `json:"node" jsonschema:"host that exposes the interface"`
	Backend   string   `json:"backend" jsonschema:"macvlan or sriov"`
	Link      string   `json:"link" jsonschema:"up or down"`
	Addresses []string `json:"addresses,omitempty" jsonschema:"IP/CIDR addresses"`
}

type networkOutput struct {
	Interfaces []networkInterface `json:"interfaces"`
}

func getNetworkInterfaces(c *novaapi.Client) tool.Registration {
	const name = "get_network_interfaces"
	add := func(s *sdk.Server) {
		sdk.AddTool(s, &sdk.Tool{
			Name:        name,
			Description: "Network interfaces of the instance's host that robot controllers can be connected to (on-prem instances; cloud instances don't have them).",
			Annotations: tool.Read("Get network interfaces"),
		}, func(ctx context.Context, _ *sdk.CallToolRequest, _ struct{}) (*sdk.CallToolResult, networkOutput, error) {
			var raw []networkInterface
			if err := c.Get(ctx, name, "/system/network/interfaces",
				"network interface information is not available on this instance (e.g. cloud instances)", &raw); err != nil {
				return tool.Fail[networkOutput](err)
			}
			if raw == nil {
				raw = []networkInterface{}
			}
			sort.Slice(raw, func(i, j int) bool { return raw[i].Interface < raw[j].Interface })
			return tool.OK("network interfaces listed", map[string]any{"count": len(raw)}, networkOutput{Interfaces: raw})
		})
	}
	return tool.Registration{Toolset: tool.System, ReadOnly: true, Add: add}
}

// --- get_cloud_status ---

type cloudOutput struct {
	Ready     bool              `json:"ready" jsonschema:"true when every NOVA Cloud reachability check passed"`
	Checks    map[string]bool   `json:"checks" jsonschema:"result per check (is_configured, can_connect_nats, can_ping_nats, can_reach_openfga); a check is absent when a prerequisite failed"`
	Errors    map[string]string `json:"errors,omitempty" jsonschema:"failure reason per failed check"`
	CheckedAt string            `json:"checked_at"`
}

func getCloudStatus(c *novaapi.Client) tool.Registration {
	const name = "get_cloud_status"
	add := func(s *sdk.Server) {
		sdk.AddTool(s, &sdk.Tool{
			Name:        name,
			Description: "Whether the instance is connected to NOVA Cloud, with the result of each reachability check. Only reads; connecting needs the NOVA UI.",
			Annotations: tool.Read("Get cloud status"),
		}, func(ctx context.Context, _ *sdk.CallToolRequest, _ struct{}) (*sdk.CallToolResult, cloudOutput, error) {
			var raw struct {
				Ready     bool              `json:"ready"`
				Checks    map[string]bool   `json:"checks"`
				Errors    map[string]string `json:"errors"`
				CheckedAt string            `json:"checked_at"`
			}
			if err := c.Get(ctx, name, "/experimental/cloud/status", "cloud status is not available on this instance", &raw); err != nil {
				return tool.Fail[cloudOutput](err)
			}
			if raw.Checks == nil {
				raw.Checks = map[string]bool{}
			}
			out := cloudOutput{Ready: raw.Ready, Checks: raw.Checks, Errors: raw.Errors, CheckedAt: raw.CheckedAt}
			return tool.OK("cloud status", map[string]any{"ready": raw.Ready}, out)
		})
	}
	return tool.Registration{Toolset: tool.System, ReadOnly: true, Add: add}
}

// --- get_session ---

type capability struct {
	Capability  string   `json:"capability"`
	ResourceIDs []string `json:"resource_ids,omitempty" jsonschema:"resources the capability is limited to; empty means global"`
}

type sessionOutput struct {
	UserID       string       `json:"user_id,omitempty"`
	UserName     string       `json:"user_name,omitempty"`
	UserEmail    string       `json:"user_email,omitempty"`
	Capabilities []capability `json:"capabilities" jsonschema:"what the caller may do; empty on instances without authorization"`
	ExpiresAt    string       `json:"expires_at,omitempty" jsonschema:"when the caller's login expires"`
}

func getSession(c *novaapi.Client) tool.Registration {
	const name = "get_session"
	add := func(s *sdk.Server) {
		sdk.AddTool(s, &sdk.Tool{
			Name: name,
			Description: "Who the instance sees calling (identity) and which capabilities (scopes) that login has. " +
				"Use it to explain FORBIDDEN errors. Never returns the token or session id.",
			Annotations: tool.Read("Get session"),
		}, func(ctx context.Context, _ *sdk.CallToolRequest, _ struct{}) (*sdk.CallToolResult, sessionOutput, error) {
			var raw struct {
				User struct {
					ID    string `json:"id"`
					Name  string `json:"name"`
					Email string `json:"email"`
				} `json:"user"`
				Capabilities []capability `json:"capabilities"`
				ExpiresAt    string       `json:"expires_at"`
			}
			if err := c.Get(ctx, name, "/session", "", &raw); err != nil {
				return tool.Fail[sessionOutput](err)
			}
			caps := raw.Capabilities
			if caps == nil {
				caps = []capability{}
			}
			sort.Slice(caps, func(i, j int) bool { return caps[i].Capability < caps[j].Capability })
			expires := raw.ExpiresAt
			if strings.HasPrefix(expires, "0001-01-01") {
				expires = ""
			}
			out := sessionOutput{UserID: raw.User.ID, UserName: raw.User.Name, UserEmail: raw.User.Email, Capabilities: caps, ExpiresAt: expires}
			return tool.OK("session", map[string]any{"capabilities": len(caps)}, out)
		})
	}
	return tool.Registration{Toolset: tool.System, ReadOnly: true, Add: add}
}
