// Package instances lets an agent list the user's configured NOVA instances and switch
// the target of this `nova mcp` session. It only exists locally (the instance MCP
// service is the instance) and only when the instance isn't pinned with --instance or
// NOVA_INSTANCE (tool.Env.Instances is nil then). Switching never writes the config,
// so `nova instance select` and other sessions are unaffected.
package instances

import (
	"context"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/novaapi"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/tool"
)

// Tools returns list_instances and use_instance, or nothing when the instance is pinned.
func Tools(env tool.Env) []tool.Registration {
	if env.Instances == nil {
		return nil
	}
	sw := env.Instances
	return []tool.Registration{
		{Toolset: tool.Instances, ReadOnly: true, Add: func(s *sdk.Server) { addList(s, sw) }},
		// changes only which instance this session talks to, nothing on an instance or in
		// the config: served under --read-only too
		{Toolset: tool.Instances, ReadOnly: true, Add: func(s *sdk.Server) { addUse(s, sw) }},
	}
}

type listOutput struct {
	Instances []tool.InstanceInfo `json:"instances"`
	Current   string              `json:"current,omitempty" jsonschema:"name of the instance this session targets"`
}

func addList(s *sdk.Server, sw tool.InstanceSwitcher) {
	const name = "list_instances"
	sdk.AddTool(s, &sdk.Tool{
		Name: name,
		Description: "List the NOVA instances configured in the user's nova CLI (name, kind, host, default cell) and " +
			"which one this session targets. Switch with use_instance.",
		Annotations: tool.Read("List instances"),
	}, func(context.Context, *sdk.CallToolRequest, struct{}) (*sdk.CallToolResult, listOutput, error) {
		list, err := sw.List()
		if err != nil {
			return tool.Fail[listOutput](&novaapi.Error{Code: novaapi.CodeInternal, Message: name + ": " + err.Error()})
		}
		out := listOutput{Instances: list}
		for _, i := range list {
			if i.Current {
				out.Current = i.Name
			}
		}
		if out.Instances == nil {
			out.Instances = []tool.InstanceInfo{}
		}
		return tool.OK("instances listed", map[string]any{"count": len(list), "current": out.Current}, out)
	})
}

type useInput struct {
	Name string `json:"name" jsonschema:"instance name from list_instances"`
}

func addUse(s *sdk.Server, sw tool.InstanceSwitcher) {
	const name = "use_instance"
	sdk.AddTool(s, &sdk.Tool{
		Name: name,
		Description: "Switch the instance this session talks to (all later tool calls go there). Only for this " +
			"session: the user's selected instance and other sessions are unchanged. Confirm with the user before " +
			"switching to an instance they didn't name; call get_context afterwards.",
		Annotations: tool.Annotations("Use instance", false, true, false, false),
	}, func(_ context.Context, _ *sdk.CallToolRequest, in useInput) (*sdk.CallToolResult, tool.InstanceInfo, error) {
		if err := tool.Required(name, "name", in.Name); err != nil {
			return tool.Fail[tool.InstanceInfo](err)
		}
		info, err := sw.Use(in.Name)
		if err != nil {
			return tool.Fail[tool.InstanceInfo](&novaapi.Error{Code: novaapi.CodeInvalidArgument, Message: name + ": " + err.Error()})
		}
		return tool.OK("instance in use for this session", map[string]any{
			"instance": info.Name, "host": info.Host, "kind": info.Kind, "cell": info.Cell,
		}, info)
	})
}
