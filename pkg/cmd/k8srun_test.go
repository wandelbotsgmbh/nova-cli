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

// fakeK8sRunService hands out clusters with increasing ids and records destroyed ones.
// Lookups of ids in gone answer 404, ids in broken 500.
type fakeK8sRunService struct {
	mu        sync.Mutex
	next      int
	destroyed []string
	comments  []string
	gone      map[string]bool
	broken    map[string]bool
	// kubeconfigs answered per id (base64 in a JSON string); default "kubeconfig"
	kubeconfigs map[string]string
}

func newFakeK8sRunService(t *testing.T) *fakeK8sRunService {
	t.Helper()
	f := &fakeK8sRunService{gone: map[string]bool{}, broken: map[string]bool{}, kubeconfigs: map[string]string{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/kubeconfig"):
			id := strings.Split(strings.Trim(r.URL.Path, "/"), "/")[0]
			switch {
			case f.broken[id]:
				w.WriteHeader(http.StatusInternalServerError)
			case f.gone[id]:
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"msg":"The provided id is not assigned."}`))
			case f.kubeconfigs[id] != "":
				_ = json.NewEncoder(w).Encode(base64.StdEncoding.EncodeToString([]byte(f.kubeconfigs[id])))
			default:
				_, _ = w.Write([]byte(`"a2ViZWNvbmZpZw=="`))
			}
		case r.Method == http.MethodGet:
			f.comments = append(f.comments, r.URL.Query().Get("comment"))
			f.next++
			_ = json.NewEncoder(w).Encode(virtualInstance{
				Id:         fmt.Sprintf("id-%d", f.next),
				Ip:         fmt.Sprintf("10.0.0.%d", f.next),
				Kubeconfig: base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("kubeconfig-%d", f.next))),
			})
		case r.Method == http.MethodPut:
			f.destroyed = append(f.destroyed, r.URL.Query().Get("instance_id"))
		}
	}))
	t.Cleanup(srv.Close)
	old := clusterServiceURL
	clusterServiceURL = srv.URL
	t.Cleanup(func() { clusterServiceURL = old })
	return f
}

func TestK8sRunNamedSideBySide(t *testing.T) {
	withConfig(t, "")
	fake := newFakeK8sRunService(t)

	mustRun(t, "k8srun", "create", "--name", "sim-a")
	mustRun(t, "k8srun", "create", "--name", "sim-b")
	if len(fake.destroyed) != 0 {
		t.Fatalf("named create must not destroy anything, destroyed %v", fake.destroyed)
	}

	r, _ := instances.Load(viper.GetViper())
	a, _ := r.Get("sim-a")
	if a.Kind != instances.KindK8sRun || a.ID != "id-1" || a.Host != "10.0.0.1" || r.Current != "sim-b" {
		t.Fatalf("unexpected registry: %+v", r)
	}
	if b, err := os.ReadFile(a.Kubeconfig); err != nil || string(b) != "kubeconfig-1" {
		t.Errorf("kubeconfig of sim-a: %q, %v", b, err)
	}
	if _, err := os.Stat("kubeconfig"); err == nil {
		t.Error("named create must not write ./kubeconfig")
	}

	_, _, err := runWithConfig(t, "k8srun", "create", "--name", "sim-a")
	if err == nil || !strings.Contains(err.Error(), "--replace") {
		t.Fatalf("existing name without --replace: err = %v", err)
	}
	mustRun(t, "k8srun", "create", "--name", "sim-a", "--replace")
	if strings.Join(fake.destroyed, ",") != "id-1" {
		t.Errorf("--replace must destroy the old sim-a only, destroyed %v", fake.destroyed)
	}

	mustRun(t, "k8srun", "destroy", "sim-b")
	if out := mustRun(t, "k8srun", "list"); strings.Contains(out, "sim-b") || !strings.Contains(out, "sim-a") {
		t.Errorf("unexpected list:\n%s", out)
	}
}

func TestK8sRunCreateNoSelect(t *testing.T) {
	withConfig(t, "")
	newFakeK8sRunService(t)
	mustRun(t, "k8srun", "create", "--name", "sim-a")
	mustRun(t, "k8srun", "create", "--name", "sim-b", "--no-select")

	r, _ := instances.Load(viper.GetViper())
	if _, ok := r.Get("sim-b"); !ok {
		t.Fatalf("sim-b not added: %+v", r)
	}
	if r.Current != "sim-a" {
		t.Errorf("selection changed to %q, want sim-a", r.Current)
	}
}

func TestK8sRunCommentNamesTheUser(t *testing.T) {
	withConfig(t, "")
	fake := newFakeK8sRunService(t)
	mustRun(t, "k8srun", "create", "--name", "sim-a")

	want := "created by nova-cli for " + localUser()
	if len(fake.comments) != 1 || fake.comments[0] != want {
		t.Errorf("comments %q, want [%q]", fake.comments, want)
	}
	if strings.HasPrefix(localUser(), "@") {
		t.Errorf("no user name in %q", localUser())
	}
}

func TestK8sRunUnnamedReplacesOnlyUnnamed(t *testing.T) {
	withConfig(t, "")
	fake := newFakeK8sRunService(t)

	mustRun(t, "k8srun", "create", "--name", "keep")
	mustRun(t, "k8srun", "create")
	mustRun(t, "k8srun", "create")
	if strings.Join(fake.destroyed, ",") != "id-2" {
		t.Errorf("unnamed create must replace only the previous unnamed cluster, destroyed %v", fake.destroyed)
	}
	if b, err := os.ReadFile("kubeconfig"); err != nil || string(b) != "kubeconfig-3" {
		t.Errorf("./kubeconfig: %q, %v", b, err)
	}
	r, _ := instances.Load(viper.GetViper())
	if c, _ := r.Get(defaultK8sRunName); c.ID != "id-3" || r.Current != defaultK8sRunName {
		t.Errorf("unexpected registry: %+v", r)
	}
}

func TestK8sRunRefusesOtherKinds(t *testing.T) {
	withConfig(t, "")
	fake := newFakeK8sRunService(t)
	mustRun(t, "instance", "add", "lab", "--host", "192.168.1.10")

	if _, _, err := runWithConfig(t, "k8srun", "create", "--name", "lab", "--replace"); err == nil || !strings.Contains(err.Error(), "host instance") {
		t.Errorf("create over a host entry: err = %v", err)
	}
	if _, _, err := runWithConfig(t, "k8srun", "destroy", "lab"); err == nil || !strings.Contains(err.Error(), "only works for k8srun instances") {
		t.Errorf("destroy a host entry: err = %v", err)
	}
	if _, _, err := runWithConfig(t, "k8srun", "destroy"); err == nil {
		t.Error("destroy without a name must refuse the selected host entry")
	}
	if fake.next != 0 || len(fake.destroyed) != 0 {
		t.Errorf("no request expected, got create=%d destroy=%v", fake.next, fake.destroyed)
	}
}

func TestVirtualAlias(t *testing.T) {
	withConfig(t, "")
	newFakeK8sRunService(t)
	// cobra prints the deprecation note to the out writer, which defaults to stderr
	out, errOut, err := runWithConfig(t, "virtual", "create")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out+errOut, `Command "create" is deprecated, 'nova virtual' was renamed: use 'nova k8srun create'`) {
		t.Errorf("expected a deprecation note naming 'nova virtual' and the replacement, got %q", out+errOut)
	}
	if out, errOut, _ := runWithConfig(t, "virtual", "list"); !strings.Contains(out+errOut, "use 'nova k8srun list'") {
		t.Errorf("deprecation note of 'virtual list': %q", out+errOut)
	}
	if out, errOut, _ := runWithConfig(t, "k8srun", "list"); strings.Contains(out+errOut, "deprecated") {
		t.Error("no deprecation note for 'nova k8srun'")
	}
}

func TestK8sRunPrune(t *testing.T) {
	withConfig(t, "")
	fake := newFakeK8sRunService(t)
	mustRun(t, "instance", "add", "lab", "--host", "192.168.1.10")
	mustRun(t, "k8srun", "create", "--name", "alive")   // id-1
	mustRun(t, "k8srun", "create", "--name", "expired") // id-2
	mustRun(t, "k8srun", "create", "--name", "unknown") // id-3
	fake.gone["id-2"] = true
	fake.broken["id-3"] = true
	mustRun(t, "instance", "select", "expired")
	expiredDir, _ := instanceDir("expired")

	out, _, err := runWithConfig(t, "k8srun", "prune", "--dry-run")
	if err == nil || !strings.Contains(out, "would remove expired") {
		t.Fatalf("dry run: out=%q err=%v", out, err)
	}
	if r, _ := instances.Load(viper.GetViper()); len(r.Instances) != 4 {
		t.Fatalf("--dry-run must not change the list: %+v", r.Instances)
	}

	out, errOut, err := runWithConfig(t, "k8srun", "prune")
	if err == nil || !strings.Contains(err.Error(), "without a terminal, confirm with -y") || strings.Contains(out, "removed") {
		t.Fatalf("without a terminal and -y nothing may be removed: out=%q err=%v", out, err)
	}
	if r, _ := instances.Load(viper.GetViper()); len(r.Instances) != 4 {
		t.Fatalf("an unconfirmed prune must not change the list: %+v", r.Instances)
	}

	out, errOut, err = runWithConfig(t, "k8srun", "prune", "-y")
	if err == nil || !strings.Contains(err.Error(), "could not be checked") {
		t.Errorf("an unchecked cluster must fail the command, err = %v", err)
	}
	if !strings.Contains(out, "removed expired") || !strings.Contains(errOut, `keeping "unknown"`) {
		t.Errorf("out=%q errOut=%q", out, errOut)
	}
	if !strings.Contains(errOut, `selected instance "expired" was removed`) {
		t.Errorf("expected a note about the removed selection, got %q", errOut)
	}
	r, _ := instances.Load(viper.GetViper())
	for _, name := range []string{"lab", "alive", "unknown"} {
		if _, ok := r.Get(name); !ok {
			t.Errorf("%q must be kept", name)
		}
	}
	if _, ok := r.Get("expired"); ok || r.Current != "" {
		t.Errorf("expired must be removed and unselected: %+v", r)
	}
	if _, err := os.Stat(expiredDir); !os.IsNotExist(err) {
		t.Errorf("kubeconfig dir of expired must be removed, stat err = %v", err)
	}
	if len(fake.destroyed) != 0 {
		t.Errorf("prune must not destroy clusters, destroyed %v", fake.destroyed)
	}
}
