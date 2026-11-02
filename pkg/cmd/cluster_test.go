package cmd

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/spf13/viper"
	"github.com/wandelbotsgmbh/nova-cli/pkg/instances"
)

// fakeClusterService hands out clusters with increasing ids and records destroyed ones.
type fakeClusterService struct {
	mu        sync.Mutex
	next      int
	destroyed []string
}

func newFakeClusterService(t *testing.T) *fakeClusterService {
	t.Helper()
	f := &fakeClusterService{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		switch r.Method {
		case http.MethodGet:
			f.next++
			_ = json.NewEncoder(w).Encode(virtualInstance{
				Id:         fmt.Sprintf("id-%d", f.next),
				Ip:         fmt.Sprintf("10.0.0.%d", f.next),
				Kubeconfig: base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("kubeconfig-%d", f.next))),
			})
		case http.MethodPut:
			f.destroyed = append(f.destroyed, r.URL.Query().Get("instance_id"))
		}
	}))
	t.Cleanup(srv.Close)
	old := clusterServiceURL
	clusterServiceURL = srv.URL
	t.Cleanup(func() { clusterServiceURL = old })
	return f
}

func TestClusterNamedSideBySide(t *testing.T) {
	withConfig(t, "")
	fake := newFakeClusterService(t)

	mustRun(t, "cluster", "create", "--name", "sim-a")
	mustRun(t, "cluster", "create", "--name", "sim-b")
	if len(fake.destroyed) != 0 {
		t.Fatalf("named create must not destroy anything, destroyed %v", fake.destroyed)
	}

	r, _ := instances.Load(viper.GetViper())
	a, _ := r.Get("sim-a")
	if a.Kind != instances.KindCluster || a.ID != "id-1" || a.Host != "10.0.0.1" || r.Current != "sim-b" {
		t.Fatalf("unexpected registry: %+v", r)
	}
	if b, err := os.ReadFile(a.Kubeconfig); err != nil || string(b) != "kubeconfig-1" {
		t.Errorf("kubeconfig of sim-a: %q, %v", b, err)
	}
	if _, err := os.Stat("kubeconfig"); err == nil {
		t.Error("named create must not write ./kubeconfig")
	}

	_, _, err := runNova(t, "cluster", "create", "--name", "sim-a")
	if err == nil || !strings.Contains(err.Error(), "--replace") {
		t.Fatalf("existing name without --replace: err = %v", err)
	}
	mustRun(t, "cluster", "create", "--name", "sim-a", "--replace")
	if strings.Join(fake.destroyed, ",") != "id-1" {
		t.Errorf("--replace must destroy the old sim-a only, destroyed %v", fake.destroyed)
	}

	mustRun(t, "cluster", "destroy", "sim-b")
	if out := mustRun(t, "cluster", "list"); strings.Contains(out, "sim-b") || !strings.Contains(out, "sim-a") {
		t.Errorf("unexpected list:\n%s", out)
	}
}

func TestClusterUnnamedReplacesOnlyUnnamed(t *testing.T) {
	withConfig(t, "")
	fake := newFakeClusterService(t)

	mustRun(t, "cluster", "create", "--name", "keep")
	mustRun(t, "cluster", "create")
	mustRun(t, "cluster", "create")
	if strings.Join(fake.destroyed, ",") != "id-2" {
		t.Errorf("unnamed create must replace only the previous unnamed cluster, destroyed %v", fake.destroyed)
	}
	if b, err := os.ReadFile("kubeconfig"); err != nil || string(b) != "kubeconfig-3" {
		t.Errorf("./kubeconfig: %q, %v", b, err)
	}
	r, _ := instances.Load(viper.GetViper())
	if c, _ := r.Get(defaultClusterName); c.ID != "id-3" || r.Current != defaultClusterName {
		t.Errorf("unexpected registry: %+v", r)
	}
}

func TestClusterRefusesOtherKinds(t *testing.T) {
	withConfig(t, "")
	fake := newFakeClusterService(t)
	mustRun(t, "instance", "add", "lab", "--host", "192.168.1.10")

	if _, _, err := runNova(t, "cluster", "create", "--name", "lab", "--replace"); err == nil || !strings.Contains(err.Error(), "host instance") {
		t.Errorf("create over a host entry: err = %v", err)
	}
	if _, _, err := runNova(t, "cluster", "destroy", "lab"); err == nil || !strings.Contains(err.Error(), "only works for cluster instances") {
		t.Errorf("destroy a host entry: err = %v", err)
	}
	if _, _, err := runNova(t, "cluster", "destroy"); err == nil {
		t.Error("destroy without a name must refuse the selected host entry")
	}
	if fake.next != 0 || len(fake.destroyed) != 0 {
		t.Errorf("no request expected, got create=%d destroy=%v", fake.next, fake.destroyed)
	}
}

func TestVirtualAlias(t *testing.T) {
	withConfig(t, "")
	newFakeClusterService(t)
	// cobra prints the deprecation note to the out writer, which defaults to stderr
	out, errOut, err := runNova(t, "virtual", "create")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out+errOut, "use 'nova cluster' instead") {
		t.Errorf("expected a deprecation note, got %q", out+errOut)
	}
	if out, errOut, _ := runNova(t, "cluster", "list"); strings.Contains(out+errOut, "deprecated") {
		t.Error("no deprecation note for 'nova cluster'")
	}
}
