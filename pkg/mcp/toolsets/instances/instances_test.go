package instances_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/tool"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/toolsets/instances"
)

type fakeSwitcher struct {
	current string
	list    []tool.InstanceInfo
}

func (f *fakeSwitcher) List() ([]tool.InstanceInfo, error) {
	out := make([]tool.InstanceInfo, len(f.list))
	for i, inst := range f.list {
		inst.Current = inst.Name == f.current
		out[i] = inst
	}
	return out, nil
}

func (f *fakeSwitcher) Use(name string) (tool.InstanceInfo, error) {
	for _, inst := range f.list {
		if inst.Name == name {
			f.current = name
			inst.Current = true
			return inst, nil
		}
	}
	return tool.InstanceInfo{}, errors.New(`no instance "` + name + `" is configured (see list_instances)`)
}

func connect(t *testing.T, env tool.Env) *sdk.ClientSession {
	t.Helper()
	server := sdk.NewServer(&sdk.Implementation{Name: "t", Version: "t"}, nil)
	for _, reg := range instances.Tools(env) {
		reg.Add(server)
	}
	ct, st := sdk.NewInMemoryTransports()
	if _, err := server.Connect(context.Background(), st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := sdk.NewClient(&sdk.Implementation{Name: "c", Version: "c"}, nil).Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func call(t *testing.T, cs *sdk.ClientSession, name string, args map[string]any) (string, bool) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return res.Content[0].(*sdk.TextContent).Text, res.IsError
}

func TestPinned_NoTools(t *testing.T) {
	if regs := instances.Tools(tool.Env{}); len(regs) != 0 {
		t.Fatalf("a pinned instance must not offer instance tools, got %d", len(regs))
	}
}

func TestListAndUse(t *testing.T) {
	sw := &fakeSwitcher{current: "a", list: []tool.InstanceInfo{
		{Name: "a", Kind: "cloud", Host: "a.instance.wandelbots.io", Cell: "cell", Env: "prod"},
		{Name: "b", Kind: "host", Host: "192.168.1.10", Cell: "cell"},
	}}
	cs := connect(t, tool.Env{Instances: sw})

	text, isErr := call(t, cs, "list_instances", nil)
	if isErr || !strings.Contains(text, "count=2") || !strings.Contains(text, "current=a") {
		t.Fatalf("list: %s", text)
	}
	text, isErr = call(t, cs, "use_instance", map[string]any{"name": "b"})
	if isErr || !strings.Contains(text, "instance=b") || sw.current != "b" {
		t.Fatalf("use: %s (current %s)", text, sw.current)
	}
	text, isErr = call(t, cs, "use_instance", map[string]any{"name": "nope"})
	if !isErr || !strings.HasPrefix(text, "INVALID_ARGUMENT:") || sw.current != "b" {
		t.Fatalf("unknown instance: %s (current %s)", text, sw.current)
	}
}
