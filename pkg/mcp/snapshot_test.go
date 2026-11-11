package mcp_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	novamcp "github.com/wandelbotsgmbh/nova-cli/pkg/mcp"
)

const snapshotPath = "testdata/tools_snapshot.json"

// toolSnapshot captures exactly the fields ADR-0008's versioning decision
// asks to pin: name, description, input schema and annotations. Changing
// any of these for a shipped tool is a tool-surface change and should show
// up as a diff in review, not silently.
type toolSnapshot struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema any    `json:"inputSchema"`
	Annotations any    `json:"annotations"`
	Meta        any    `json:"_meta,omitempty"`
}

// TestToolsSnapshot pins the full tool surface (every toolset, not
// read-only) against testdata/tools_snapshot.json. Run with
// UPDATE_SNAPSHOTS=1 to regenerate the file after a deliberate change.
func TestToolsSnapshot(t *testing.T) {
	session := connect(t, &fakeAPI{}, novamcp.AllToolsets(), false)
	result, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	got := make([]toolSnapshot, len(result.Tools))
	for i, tool := range result.Tools {
		got[i] = toolSnapshot{
			Name:        tool.Name,
			Description: tool.Description,
			InputSchema: tool.InputSchema,
			Annotations: tool.Annotations,
			Meta:        tool.Meta,
		}
	}

	gotJSON, err := json.MarshalIndent(got, "", "  ")
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	gotJSON = append(gotJSON, '\n')

	if os.Getenv("UPDATE_SNAPSHOTS") == "1" {
		if err := os.MkdirAll(filepath.Dir(snapshotPath), 0o755); err != nil {
			t.Fatalf("mkdir testdata: %v", err)
		}
		if err := os.WriteFile(snapshotPath, gotJSON, 0o644); err != nil {
			t.Fatalf("write snapshot: %v", err)
		}
		t.Logf("wrote %s", snapshotPath)
		return
	}

	want, err := os.ReadFile(snapshotPath)
	if err != nil {
		t.Fatalf("read %s (run with UPDATE_SNAPSHOTS=1 to create it): %v", snapshotPath, err)
	}
	if string(gotJSON) != string(want) {
		t.Errorf("tool surface changed — if this is deliberate, re-run with UPDATE_SNAPSHOTS=1 and review the diff.\ngot:\n%s\nwant:\n%s", gotJSON, want)
	}
}
