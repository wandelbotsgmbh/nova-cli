package apps_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/internal/livetest"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/novaapi"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/tool"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/toolsets/apps"
)

func connect(t *testing.T, c *novaapi.Client) *sdk.ClientSession {
	t.Helper()
	server := sdk.NewServer(&sdk.Implementation{Name: "test", Version: "test"}, nil)
	for _, reg := range apps.Tools(tool.Env{Client: c}) {
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

type fakeAPI struct {
	mu      sync.Mutex
	answers map[string]string
	bodies  map[string]string
}

func newFake(t *testing.T, answers map[string]string) (*fakeAPI, *novaapi.Client) {
	t.Helper()
	f := &fakeAPI{answers: answers, bodies: map[string]string{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Method + " " + r.URL.Path
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.bodies[key] = string(body)
		answer, ok := f.answers[key]
		f.mu.Unlock()
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		status, out, _ := strings.Cut(answer, " ")
		w.Header().Set("Content-Type", "application/json")
		if status == "202" {
			w.WriteHeader(http.StatusAccepted)
		}
		_, _ = w.Write([]byte(out))
	}))
	t.Cleanup(srv.Close)
	return f, novaapi.NewClient(srv.URL, nil, nil, nil)
}

func (f *fakeAPI) body(key string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.bodies[key]
}

func call(t *testing.T, cs *sdk.ClientSession, name string, args map[string]any) (bool, string, string) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	raw, _ := json.Marshal(res.StructuredContent)
	return res.IsError, res.Content[0].(*sdk.TextContent).Text, string(raw)
}

const appWithSecrets = `{"name":"web","app_icon":"icon.png","port":3000,
	"container_image":{"image":"reg.example/web:1","credentials":{"registry":"reg.example","user":"bot","password":"REGISTRY-PASSWORD"},"secrets":[{"name":"pull-secret"}]},
	"environment":[{"name":"API_KEY","value":"ENV-SECRET"},{"name":"MODE","value":"prod"}],
	"storage":{"mount_path":"/data","capacity":"100Mi"},"resources":{"memory_limit":"500Mi"}}`

func TestDescribeApp_NeverReturnsEnvironmentValuesOrCredentials(t *testing.T) {
	_, c := newFake(t, map[string]string{
		"GET /cells/c1/apps/web": "200 " + appWithSecrets,
		"GET /cells/c1/status":   `200 {"operating_state":"ACTIVE","service_status":[{"service":"web","group":"App","status":{"severity":"ERROR","code":"ImagePullBackOff"}}]}`,
	})
	cs := connect(t, c)
	isErr, text, data := call(t, cs, "describe_app", map[string]any{"cell": "c1", "app": "web"})
	if isErr {
		t.Fatal(text)
	}
	for _, secret := range []string{"ENV-SECRET", "prod", "REGISTRY-PASSWORD", "bot", "credentials"} {
		if strings.Contains(text+data, secret) {
			t.Errorf("leaked %q: %s %s", secret, text, data)
		}
	}
	for _, want := range []string{`"environment_names":["API_KEY","MODE"]`, `"state":"ImagePullBackOff"`, `"path":"/c1/web"`, `"pull_secrets":["pull-secret"]`, `"storage_capacity":"100Mi"`} {
		if !strings.Contains(data, want) {
			t.Errorf("missing %s in %s", want, data)
		}
	}
}

func TestUpdateApp_ChangesOnlyTheGivenFields(t *testing.T) {
	f, c := newFake(t, map[string]string{
		"GET /cells/c1/apps/web": "200 " + appWithSecrets,
		"PUT /cells/c1/apps/web": "202 ",
	})
	cs := connect(t, c)
	isErr, text, data := call(t, cs, "update_app", map[string]any{
		"cell": "c1", "app": "web", "image": "reg.example/web:2",
		"set_environment": []map[string]string{{"name": "MODE", "value": "dev"}, {"name": "NEW", "value": "1"}},
	})
	if isErr || !strings.Contains(text, "status=pending") {
		t.Fatalf("unexpected: %s", text)
	}
	if strings.Contains(text+data, "ENV-SECRET") || strings.Contains(text+data, "REGISTRY-PASSWORD") {
		t.Errorf("result leaked a secret: %s %s", text, data)
	}
	var sent map[string]any
	if err := json.Unmarshal([]byte(f.body("PUT /cells/c1/apps/web")), &sent); err != nil {
		t.Fatal(err)
	}
	image := sent["container_image"].(map[string]any)
	if image["image"] != "reg.example/web:2" {
		t.Errorf("image not updated: %v", image)
	}
	if creds, _ := image["credentials"].(map[string]any); creds["password"] != "REGISTRY-PASSWORD" {
		t.Errorf("existing credentials must be sent back unchanged: %v", image)
	}
	env, _ := json.Marshal(sent["environment"])
	if string(env) != `[{"name":"API_KEY","value":"ENV-SECRET"},{"name":"MODE","value":"dev"},{"name":"NEW","value":"1"}]` {
		t.Errorf("unexpected environment %s", env)
	}
	if sent["port"] != float64(3000) || sent["storage"] == nil {
		t.Errorf("untouched fields must be kept: %v", sent)
	}
}

func TestUpdateApp_DropsEmptyCredentialsAndRemovesEnv(t *testing.T) {
	f, c := newFake(t, map[string]string{
		"GET /cells/c1/apps/web": `200 {"name":"web","app_icon":"i","container_image":{"image":"x","credentials":{"registry":"","user":"","password":""},"secrets":[{"name":"s"}]},"environment":[{"name":"A","value":"1"}]}`,
		"PUT /cells/c1/apps/web": "200 ",
	})
	cs := connect(t, c)
	isErr, text, _ := call(t, cs, "update_app", map[string]any{"cell": "c1", "app": "web", "remove_environment": []string{"A"}})
	if isErr || !strings.Contains(text, "status=completed") {
		t.Fatalf("unexpected: %s", text)
	}
	body := f.body("PUT /cells/c1/apps/web")
	if strings.Contains(body, "credentials") || !strings.Contains(body, `"environment":[]`) || !strings.Contains(body, `"secrets":[{"name":"s"}]`) {
		t.Errorf("unexpected body %s", body)
	}
}

func TestUpdateApp_NothingToChange(t *testing.T) {
	_, c := newFake(t, nil)
	isErr, text, _ := call(t, connect(t, c), "update_app", map[string]any{"cell": "c1", "app": "web"})
	if !isErr || !strings.HasPrefix(text, "INVALID_ARGUMENT") {
		t.Errorf("unexpected: %s", text)
	}
}

func TestAddApp(t *testing.T) {
	f, c := newFake(t, map[string]string{"POST /cells/c1/apps": "202 "})
	cs := connect(t, c)
	isErr, text, _ := call(t, cs, "add_app", map[string]any{"cell": "c1", "name": "Bad_Name", "image": "x"})
	if !isErr || !strings.HasPrefix(text, "INVALID_ARGUMENT") {
		t.Errorf("unexpected: %s", text)
	}
	isErr, text, _ = call(t, cs, "add_app", map[string]any{"cell": "c1", "name": "web", "image": "x", "storage_mount_path": "/data"})
	if !isErr || !strings.Contains(text, "go together") {
		t.Errorf("unexpected: %s", text)
	}
	isErr, text, _ = call(t, cs, "add_app", map[string]any{"cell": "c1", "name": "web", "image": "reg/web:1", "pull_secrets": []string{"s1"}})
	if isErr || !strings.Contains(text, "status=pending") {
		t.Fatalf("unexpected: %s", text)
	}
	want := `{"app_icon":"app_icon.png","container_image":{"image":"reg/web:1","secrets":[{"name":"s1"}]},"name":"web","port":8080}`
	if body := f.body("POST /cells/c1/apps"); body != want {
		t.Errorf("unexpected body\n got %s\nwant %s", body, want)
	}
}

func TestListApps_Paginated(t *testing.T) {
	_, c := newFake(t, map[string]string{"GET /cells/c1/apps": `200 ["c","a","b"]`})
	isErr, text, data := call(t, connect(t, c), "list_apps", map[string]any{"cell": "c1", "limit": 2})
	if isErr || !strings.Contains(data, `"apps":["a","b"]`) || !strings.Contains(text, "next_offset=2") {
		t.Errorf("unexpected: %s %s", text, data)
	}
}

// TestLiveAppLifecycle installs mcp-test-app with the image of the existing app "test"
// (pullable with the instance's secrets), updates its environment and deletes it.
func TestLiveAppLifecycle(t *testing.T) {
	cell := livetest.Cell()
	cs := connect(t, livetest.Client(t))

	isErr, text, data := call(t, cs, "list_apps", map[string]any{"cell": cell})
	if isErr {
		t.Fatal(text)
	}
	t.Log(text, data)
	var listed struct {
		Apps []string `json:"apps"`
	}
	_ = json.Unmarshal([]byte(data), &listed)
	var source string
	for _, a := range listed.Apps {
		if !strings.HasPrefix(a, "mcp-test-") {
			source = a
		}
	}
	if source == "" {
		t.Skip("no existing app to copy the image from")
	}
	isErr, text, data = call(t, cs, "describe_app", map[string]any{"cell": cell, "app": source})
	if isErr {
		t.Fatal(text)
	}
	t.Log(text)
	var described struct {
		Image       string   `json:"image"`
		PullSecrets []string `json:"pull_secrets"`
	}
	_ = json.Unmarshal([]byte(data), &described)

	const app = "mcp-test-app"
	isErr, text, _ = call(t, cs, "add_app", map[string]any{"cell": cell, "name": app, "image": described.Image,
		"pull_secrets": described.PullSecrets, "environment": []map[string]string{{"name": "MCP_TEST", "value": "1"}}})
	if isErr {
		t.Fatal(text)
	}
	t.Log(text)
	t.Cleanup(func() {
		_, text, _ := call(t, cs, "delete_app", map[string]any{"cell": cell, "app": app})
		t.Log("cleanup: " + text)
	})

	waitState := func(want string) string {
		deadline := time.Now().Add(3 * time.Minute)
		for {
			_, text, _ := call(t, cs, "describe_app", map[string]any{"cell": cell, "app": app})
			if strings.Contains(text, "state="+want) {
				return text
			}
			if time.Now().After(deadline) {
				t.Fatalf("app not %s in time: %s", want, text)
			}
			time.Sleep(5 * time.Second)
		}
	}
	t.Log(waitState("Running"))

	isErr, text, _ = call(t, cs, "update_app", map[string]any{"cell": cell, "app": app,
		"set_environment": []map[string]string{{"name": "MCP_TEST", "value": "2"}, {"name": "MCP_EXTRA", "value": "x"}}})
	if isErr {
		t.Fatal(text)
	}
	t.Log(text)
	_, text, data = call(t, cs, "describe_app", map[string]any{"cell": cell, "app": app})
	if !strings.Contains(data, `"environment_names":["MCP_EXTRA","MCP_TEST"]`) || strings.Contains(data, `"2"`) {
		t.Errorf("unexpected after update: %s", data)
	}
	t.Log(waitState("Running"))
}
