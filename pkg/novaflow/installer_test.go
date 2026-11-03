package novaflow

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
)

// fakeInstaller answers like the novaflow-installer app; the apps it deploys
// become ready after readyAfter readiness polls.
type fakeInstaller struct {
	mu         sync.Mutex
	installed  string
	polls      int
	readyAfter int
	failApp    string
}

func (f *fakeInstaller) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	reply := func(v any) { _ = json.NewEncoder(w).Encode(v) }
	switch p := strings.TrimPrefix(r.URL.Path, "/cell/novaflow-installer"); {
	case p == "/health":
		reply(map[string]string{"status": "ok"})
	case p == "/api/status":
		reply(map[string]any{"installed_version": nullable(f.installed), "recommended_version": "2026.10.81"})
	case p == "/api/install-all" && r.Method == http.MethodPost:
		var body struct{ Version string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.installed = body.Version
		reply([]map[string]any{
			{"app": "forest", "success": true, "message": "ok"},
			{"app": "controlplane", "success": f.failApp != "controlplane", "message": "Failed to install controlplane: 500"},
		})
	case p == "/api/readiness":
		f.polls++
		if f.polls < f.readyAfter {
			reply(map[string]any{"ready": false, "waiting_for": []string{"controlplane"}, "failed": []string{}})
			return
		}
		reply(map[string]any{"ready": true, "waiting_for": []string{}, "failed": []string{}})
	case p == "/api/uninstall-all" && r.Method == http.MethodDelete:
		f.installed = ""
		reply([]map[string]any{{"app": "forest", "success": true, "message": "ok"}})
	case strings.HasPrefix(r.URL.Path, "/cell/forest/trees") && f.installed != "" && f.polls >= f.readyAfter:
		reply(map[string]any{"items": []any{}, "total": 0, "has_more": false})
	default:
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusNotFound)
	}
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func TestInstallerInstallAndWait(t *testing.T) {
	fake := &fakeInstaller{readyAfter: 3}
	c := newTestClient(t, fake)
	ctx := context.Background()

	if err := c.WaitForInstaller(ctx, 1); err != nil {
		t.Fatal(err)
	}
	s, err := c.InstallerStatus(ctx)
	if err != nil || s.InstalledVersion != "" || s.RecommendedVersion != "2026.10.81" {
		t.Fatalf("status %+v, %v", s, err)
	}
	if _, err := c.InstallerInstall(ctx, s.RecommendedVersion); err != nil {
		t.Fatal(err)
	}
	var states []string
	if err := c.WaitForInstallerDeployment(ctx, s.RecommendedVersion, 1, func(s string) { states = append(states, s) }); err != nil {
		t.Fatal(err)
	}
	if len(states) == 0 || states[0] != "waiting for controlplane" {
		t.Fatalf("progress %q", states)
	}
	if fake.installed != "2026.10.81" {
		t.Fatalf("installed %q", fake.installed)
	}
}

func TestInstallerReportsFailedApps(t *testing.T) {
	c := newTestClient(t, &fakeInstaller{failApp: "controlplane"})
	_, err := c.InstallerInstall(context.Background(), "2026.10.81")
	if err == nil || !strings.Contains(err.Error(), "Failed to install controlplane") {
		t.Fatalf("want the failed app in the error, got %v", err)
	}
}

func TestInstallerUninstallWaitsForRemoval(t *testing.T) {
	fake := &fakeInstaller{installed: "2026.10.81"}
	c := newTestClient(t, fake)
	if _, err := c.InstallerUninstall(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := c.WaitForRemoval(context.Background(), 1, nil); err != nil {
		t.Fatal(err)
	}
}
