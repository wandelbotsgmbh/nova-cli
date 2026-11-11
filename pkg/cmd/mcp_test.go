package cmd

import (
	"slices"
	"testing"

	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/tool"
)

// An unpinned session always serves the instance tools: with the defaults when
// --toolsets is not given, and on top of an explicit list.
func TestWithInstanceTools(t *testing.T) {
	got := withInstanceTools(nil)
	for _, want := range append(mcp.DefaultToolsets(), tool.Instances) {
		if !slices.Contains(got, want) {
			t.Errorf("no --toolsets: %q missing in %v", want, got)
		}
	}
	got = withInstanceTools([]string{tool.Planning, tool.Motion})
	if len(got) != 3 || !slices.Contains(got, tool.Instances) {
		t.Errorf("explicit list: got %v", got)
	}
}
