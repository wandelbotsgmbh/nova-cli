package novaapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/novaapi"
)

// physicalControllerBody is a realistic getRobotController answer for a physical
// controller: it carries connection details, exactly what DescribeController must never
// hand on.
const physicalControllerBody = `{
	"name": "kuka-1",
	"configuration": {
		"kind": "KukaController",
		"controller_ip": "10.1.2.3",
		"controller_port": 7000,
		"rsi_server": {"ip": "10.1.2.9", "port": 59152}
	}
}`

func serve(t *testing.T, handler http.HandlerFunc) string {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server.URL
}

func jsonAnswer(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}
}

func newAPI(t *testing.T, url string, editor novaapi.RequestEditor) novaapi.API {
	t.Helper()
	api, err := novaapi.New(url, nil, editor, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return api
}

func wantCode(t *testing.T, err error, code string) *novaapi.Error {
	t.Helper()
	var apiErr *novaapi.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected a *novaapi.Error with %s, got %v", code, err)
	}
	if apiErr.Code != code {
		t.Fatalf("expected %s, got %s", code, apiErr)
	}
	return apiErr
}

// The projection is checked on the bytes a tool gets, not only on the struct shape, so
// adding a leaky field later fails here.
func TestDescribeController_NeverLeaksConnectionDetails(t *testing.T) {
	api := newAPI(t, serve(t, jsonAnswer(physicalControllerBody)), nil)

	info, err := api.DescribeController(context.Background(), "cell-1", "kuka-1")
	if err != nil {
		t.Fatalf("DescribeController: %v", err)
	}
	out, _ := json.Marshal(info)
	for _, leaked := range []string{"10.1.2.3", "10.1.2.9", "controller_ip", "rsi_server", "7000", "59152"} {
		if strings.Contains(string(out), leaked) {
			t.Errorf("ControllerInfo leaked %q: %s", leaked, out)
		}
	}
	if !info.Physical || info.Name != "kuka-1" || info.Manufacturer != "KukaController" {
		t.Errorf("unexpected projection: %+v", info)
	}
}

func TestDescribeController_VirtualControllerType(t *testing.T) {
	api := newAPI(t, serve(t, jsonAnswer(`{
		"name": "sim-1",
		"configuration": {"kind": "VirtualController", "manufacturer": "universalrobots", "type": "universalrobots-ur5e"}
	}`)), nil)

	info, err := api.DescribeController(context.Background(), "cell-1", "sim-1")
	if err != nil {
		t.Fatalf("DescribeController: %v", err)
	}
	if info.Physical || info.VirtualType != "universalrobots-ur5e" || info.Manufacturer != "universalrobots" {
		t.Errorf("unexpected projection: %+v", info)
	}
}

func TestEditor_AppliedToEveryRequest(t *testing.T) {
	var paths, auths []string
	url := serve(t, func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		auths = append(auths, r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`["universalrobots-ur5e"]`))
	})
	api := newAPI(t, url+"/api/v2", func(_ context.Context, req *http.Request) error {
		req.Header.Set("Authorization", "Bearer local-login")
		return nil
	})

	if _, err := api.ListCells(context.Background()); err != nil {
		t.Fatalf("ListCells: %v", err)
	}
	configurations, err := api.ListRobotConfigurations(context.Background())
	if err != nil {
		t.Fatalf("ListRobotConfigurations: %v", err)
	}
	if len(configurations) != 1 || configurations[0] != "universalrobots-ur5e" {
		t.Errorf("unexpected configurations: %v", configurations)
	}
	if strings.Join(paths, " ") != "/api/v2/cells /api/v2/robot-configurations" {
		t.Errorf("unexpected paths: %v", paths)
	}
	for _, auth := range auths {
		if auth != "Bearer local-login" {
			t.Errorf("expected the editor's Authorization on every request, got %q", auth)
		}
	}
}

func TestEditor_ErrorIsUnauthenticated(t *testing.T) {
	called := false
	url := serve(t, func(http.ResponseWriter, *http.Request) { called = true })
	api := newAPI(t, url, func(context.Context, *http.Request) error {
		return errors.New("login of the prod environment expired, run 'nova login --env prod'")
	})

	_, err := api.ListCells(context.Background())
	apiErr := wantCode(t, err, novaapi.CodeUnauthenticated)
	if !strings.Contains(apiErr.Message, "nova login") {
		t.Errorf("expected the login hint, got %q", apiErr.Message)
	}
	_, err = api.ListRobotConfigurations(context.Background())
	wantCode(t, err, novaapi.CodeUnauthenticated)
	if called {
		t.Error("no request may be sent without a login")
	}
}

// The instance ingress answers a missing or expired login with an error page and
// HTTP 200; it must never be read as data (e.g. as the system version).
func TestIngressErrorPageAs200IsUnauthenticated(t *testing.T) {
	for name, handler := range map[string]http.HandlerFunc{
		"text": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte("Error 401: Unauthorized"))
		},
		"json": jsonAnswer(`{"error": true, "code": 401, "message": "Unauthorized"}`),
	} {
		t.Run(name, func(t *testing.T) {
			api := newAPI(t, serve(t, handler), nil)
			_, err := api.GetSystemVersion(context.Background())
			wantCode(t, err, novaapi.CodeUnauthenticated)
			_, err = api.ListCells(context.Background())
			wantCode(t, err, novaapi.CodeUnauthenticated)
			_, err = api.ListRobotConfigurations(context.Background())
			wantCode(t, err, novaapi.CodeUnauthenticated)
			_, err = api.DeleteController(context.Background(), "cell", "ctrl")
			wantCode(t, err, novaapi.CodeUnauthenticated)
		})
	}
}

func TestGetSystemVersion_TextOrJSON(t *testing.T) {
	for _, body := range []string{"26.8.0\n", `"26.8.0"`} {
		api := newAPI(t, serve(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }), nil)
		version, err := api.GetSystemVersion(context.Background())
		if err != nil || version != "26.8.0" {
			t.Errorf("body %q: got %q, %v", body, version, err)
		}
	}
}

func TestStatusMapping(t *testing.T) {
	cases := []struct {
		status int
		body   string
		code   string
		msg    string
	}{
		{http.StatusNotFound, `{}`, novaapi.CodeNotFound, "controller 'ctrl' not found in cell 'cell'"},
		{http.StatusBadRequest, `{"code": "x", "message": "name is invalid"}`, novaapi.CodeInvalidArgument, "name is invalid"},
		{http.StatusForbidden, `{}`, novaapi.CodeForbidden, "not allowed"},
		{http.StatusUnauthorized, ``, novaapi.CodeUnauthenticated, "nova login"},
		{http.StatusInternalServerError, `secret stack trace`, novaapi.CodeInternal, "HTTP 500"},
	}
	for _, c := range cases {
		api := newAPI(t, serve(t, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(c.status)
			_, _ = w.Write([]byte(c.body))
		}), nil)
		_, err := api.DeleteController(context.Background(), "cell", "ctrl")
		apiErr := wantCode(t, err, c.code)
		if !strings.Contains(apiErr.Message, c.msg) {
			t.Errorf("HTTP %d: expected %q in %q", c.status, c.msg, apiErr.Message)
		}
		if strings.Contains(apiErr.Message, "secret") {
			t.Errorf("HTTP %d: body leaked into %q", c.status, apiErr.Message)
		}
	}
}

func TestCreateVirtualController_SendsPreset(t *testing.T) {
	var got map[string]any
	api := newAPI(t, serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/cells/cell-1/controllers" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(http.StatusAccepted)
	}), nil)

	result, err := api.CreateVirtualController(context.Background(), "cell-1", novaapi.VirtualControllerSpec{
		Name: "sim-1", Manufacturer: "universalrobots", Type: "universalrobots-ur5e",
	})
	if err != nil {
		t.Fatalf("CreateVirtualController: %v", err)
	}
	if result.Status != novaapi.StatusPending {
		t.Errorf("expected pending for 202, got %+v", result)
	}
	config, _ := got["configuration"].(map[string]any)
	if got["name"] != "sim-1" || config["kind"] != "VirtualController" || config["type"] != "universalrobots-ur5e" {
		t.Errorf("unexpected body: %v", got)
	}
}

func TestValidationDetailIsPassedOn(t *testing.T) {
	api := newAPI(t, serve(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"detail":[{"loc":["body","joints"],"msg":"invalid dimension (expected 6, but got 3)"}]}`))
	}), nil)
	_, err := api.DeleteController(context.Background(), "cell", "ctrl")
	apiErr := wantCode(t, err, novaapi.CodeInvalidArgument)
	if !strings.Contains(apiErr.Message, "expected 6, but got 3") {
		t.Errorf("expected the validation message, got %q", apiErr.Message)
	}
}
