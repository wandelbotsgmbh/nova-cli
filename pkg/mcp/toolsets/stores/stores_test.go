package stores_test

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
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/toolsets/stores"
)

func connect(t *testing.T, c *novaapi.Client, readOnly bool) *sdk.ClientSession {
	t.Helper()
	server := sdk.NewServer(&sdk.Implementation{Name: "test", Version: "test"}, nil)
	for _, reg := range stores.Tools(tool.Env{Client: c}) {
		if reg.Toolset != tool.Stores {
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
	Method, Path string
	Header       http.Header
	Body         []byte
}

func fake(t *testing.T, answers map[string]func(w http.ResponseWriter)) (*novaapi.Client, *[]request) {
	t.Helper()
	var got []request
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got = append(got, request{r.Method, r.URL.EscapedPath(), r.Header, body})
		if answer, ok := answers[r.Method+" "+r.URL.EscapedPath()]; ok {
			answer(w)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(server.Close)
	return novaapi.NewClient(server.URL, nil, nil, nil), &got
}

func answer(status int, contentType, body string, headers ...string) func(http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		for i := 0; i+1 < len(headers); i += 2 {
			w.Header().Set(headers[i], headers[i+1])
		}
		if contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}
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

func TestKindIsAnEnumInEverySchema(t *testing.T) {
	c, _ := fake(t, nil)
	tools, err := connect(t, c, false).ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) != 4 {
		t.Fatalf("expected 4 tools, got %d", len(tools.Tools))
	}
	for _, tl := range tools.Tools {
		raw, _ := json.Marshal(tl.InputSchema)
		if !strings.Contains(string(raw), `"enum":["collider","collision_tool","link_chain","collision_setup","object"]`) {
			t.Errorf("%s: kind is not an enum: %s", tl.Name, raw)
		}
	}
}

func TestListStoreItems_CollisionSummariesSortedAndPaged(t *testing.T) {
	c, _ := fake(t, map[string]func(http.ResponseWriter){
		"GET /cells/cell/store/collision/tools": answer(200, "application/json", `{
			"gripper": {"a": {"shape": {"shape_type": "box", "size_x": 1}}, "b": {"shape": {"shape_type": "sphere", "radius": 2}}, "c": {"shape": {"shape_type": "box"}}},
			"anvil": {"x": {"shape": {"shape_type": "cylinder"}}},
			"zz": {}}`),
	})
	res, text := call(t, connect(t, c, true), "list_store_items", map[string]any{"cell": "cell", "kind": "collision_tool", "limit": 2})
	if res.IsError || !strings.Contains(text, "total=3") || !strings.Contains(text, "next_offset=2") {
		t.Fatalf("unexpected result %q", text)
	}
	var out struct {
		Items []stores.Summary `json:"items"`
	}
	structured(t, res, &out)
	if len(out.Items) != 2 || out.Items[0].Key != "anvil" || out.Items[1].Summary != "3 colliders (2 box, 1 sphere)" {
		t.Errorf("unexpected items %+v", out.Items)
	}
}

func TestListStoreItems_ObjectsPrefixAndMissingStore(t *testing.T) {
	c, _ := fake(t, map[string]func(http.ResponseWriter){
		"GET /cells/cell/store/objects": answer(200, "application/json", `["trees/1", "app/b", "app/a"]`),
	})
	res, _ := call(t, connect(t, c, false), "list_store_items", map[string]any{"cell": "cell", "kind": "object", "prefix": "app/"})
	var out struct {
		Items []stores.Summary `json:"items"`
	}
	structured(t, res, &out)
	if len(out.Items) != 2 || out.Items[0].Key != "app/a" {
		t.Errorf("unexpected items %+v", out.Items)
	}

	empty, _ := fake(t, nil) // 404: the store is created on first write
	res, text := call(t, connect(t, empty, false), "list_store_items", map[string]any{"cell": "cell", "kind": "object"})
	if res.IsError || !strings.Contains(text, "total=0") {
		t.Errorf("expected an empty list, got %q", text)
	}
}

func TestUnknownKind(t *testing.T) {
	c, got := fake(t, nil)
	res, text := call(t, connect(t, c, false), "get_store_item", map[string]any{"cell": "cell", "kind": "robot", "key": "x"})
	// the SDK rejects it against the enum before the handler runs
	if !res.IsError || !strings.Contains(text, "robot") || len(*got) != 0 {
		t.Errorf("expected a refusal without a request, got %q", text)
	}
}

func TestGetStoreItem_CollisionBodyAndCap(t *testing.T) {
	big := `{"shape": {"shape_type": "convex_hull", "vertices": [` + strings.Repeat("[1.5,2.5,3.5],", 3000) + `[0,0,0]]}}`
	c, _ := fake(t, map[string]func(http.ResponseWriter){
		"GET /cells/cell/store/collision/colliders/table": answer(200, "application/json", `{"shape": {"shape_type": "box", "size_x": 1, "size_y": 2, "size_z": 3, "box_type": "FULL"}, "margin": 5}`),
		"GET /cells/cell/store/collision/colliders/hull":  answer(200, "application/json", big),
	})
	cs := connect(t, c, false)
	res, text := call(t, cs, "get_store_item", map[string]any{"cell": "cell", "kind": "collider", "key": "table"})
	var out struct {
		Summary     string         `json:"summary"`
		Body        map[string]any `json:"body"`
		BodyOmitted string         `json:"body_omitted"`
	}
	structured(t, res, &out)
	if res.IsError || out.Summary != "box, margin 5 mm" || out.Body["margin"] != 5.0 {
		t.Errorf("unexpected result %q %+v", text, out)
	}

	res, _ = call(t, cs, "get_store_item", map[string]any{"cell": "cell", "kind": "collider", "key": "hull"})
	out.Body = nil
	structured(t, res, &out)
	if out.Body != nil || out.BodyOmitted == "" || out.Summary != "convex_hull" {
		t.Errorf("a body above 32 KiB must be omitted with a summary, got %+v", out)
	}

	res, text = call(t, cs, "get_store_item", map[string]any{"cell": "cell", "kind": "collider", "key": "nope"})
	if !res.IsError || text != "NOT_FOUND: get_store_item: collider 'nope' not found in cell 'cell'" {
		t.Errorf("unexpected result %q", text)
	}
}

func TestGetStoreItem_Objects(t *testing.T) {
	c, got := fake(t, map[string]func(http.ResponseWriter){
		"HEAD /cells/cell/store/objects/app/config.json": answer(200, "application/json", "", "Content-Length", "13", "X-Metadata", "alias,My+Conf,is_hidden,false"),
		"GET /cells/cell/store/objects/app/config.json":  answer(200, "application/json", `{"a": [1, 2]}`),
		"HEAD /cells/cell/store/objects/app/blob":        answer(200, "application/octet-stream", "", "Content-Length", "4"),
		"GET /cells/cell/store/objects/app/blob":         answer(200, "application/octet-stream", "\x00\xff\x00\xff"),
		"HEAD /cells/cell/store/objects/app/huge":        answer(200, "application/octet-stream", "", "Content-Length", "50000000"),
	})
	cs := connect(t, c, false)
	type objectOut struct {
		Body        any               `json:"body"`
		Content     string            `json:"content"`
		Metadata    map[string]string `json:"metadata"`
		Size        int               `json:"size"`
		BodyOmitted string            `json:"body_omitted"`
	}
	res, text := call(t, cs, "get_store_item", map[string]any{"cell": "cell", "kind": "object", "key": "app/config.json"})
	var out objectOut
	structured(t, res, &out)
	if res.IsError || out.Body == nil || out.Metadata["alias"] != "My Conf" || out.Metadata["is_hidden"] != "false" {
		t.Errorf("unexpected JSON object result %q %+v", text, out)
	}

	res, _ = call(t, cs, "get_store_item", map[string]any{"cell": "cell", "kind": "object", "key": "app/blob"})
	out = objectOut{}
	structured(t, res, &out)
	if out.Content != "" || out.Body != nil || !strings.Contains(out.BodyOmitted, "binary") {
		t.Errorf("binary content must not be returned, got %+v", out)
	}

	before := len(*got)
	res, _ = call(t, cs, "get_store_item", map[string]any{"cell": "cell", "kind": "object", "key": "app/huge"})
	out = objectOut{}
	structured(t, res, &out)
	if out.Size != 50000000 || out.BodyOmitted == "" || len(*got) != before+1 {
		t.Errorf("a large object must be described from HEAD only, got %+v after %d requests", out, len(*got)-before)
	}
}

func TestPutStoreItem_ValidatesCollisionBodies(t *testing.T) {
	c, got := fake(t, nil)
	cs := connect(t, c, false)
	for name, args := range map[string]map[string]any{
		"collider without shape": {"kind": "collider", "key": "a", "body": map[string]any{"pose": map[string]any{}}},
		"sphere without radius":  {"kind": "collider", "key": "a", "body": map[string]any{"shape": map[string]any{"shape_type": "sphere"}}},
		"unknown shape":          {"kind": "collider", "key": "a", "body": map[string]any{"shape": map[string]any{"shape_type": "torus"}}},
		"link chain as object":   {"kind": "link_chain", "key": "a", "body": map[string]any{"x": 1}},
		"tool with bad collider": {"kind": "collision_tool", "key": "a", "body": map[string]any{"c": map[string]any{"shape": map[string]any{"shape_type": "box"}}}},
		"setup with unknown":     {"kind": "collision_setup", "key": "a", "body": map[string]any{"colider": map[string]any{}}},
		"collider with metadata": {"kind": "collider", "key": "a", "body": map[string]any{"shape": map[string]any{"shape_type": "plane"}}, "metadata": map[string]any{"a": "b"}},
		"key with slash":         {"kind": "collider", "key": "a/b", "body": map[string]any{"shape": map[string]any{"shape_type": "plane"}}},
		"object without content": {"kind": "object", "key": "app/x"},
		"object with both":       {"kind": "object", "key": "app/x", "content": "a", "body": map[string]any{}},
		"object metadata comma":  {"kind": "object", "key": "app/x", "content": "a", "metadata": map[string]any{"a": "b,c"}},
		"object key with dotdot": {"kind": "object", "key": "app/../x", "content": "a"},
	} {
		args["cell"] = "cell"
		res, text := call(t, cs, "put_store_item", args)
		if !res.IsError || !strings.HasPrefix(text, "INVALID_ARGUMENT:") {
			t.Errorf("%s: expected INVALID_ARGUMENT, got %q", name, text)
		}
	}
	if len(*got) != 0 {
		t.Errorf("invalid input reached the API: %d requests", len(*got))
	}
}

func TestPutStoreItem_ProtectedObjects(t *testing.T) {
	c, got := fake(t, nil)
	cs := connect(t, c, false)
	for _, key := range []string{"trees/flow-1", "datasets/default/1"} {
		res, text := call(t, cs, "put_store_item", map[string]any{"cell": "cell", "kind": "object", "key": key, "content": "x"})
		if !res.IsError || !strings.HasPrefix(text, "FORBIDDEN:") {
			t.Errorf("%s: expected FORBIDDEN, got %q", key, text)
		}
		res, text = call(t, cs, "delete_store_item", map[string]any{"cell": "cell", "kind": "object", "key": key})
		if !res.IsError || !strings.HasPrefix(text, "FORBIDDEN:") {
			t.Errorf("%s: expected FORBIDDEN on delete, got %q", key, text)
		}
	}
	if len(*got) != 0 {
		t.Errorf("protected keys reached the API: %d requests", len(*got))
	}
}

func TestPutStoreItem_SendsCollisionAndObject(t *testing.T) {
	c, got := fake(t, map[string]func(http.ResponseWriter){
		"PUT /cells/cell/store/collision/link-chains/ur5e": answer(200, "application/json", `[]`),
		"PUT /cells/cell/store/objects/app/notes.txt":      answer(204, "", ""),
	})
	cs := connect(t, c, false)
	res, text := call(t, cs, "put_store_item", map[string]any{"cell": "cell", "kind": "link_chain", "key": "ur5e",
		"body": []any{map[string]any{"base": map[string]any{"shape": map[string]any{"shape_type": "cylinder", "radius": 50, "height": 100}}}, map[string]any{}}})
	if res.IsError {
		t.Fatal(text)
	}
	if !strings.HasPrefix(string((*got)[0].Body), `[{"base"`) {
		t.Errorf("unexpected body %s", (*got)[0].Body)
	}

	res, text = call(t, cs, "put_store_item", map[string]any{"cell": "cell", "kind": "object", "key": "app/notes.txt",
		"content": "hello", "metadata": map[string]any{"b": "2", "a": "1"}})
	if res.IsError || !strings.Contains(text, "size=5") {
		t.Fatalf("unexpected result %q", text)
	}
	req := (*got)[1]
	if !strings.HasPrefix(req.Header.Get("Content-Type"), "multipart/form-data") || req.Header.Get("X-Metadata") != "a,1,b,2" ||
		!strings.Contains(string(req.Body), `name="any_value"; filename="notes.txt"`) || !strings.Contains(string(req.Body), "hello") {
		t.Errorf("unexpected upload %v %s", req.Header, req.Body)
	}
}

func TestDeleteStoreItem_ChecksExistence(t *testing.T) {
	c, got := fake(t, map[string]func(http.ResponseWriter){
		"GET /cells/cell/store/collision/setups/s":     answer(200, "application/json", `{}`),
		"DELETE /cells/cell/store/collision/setups/s":  answer(204, "", ""),
		"DELETE /cells/cell/store/objects/app/missing": answer(204, "", ""), // the store answers 204 for unknown keys
	})
	cs := connect(t, c, false)
	res, text := call(t, cs, "delete_store_item", map[string]any{"cell": "cell", "kind": "collision_setup", "key": "s"})
	if res.IsError || !strings.HasPrefix(text, "OK:") {
		t.Errorf("unexpected result %q", text)
	}
	res, text = call(t, cs, "delete_store_item", map[string]any{"cell": "cell", "kind": "object", "key": "app/missing"})
	if !res.IsError || !strings.HasPrefix(text, "NOT_FOUND:") {
		t.Errorf("expected NOT_FOUND for a missing object, got %q", text)
	}
	for _, r := range *got {
		if r.Method == http.MethodDelete && r.Path == "/cells/cell/store/objects/app/missing" {
			t.Error("a missing object must not be deleted")
		}
	}
}

func TestReadOnly(t *testing.T) {
	c, _ := fake(t, nil)
	tools, err := connect(t, c, true).ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tl := range tools.Tools {
		if !tl.Annotations.ReadOnlyHint {
			t.Errorf("%s is served read-only but not annotated so", tl.Name)
		}
	}
	if len(tools.Tools) != 2 {
		t.Errorf("expected list and get only, got %d tools", len(tools.Tools))
	}
}

// TestLiveStores writes, reads and deletes its own mcp-test-* items of every kind.
func TestLiveStores(t *testing.T) {
	c := livetest.Client(t)
	cell := livetest.Cell()
	cs := connect(t, c, false)
	suffix := time.Now().Format("150405")
	sphere := map[string]any{"shape": map[string]any{"shape_type": "sphere", "radius": 25}}
	items := []struct {
		kind, key string
		args      map[string]any
	}{
		{"collider", "mcp-test-collider-" + suffix, map[string]any{"body": sphere}},
		{"collision_tool", "mcp-test-tool-" + suffix, map[string]any{"body": map[string]any{"tip": sphere}}},
		{"link_chain", "mcp-test-chain-" + suffix, map[string]any{"body": []any{map[string]any{"base": sphere}, map[string]any{}}}},
		{"collision_setup", "mcp-test-setup-" + suffix, map[string]any{"body": map[string]any{"colliders": map[string]any{"ball": sphere}, "self_collision_detection": false}}},
		{"object", "mcp-test/" + suffix + "/notes.json", map[string]any{"body": map[string]any{"hello": "world"}, "metadata": map[string]any{"alias": "mcp test"}}},
		{"object", "mcp-test/" + suffix + "/notes.txt", map[string]any{"content": "plain text"}},
	}
	for _, item := range items {
		args := map[string]any{"cell": cell, "kind": item.kind, "key": item.key}
		for k, v := range item.args {
			args[k] = v
		}
		res, text := call(t, cs, "put_store_item", args)
		t.Logf("put %s %s: %s", item.kind, item.key, text)
		if res.IsError {
			t.Errorf("put %s: %s", item.kind, text)
			continue
		}
		t.Cleanup(func() {
			_, text := call(t, cs, "delete_store_item", map[string]any{"cell": cell, "kind": item.kind, "key": item.key})
			t.Logf("delete %s %s: %s", item.kind, item.key, text)
			if !strings.HasPrefix(text, "OK:") {
				t.Errorf("cleanup of %s %s failed: %s", item.kind, item.key, text)
			}
		})

		res, text = call(t, cs, "get_store_item", map[string]any{"cell": cell, "kind": item.kind, "key": item.key})
		raw, _ := json.Marshal(res.StructuredContent)
		t.Logf("get: %s %s", text, raw)
		if res.IsError {
			t.Errorf("get %s: %s", item.kind, text)
		}
		if item.args["metadata"] != nil && !strings.Contains(string(raw), `"alias":"mcp test"`) {
			t.Errorf("metadata didn't round-trip: %s", raw)
		}

		res, text = call(t, cs, "list_store_items", map[string]any{"cell": cell, "kind": item.kind, "prefix": "mcp-test"})
		raw, _ = json.Marshal(res.StructuredContent)
		t.Logf("list: %s %s", text, raw)
		if res.IsError || !strings.Contains(string(raw), item.key) {
			t.Errorf("list %s doesn't contain %s: %s", item.kind, item.key, raw)
		}
	}

	_, text := call(t, cs, "get_store_item", map[string]any{"cell": cell, "kind": "object", "key": "mcp-test/does-not-exist"})
	if !strings.HasPrefix(text, "NOT_FOUND:") {
		t.Errorf("expected NOT_FOUND, got %q", text)
	}
	_, text = call(t, cs, "delete_store_item", map[string]any{"cell": cell, "kind": "collider", "key": "mcp-test-does-not-exist"})
	if !strings.HasPrefix(text, "NOT_FOUND:") {
		t.Errorf("expected NOT_FOUND, got %q", text)
	}
}
