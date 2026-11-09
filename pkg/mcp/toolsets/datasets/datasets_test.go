package datasets_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/internal/livetest"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/novaapi"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/tool"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/toolsets/datasets"
)

func connect(t *testing.T, c *novaapi.Client, readOnly bool) *sdk.ClientSession {
	t.Helper()
	server := sdk.NewServer(&sdk.Implementation{Name: "test", Version: "test"}, nil)
	for _, reg := range datasets.Tools(tool.Env{Client: c}) {
		if reg.Toolset != tool.Datasets {
			t.Fatalf("tool registered for toolset %q", reg.Toolset)
		}
		if readOnly && !reg.ReadOnly {
			continue
		}
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

type request struct {
	Method, Path, Query string
	Body                []byte
}

// fake serves answers by "METHOD path" and records requests.
func fake(t *testing.T, answers map[string]func(w http.ResponseWriter)) (*novaapi.Client, *[]request) {
	t.Helper()
	var got []request
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got = append(got, request{r.Method, r.URL.EscapedPath(), r.URL.RawQuery, body})
		if answer, ok := answers[r.Method+" "+r.URL.EscapedPath()]; ok {
			answer(w)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(server.Close)
	return novaapi.NewClient(server.URL, nil, nil, nil), &got
}

func jsonAnswer(status int, body string) func(http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}
}

func call(t *testing.T, cs *sdk.ClientSession, name string, args map[string]any) (*sdk.CallToolResult, string) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return res, res.Content[0].(*sdk.TextContent).Text
}

func structured(t *testing.T, res *sdk.CallToolResult, out any) {
	t.Helper()
	raw, _ := json.Marshal(res.StructuredContent)
	if err := json.Unmarshal(raw, out); err != nil {
		t.Fatal(err)
	}
}

const datasetBody = `{
	"dataset": "default", "name": "default", "revision": 3, "description": "Default seeded dataset",
	"created_at": "2026-11-04T10:33:45Z", "updated_at": "2026-11-04T10:33:45Z",
	"poses": [
		{"pose": {"position": [1000, 0, 900], "orientation": [0, 3.141, 0]}, "kinematic_configuration": null, "frame": null,
		 "dataset_pose": "home", "dataset": "default", "name": "home", "metadata": {"seeded": "true"}},
		{"pose": {"position": [800, 0, 800], "orientation": [0, 3.141, 0]},
		 "kinematic_configuration": {"kinematic_branch": {"shoulder_branch": "FRONT", "elbow_branch": "UP", "wrist_branch": "NO_FLIP"}},
		 "frame": "table", "dataset_pose": "table-origin", "dataset": "default"}
	],
	"frames": [{"frame": "table", "name": "Table", "reference_frame": null, "pose": {"position": [500, 0, 0], "orientation": [0, 0, 0]}, "dataset": "default"}],
	"command_routines": [{
		"command_routine": "pick", "name": "Pick", "motion_group": {"type": "id", "id": "0@ur5e"},
		"motion_group_setup": {"motion_group_model": "UniversalRobots_UR5e", "cycle_time": 8, "payload": {"name": "secret-payload", "payload": 1}},
		"tcp": "Flange", "start_joint_position": [0, -1.57, 1.57, 0, 0, 0],
		"commands": [
			{"type": "explicit_path_motion", "path_type": "line", "target": {"dataset_pose": "home"}, "metadata": {"m": "x"}},
			{"type": "set_io", "io_value": {"io": "out1", "value": true}, "io_origin": "controller", "intent": "open gripper"}
		]
	}]
}`

func TestListDatasets_LatestOnlyByDefault(t *testing.T) {
	c, got := fake(t, map[string]func(http.ResponseWriter){
		"GET /experimental/cells/cell/datasets": jsonAnswer(200, `[
			{"dataset": "a", "name": "A", "revision": 2, "description": "`+strings.Repeat("x", 500)+`", "created_at": "t", "updated_at": "u"}]`),
	})
	cs := connect(t, c, false)
	res, text := call(t, cs, "list_datasets", map[string]any{"cell": "cell"})
	if res.IsError || !strings.Contains(text, "total=1") {
		t.Fatalf("unexpected result %q", text)
	}
	if (*got)[0].Query != "latest_only=true" {
		t.Errorf("expected latest_only=true, got %q", (*got)[0].Query)
	}
	var out struct {
		Datasets []datasets.DatasetSummary `json:"datasets"`
	}
	structured(t, res, &out)
	if len(out.Datasets) != 1 || len([]rune(out.Datasets[0].Description)) > 201 {
		t.Errorf("expected one dataset with a shortened description, got %+v", out.Datasets)
	}
}

func TestListDatasets_OneDatasetPicksLatestRevision(t *testing.T) {
	c, got := fake(t, map[string]func(http.ResponseWriter){
		"GET /experimental/cells/cell/datasets": jsonAnswer(200, `[
			{"dataset": "a", "revision": 1, "created_at": "t", "updated_at": "u"},
			{"dataset": "a", "revision": 4, "created_at": "t", "updated_at": "u"}]`),
	})
	res, _ := call(t, connect(t, c, false), "list_datasets", map[string]any{"cell": "cell", "dataset": "a"})
	var out struct {
		Datasets []datasets.DatasetSummary `json:"datasets"`
	}
	structured(t, res, &out)
	if len(out.Datasets) != 1 || out.Datasets[0].Revision != 4 {
		t.Errorf("expected only revision 4, got %+v", out.Datasets)
	}
	if (*got)[0].Query != "dataset=a" {
		t.Errorf("unexpected query %q", (*got)[0].Query)
	}
}

func TestDescribeDataset(t *testing.T) {
	c, got := fake(t, map[string]func(http.ResponseWriter){
		"GET /experimental/cells/cell/datasets/default": jsonAnswer(200, datasetBody),
	})
	res, text := call(t, connect(t, c, false), "describe_dataset", map[string]any{"cell": "cell", "dataset": "default", "revision": 3})
	if res.IsError || !strings.Contains(text, "poses=2") || !strings.Contains(text, "command_routines=1") {
		t.Fatalf("unexpected result %q", text)
	}
	if (*got)[0].Query != "revision=3" {
		t.Errorf("expected revision=3, got %q", (*got)[0].Query)
	}
	raw, _ := json.Marshal(res.StructuredContent)
	if strings.Contains(string(raw), "secret-payload") || strings.Contains(string(raw), "metadata") {
		t.Errorf("describe_dataset handed on fields it shouldn't: %s", raw)
	}
}

func TestListDatasetPoses_FilterAndBranch(t *testing.T) {
	c, _ := fake(t, map[string]func(http.ResponseWriter){
		"GET /experimental/cells/cell/datasets/default": jsonAnswer(200, datasetBody),
	})
	res, text := call(t, connect(t, c, false), "list_dataset_poses", map[string]any{"cell": "cell", "dataset": "default", "frame": "table"})
	if res.IsError {
		t.Fatal(text)
	}
	var out struct {
		Poses []datasets.PoseSummary `json:"poses"`
	}
	structured(t, res, &out)
	if len(out.Poses) != 1 || out.Poses[0].DatasetPose != "table-origin" || out.Poses[0].Branch != "FRONT/UP/NO_FLIP" {
		t.Errorf("unexpected poses %+v", out.Poses)
	}
}

func TestDescribeCommandRoutine(t *testing.T) {
	c, _ := fake(t, map[string]func(http.ResponseWriter){
		"GET /experimental/cells/cell/datasets/default": jsonAnswer(200, datasetBody),
	})
	cs := connect(t, c, false)
	res, text := call(t, cs, "describe_command_routine", map[string]any{"cell": "cell", "dataset": "default", "command_routine": "pick"})
	if res.IsError {
		t.Fatal(text)
	}
	var out struct {
		MotionGroupModel string                    `json:"motion_group_model"`
		Commands         []datasets.CommandSummary `json:"commands"`
	}
	structured(t, res, &out)
	if out.MotionGroupModel != "UniversalRobots_UR5e" || len(out.Commands) != 2 ||
		out.Commands[0].Type != "explicit_path_motion" || out.Commands[1].Intent != "open gripper" {
		t.Errorf("unexpected routine %+v", out)
	}
	if strings.Contains(out.Commands[0].Details, "metadata") || !strings.Contains(out.Commands[0].Details, "home") {
		t.Errorf("unexpected details %q", out.Commands[0].Details)
	}

	res, text = call(t, cs, "describe_command_routine", map[string]any{"cell": "cell", "dataset": "default", "command_routine": "nope"})
	if !res.IsError || !strings.HasPrefix(text, "NOT_FOUND:") {
		t.Errorf("expected NOT_FOUND, got %q", text)
	}
}

func TestResolvePoses_ValidatesAndSends(t *testing.T) {
	c, got := fake(t, map[string]func(http.ResponseWriter){
		"POST /experimental/cells/cell/datasets/default/frames/table/resolve": jsonAnswer(200, `[{"position": [1300, 0, 800], "orientation": [0, 0, 0]}]`),
	})
	cs := connect(t, c, true) // read-only: resolve is a pure computation
	res, text := call(t, cs, "resolve_poses_to_world", map[string]any{"cell": "cell", "dataset": "default", "frame": "table",
		"poses": []any{map[string]any{"position": []float64{800, 0, 800}, "orientation": []float64{0, 0, 0}}}})
	if res.IsError || !strings.Contains(text, "count=1") {
		t.Fatalf("unexpected result %q", text)
	}
	if !strings.Contains(string((*got)[0].Body), `"position":[800,0,800]`) {
		t.Errorf("unexpected body %s", (*got)[0].Body)
	}

	res, text = call(t, cs, "localize_poses_from_world", map[string]any{"cell": "cell", "dataset": "default", "frame": "table",
		"poses": []any{map[string]any{"position": []float64{1, 2}, "orientation": []float64{0, 0, 0}}}})
	if !res.IsError || !strings.HasPrefix(text, "INVALID_ARGUMENT:") {
		t.Errorf("expected INVALID_ARGUMENT for a 2-element position, got %q", text)
	}
	if len(*got) != 1 {
		t.Error("an invalid pose must not reach the API")
	}
}

func TestCreateDataset_ValidatesBeforeSending(t *testing.T) {
	c, got := fake(t, nil)
	cs := connect(t, c, false)
	for name, args := range map[string]map[string]any{
		"bad id":        {"cell": "cell", "dataset": "1abc"},
		"unknown frame": {"cell": "cell", "dataset": "a", "poses": []any{map[string]any{"dataset_pose": "p", "frame": "nope", "pose": map[string]any{"position": []float64{0, 0, 0}, "orientation": []float64{0, 0, 0}}}}},
		"duplicate":     {"cell": "cell", "dataset": "a", "poses": []any{map[string]any{"dataset_pose": "p", "pose": map[string]any{"position": []float64{0, 0, 0}, "orientation": []float64{0, 0, 0}}}, map[string]any{"dataset_pose": "p", "pose": map[string]any{"position": []float64{0, 0, 0}, "orientation": []float64{0, 0, 0}}}}},
	} {
		res, text := call(t, cs, "create_dataset", args)
		if !res.IsError || !strings.HasPrefix(text, "INVALID_ARGUMENT:") {
			t.Errorf("%s: expected INVALID_ARGUMENT, got %q", name, text)
		}
	}
	if len(*got) != 0 {
		t.Errorf("invalid input reached the API: %+v", *got)
	}
}

func TestCreateDataset_ConflictAndBody(t *testing.T) {
	c, got := fake(t, map[string]func(http.ResponseWriter){
		"GET /experimental/cells/cell/datasets":  jsonAnswer(200, `[]`),
		"POST /experimental/cells/cell/datasets": jsonAnswer(409, `{"message": "dataset exists"}`),
	})
	res, text := call(t, connect(t, c, false), "create_dataset", map[string]any{"cell": "cell", "dataset": "a",
		"frames": []any{map[string]any{"frame": "table", "pose": map[string]any{"position": []float64{1, 2, 3}, "orientation": []float64{0, 0, 0}}}},
		"poses":  []any{map[string]any{"dataset_pose": "p1", "frame": "table", "pose": map[string]any{"position": []float64{0, 0, 0}, "orientation": []float64{0, 0, 0}}}}})
	if !res.IsError || !strings.HasPrefix(text, "ALREADY_EXISTS:") {
		t.Errorf("expected ALREADY_EXISTS, got %q", text)
	}
	if (*got)[0].Query != "dataset=a" {
		t.Errorf("expected an existence check first, got %+v", (*got)[0])
	}
	var body map[string]any
	_ = json.Unmarshal((*got)[1].Body, &body)
	pose := body["poses"].([]any)[0].(map[string]any)
	frame := body["frames"].([]any)[0].(map[string]any)
	if pose["dataset"] != "a" || pose["frame"] != "table" || frame["dataset"] != "a" {
		t.Errorf("unexpected body %s", (*got)[0].Body)
	}
	if _, ok := frame["reference_frame"]; ok {
		t.Errorf("an empty reference frame must be omitted: %s", (*got)[1].Body)
	}
}

// POST on an existing id silently saves a new revision (verified live), so the tool
// checks first and only does that with new_revision.
func TestCreateDataset_ExistingNeedsNewRevision(t *testing.T) {
	c, got := fake(t, map[string]func(http.ResponseWriter){
		"GET /experimental/cells/cell/datasets": jsonAnswer(200, `[
			{"dataset": "a", "revision": 1, "created_at": "t", "updated_at": "u"},
			{"dataset": "a", "revision": 2, "created_at": "t", "updated_at": "u"}]`),
		"POST /experimental/cells/cell/datasets": jsonAnswer(201, `{"dataset": "a", "revision": 3, "poses": [], "frames": []}`),
	})
	cs := connect(t, c, false)
	res, text := call(t, cs, "create_dataset", map[string]any{"cell": "cell", "dataset": "a"})
	if !res.IsError || !strings.HasPrefix(text, "ALREADY_EXISTS:") || !strings.Contains(text, "latest revision 2") {
		t.Fatalf("expected ALREADY_EXISTS naming revision 2, got %q", text)
	}
	if len(*got) != 1 {
		t.Fatalf("nothing may be written without new_revision: %+v", *got)
	}
	res, text = call(t, cs, "create_dataset", map[string]any{"cell": "cell", "dataset": "a", "new_revision": true})
	if res.IsError || !strings.Contains(text, "revision=3") {
		t.Errorf("expected revision 3, got %q", text)
	}
}

func TestDeleteDataset_ReportsRemainingRevisions(t *testing.T) {
	c, _ := fake(t, map[string]func(http.ResponseWriter){
		"DELETE /experimental/cells/cell/datasets/a": func(w http.ResponseWriter) { w.WriteHeader(http.StatusNoContent) },
		"GET /experimental/cells/cell/datasets":      jsonAnswer(200, `[{"dataset": "a", "revision": 1, "created_at": "t", "updated_at": "u"}]`),
	})
	res, text := call(t, connect(t, c, false), "delete_dataset", map[string]any{"cell": "cell", "dataset": "a"})
	if res.IsError || !strings.Contains(text, "remaining_revisions=1") || !strings.Contains(text, "revision=latest") {
		t.Errorf("unexpected result %q", text)
	}
}

func TestDeleteDataset_ReadOnlyHidesWrites(t *testing.T) {
	c, _ := fake(t, nil)
	cs := connect(t, c, true)
	tools, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tl := range tools.Tools {
		if tl.Name == "create_dataset" || tl.Name == "delete_dataset" {
			t.Errorf("%s registered under read-only", tl.Name)
		}
		if !tl.Annotations.ReadOnlyHint {
			t.Errorf("%s is served read-only but not annotated so", tl.Name)
		}
	}
	if len(tools.Tools) != 6 {
		t.Errorf("expected 6 read tools, got %d", len(tools.Tools))
	}
}

func TestDeleteDataset_NotFound(t *testing.T) {
	c, got := fake(t, nil)
	res, text := call(t, connect(t, c, false), "delete_dataset", map[string]any{"cell": "cell", "dataset": "gone", "revision": 2})
	if !res.IsError || text != "NOT_FOUND: delete_dataset: revision 2 of dataset 'gone' not found in cell 'cell'" {
		t.Errorf("unexpected result %q", text)
	}
	if (*got)[0].Method != http.MethodDelete || (*got)[0].Query != "revision=2" {
		t.Errorf("unexpected request %+v", (*got)[0])
	}
}

// TestLiveDatasets creates a dataset, reads and transforms it, and deletes it again.
func TestLiveDatasets(t *testing.T) {
	c := livetest.Client(t)
	cell := livetest.Cell()
	cs := connect(t, c, false)
	id := "mcp-test-" + time.Now().Format("150405")

	res, text := call(t, cs, "list_datasets", map[string]any{"cell": cell})
	if res.IsError {
		t.Fatal(text)
	}
	t.Log(text)

	res, text = call(t, cs, "create_dataset", map[string]any{"cell": cell, "dataset": id, "name": "mcp test",
		"frames": []any{map[string]any{"frame": "table", "pose": map[string]any{"position": []float64{500, 0, 0}, "orientation": []float64{0, 0, 0}}}},
		"poses": []any{
			map[string]any{"dataset_pose": "p1", "frame": "table", "pose": map[string]any{"position": []float64{100, 0, 0}, "orientation": []float64{0, 0, 0}}},
			map[string]any{"dataset_pose": "p2", "pose": map[string]any{"position": []float64{0, 0, 500}, "orientation": []float64{0, 3.141, 0}}},
		}})
	if res.IsError {
		t.Fatal(text)
	}
	t.Log(text)
	t.Cleanup(func() {
		for range 5 { // every revision
			_, text := call(t, cs, "delete_dataset", map[string]any{"cell": cell, "dataset": id})
			t.Log(text)
			if !strings.HasPrefix(text, "OK:") || strings.Contains(text, "remaining_revisions=0") {
				return
			}
		}
	})

	for _, step := range []struct {
		name string
		args map[string]any
	}{
		{"describe_dataset", map[string]any{"cell": cell, "dataset": id}},
		{"list_dataset_poses", map[string]any{"cell": cell, "dataset": id}},
		{"resolve_poses_to_world", map[string]any{"cell": cell, "dataset": id, "frame": "table",
			"poses": []any{map[string]any{"position": []float64{100, 0, 0}, "orientation": []float64{0, 0, 0}}}}},
		{"localize_poses_from_world", map[string]any{"cell": cell, "dataset": id, "frame": "table",
			"poses": []any{map[string]any{"position": []float64{600, 0, 0}, "orientation": []float64{0, 0, 0}}}}},
		{"create_dataset", map[string]any{"cell": cell, "dataset": id}},
	} {
		res, text := call(t, cs, step.name, step.args)
		raw, _ := json.Marshal(res.StructuredContent)
		t.Logf("%s: %s %s", step.name, text, raw)
		if step.name == "create_dataset" {
			if !strings.HasPrefix(text, "ALREADY_EXISTS:") {
				t.Errorf("expected ALREADY_EXISTS on a second create, got %q", text)
			}
			continue
		}
		if res.IsError {
			t.Errorf("%s: %s", step.name, text)
		}
	}

	res, text = call(t, cs, "resolve_poses_to_world", map[string]any{"cell": cell, "dataset": id, "frame": "table",
		"poses": []any{map[string]any{"position": []float64{100, 0, 0}, "orientation": []float64{0, 0, 0}}}})
	var out struct {
		Poses []datasets.Pose `json:"poses"`
	}
	structured(t, res, &out)
	if len(out.Poses) != 1 || out.Poses[0].Position[0] < 599 || out.Poses[0].Position[0] > 601 {
		t.Errorf("expected (100,0,0) in a frame at x=500 to resolve to x=600, got %+v (%s)", out.Poses, text)
	}

	res, text = call(t, cs, "describe_dataset", map[string]any{"cell": cell, "dataset": "mcp-test-does-not-exist"})
	if !strings.HasPrefix(text, "NOT_FOUND:") {
		t.Errorf("expected NOT_FOUND, got %q", text)
	}
}
