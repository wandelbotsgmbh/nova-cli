package tool_test

import (
	"context"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/tool"
)

type empty struct{}

func session(t *testing.T, elicit func(context.Context, *sdk.ElicitRequest) (*sdk.ElicitResult, error)) (*sdk.ClientSession, *int) {
	t.Helper()
	ran := 0
	server := sdk.NewServer(&sdk.Implementation{Name: "t", Version: "t"}, nil)
	sdk.AddTool(server, &sdk.Tool{Name: "restore", Annotations: tool.Destructive("Restore")},
		func(_ context.Context, req *sdk.CallToolRequest, _ empty) (*sdk.CallToolResult, empty, error) {
			if res := tool.Approval(req, "restore", "Restore the configuration of instance x?"); res != nil {
				return res, empty{}, nil
			}
			ran++
			return tool.OK("restored", nil, empty{})
		})
	ct, st := sdk.NewInMemoryTransports()
	if _, err := server.Connect(context.Background(), st, nil); err != nil {
		t.Fatal(err)
	}
	opts := &sdk.ClientOptions{}
	if elicit != nil {
		opts.ElicitationHandler = elicit
	}
	cs, err := sdk.NewClient(&sdk.Implementation{Name: "c", Version: "c"}, opts).Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs, &ran
}

func call(t *testing.T, cs *sdk.ClientSession) string {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &sdk.CallToolParams{Name: "restore", Arguments: map[string]any{}})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	return res.Content[0].(*sdk.TextContent).Text
}

func TestApproval_Accepted(t *testing.T) {
	var asked string
	cs, ran := session(t, func(_ context.Context, req *sdk.ElicitRequest) (*sdk.ElicitResult, error) {
		asked = req.Params.Message
		return &sdk.ElicitResult{Action: "accept", Content: map[string]any{"approve": true}}, nil
	})
	if text := call(t, cs); !strings.HasPrefix(text, "OK:") || *ran != 1 {
		t.Fatalf("expected the tool to run once after approval, got %q, ran %d", text, *ran)
	}
	if !strings.Contains(asked, "instance x") {
		t.Errorf("the human must see what is approved, got %q", asked)
	}
}

func TestApproval_Declined(t *testing.T) {
	for name, result := range map[string]*sdk.ElicitResult{
		"decline":          {Action: "decline"},
		"accept but false": {Action: "accept", Content: map[string]any{"approve": false}},
	} {
		t.Run(name, func(t *testing.T) {
			cs, ran := session(t, func(context.Context, *sdk.ElicitRequest) (*sdk.ElicitResult, error) { return result, nil })
			if text := call(t, cs); !strings.HasPrefix(text, "APPROVAL_REQUIRED:") || *ran != 0 {
				t.Fatalf("expected a refusal without running, got %q, ran %d", text, *ran)
			}
		})
	}
}

func TestApproval_ClientWithoutElicitation(t *testing.T) {
	cs, ran := session(t, nil)
	if text := call(t, cs); !strings.HasPrefix(text, "APPROVAL_REQUIRED:") || *ran != 0 {
		t.Fatalf("expected APPROVAL_REQUIRED without running, got %q, ran %d", text, *ran)
	}
}
