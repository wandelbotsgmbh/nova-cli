package system_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/internal/livetest"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/novaapi"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/tool"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/toolsets/system"
)

func connect(t *testing.T, c *novaapi.Client) *sdk.ClientSession {
	t.Helper()
	server := sdk.NewServer(&sdk.Implementation{Name: "test", Version: "test"}, nil)
	for _, reg := range system.Tools(tool.Env{Client: c}) {
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

// fake serves fixed answers per "METHOD path?query".
func fake(t *testing.T, answers map[string]string) *novaapi.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Method + " " + r.URL.Path
		if r.URL.RawQuery != "" {
			key += "?" + r.URL.RawQuery
		}
		answer, ok := answers[key]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{}`))
			return
		}
		status, body, _ := strings.Cut(answer, " ")
		w.Header().Set("Content-Type", "application/json")
		switch status {
		case "200":
		case "204":
			w.WriteHeader(http.StatusNoContent)
			return
		case "404":
			w.WriteHeader(http.StatusNotFound)
		case "403":
			w.WriteHeader(http.StatusForbidden)
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return novaapi.NewClient(srv.URL, nil, nil, nil)
}

func call(t *testing.T, cs *sdk.ClientSession, name string, args map[string]any) (*sdk.CallToolResult, string, string) {
	t.Helper()
	if args == nil {
		args = map[string]any{}
	}
	res, err := cs.CallTool(context.Background(), &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	raw, _ := json.Marshal(res.StructuredContent)
	return res, res.Content[0].(*sdk.TextContent).Text, string(raw)
}

func TestGetSession_NeverReturnsTokenOrSessionID(t *testing.T) {
	cs := connect(t, fake(t, map[string]string{
		"GET /session": `200 {"session_id":"sess-secret","token":"tok-secret","access_token":"tok2","user":{"id":"u1","name":"Ada","email":"ada@example.com"},` +
			`"capabilities":[{"capability":"can_manage_cells"},{"capability":"can_access_system","resource_ids":["cell"]}],"issued_at":"2026-01-01T00:00:00Z","expires_at":"2026-01-02T00:00:00Z"}`,
	}))
	res, text, data := call(t, cs, "get_session", nil)
	if res.IsError {
		t.Fatal(text)
	}
	for _, secret := range []string{"sess-secret", "tok-secret", "tok2", "session_id"} {
		if strings.Contains(data+text, secret) {
			t.Errorf("leaked %q: %s %s", secret, text, data)
		}
	}
	if !strings.Contains(data, `"capabilities":[{"capability":"can_access_system","resource_ids":["cell"]},{"capability":"can_manage_cells"}]`) {
		t.Errorf("capabilities not projected sorted: %s", data)
	}
	if !strings.Contains(data, "ada@example.com") || !strings.Contains(data, "2026-01-02") {
		t.Errorf("identity or expiry missing: %s", data)
	}
}

func TestGetSystemStatus_ProblemsOnly(t *testing.T) {
	cs := connect(t, fake(t, map[string]string{
		"GET /system/status": `200 [{"service":"B","group":"SystemService","status":{"severity":"INFO","code":"Running"}},` +
			`{"service":"A","group":"SystemService","status":{"severity":"ERROR","code":"CrashLoopBackOff","reason":"oom"}}]`,
	}))
	_, text, data := call(t, cs, "get_system_status", nil)
	if !strings.Contains(text, "problems=1") || !strings.Contains(text, "services=2") || strings.Index(data, `"service":"A"`) > strings.Index(data, `"service":"B"`) {
		t.Errorf("unexpected: %s %s", text, data)
	}
	_, text, data = call(t, cs, "get_system_status", map[string]any{"problems_only": true})
	if !strings.Contains(text, "services=1") || strings.Contains(data, `"service":"B"`) {
		t.Errorf("problems_only kept healthy services: %s %s", text, data)
	}
}

func TestCheckSystemUpdate(t *testing.T) {
	cs := connect(t, fake(t, map[string]string{
		"GET /system/version":               `200 "26.8.0"`,
		"GET /system/update?channel=stable": `204 `,
		"GET /system/update?channel=next":   `200 26.9.0`,
	}))
	_, text, _ := call(t, cs, "check_system_update", nil)
	if !strings.HasPrefix(text, "OK: no update available") || !strings.Contains(text, "current=26.8.0") {
		t.Errorf("unexpected: %s", text)
	}
	_, text, data := call(t, cs, "check_system_update", map[string]any{"channel": "next"})
	if !strings.Contains(text, "available=26.9.0") || !strings.Contains(data, `"update_available":true`) {
		t.Errorf("unexpected: %s %s", text, data)
	}
	res, text, _ := call(t, cs, "check_system_update", map[string]any{"channel": "beta"})
	if !res.IsError || !strings.HasPrefix(text, "INVALID_ARGUMENT") {
		t.Errorf("expected INVALID_ARGUMENT, got %s", text)
	}
}

func TestGetNetworkInterfaces_NotAvailable(t *testing.T) {
	cs := connect(t, fake(t, map[string]string{
		"GET /system/network/interfaces": `404 {"code":"SystemInfoNotEnabled","message":"System info feature is not enabled."}`,
	}))
	res, text, _ := call(t, cs, "get_network_interfaces", nil)
	if !res.IsError || !strings.HasPrefix(text, "NOT_FOUND") || !strings.Contains(text, "not available") {
		t.Errorf("unexpected: %s", text)
	}
}

func TestLiveSystemTools(t *testing.T) {
	cs := connect(t, livetest.Client(t))
	for _, name := range []string{"get_system_status", "get_system_features", "check_system_update", "get_license_status",
		"get_network_interfaces", "get_cloud_status", "get_session"} {
		res, text, data := call(t, cs, name, nil)
		if res.IsError && !(name == "get_network_interfaces" && strings.HasPrefix(text, "NOT_FOUND")) {
			t.Errorf("%s: %s", name, text)
		}
		if strings.Contains(data, "session_id") {
			t.Errorf("%s leaked the session id", name)
		}
		t.Logf("%s: %s", name, text)
	}
}

// The API answers 403 with the license status when the license is not valid: that is
// the answer, not a refusal.
func TestGetLicenseStatus_InvalidLicenseIsAnAnswer(t *testing.T) {
	cs := connect(t, fake(t, map[string]string{"GET /license/status": `403 {"status":"EXPIRED","message":"expired on 2026-01-01"}`}))
	res, text, structured := call(t, cs, "get_license_status", nil)
	if res.IsError || !strings.Contains(structured, `"EXPIRED"`) {
		t.Errorf("expected the license status, got %q / %s", text, structured)
	}

	cs = connect(t, fake(t, map[string]string{"GET /license/status": `403 {"message":"no"}`}))
	if res, text, _ := call(t, cs, "get_license_status", nil); !res.IsError || !strings.HasPrefix(text, "FORBIDDEN:") {
		t.Errorf("a 403 without a status stays FORBIDDEN, got %q", text)
	}
}
