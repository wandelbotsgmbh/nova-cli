// Package guides is the `guides` toolset: how-tos for what is not a tool (streams,
// jogging, trajectory execution, free drive) and search over the instance's own API
// description for the long tail.
package guides

import (
	"context"
	"embed"
	"slices"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/tool"
)

//go:embed topics/*.md
var topicFiles embed.FS

// Topics are the guides get_guide serves, in a fixed order.
var Topics = []string{"jogging", "trajectory-execution", "state-streams", "io-streams", "free-drive", "io-wait", "visualisation"}

// Tools returns the tools of the guides toolset.
func Tools(env tool.Env) []tool.Registration {
	spec := &specCache{client: env.Client}
	return []tool.Registration{
		{Toolset: tool.Guides, ReadOnly: true, Add: addGetGuide},
		{Toolset: tool.Guides, ReadOnly: true, Add: func(s *sdk.Server) { addSearchAPIDocs(s, spec) }},
		{Toolset: tool.Guides, ReadOnly: true, Add: func(s *sdk.Server) { addDescribeAPIOperation(s, spec) }},
	}
}

type guideInput struct {
	Topic string `json:"topic" jsonschema:"one of: jogging, trajectory-execution, state-streams, io-streams, free-drive, io-wait, visualisation"`
}

type guideOutput struct {
	Topic  string   `json:"topic"`
	Guide  string   `json:"guide" jsonschema:"markdown how-to with endpoint, message flow and a minimal Python example"`
	Topics []string `json:"topics" jsonschema:"all available topics"`
}

func addGetGuide(s *sdk.Server) {
	const name = "get_guide"
	sdk.AddTool(s, &sdk.Tool{
		Name: name,
		Description: "How to use what is deliberately not a tool, from a script: jogging, trajectory execution, " +
			"state and I/O streams, free drive, waiting for I/O, visualisation. Returns endpoint, message flow and " +
			"a minimal Python example. Topics: " + strings.Join(Topics, ", ") + ".",
		Annotations: tool.Read("Get guide"),
	}, func(_ context.Context, _ *sdk.CallToolRequest, in guideInput) (*sdk.CallToolResult, guideOutput, error) {
		topic := strings.ToLower(strings.TrimSpace(in.Topic))
		if !slices.Contains(Topics, topic) {
			return tool.Invalid[guideOutput](name, "unknown topic '%s', pick one of: %s", in.Topic, strings.Join(Topics, ", "))
		}
		body, err := topicFiles.ReadFile("topics/" + topic + ".md")
		if err != nil {
			return tool.Fail[guideOutput](tool.AsAPIError(err))
		}
		connect, _ := topicFiles.ReadFile("topics/_connect.md")
		guide := string(body) + "\n" + string(connect)
		return tool.OK("guide returned", map[string]any{"topic": topic},
			guideOutput{Topic: topic, Guide: guide, Topics: Topics})
	})
}
