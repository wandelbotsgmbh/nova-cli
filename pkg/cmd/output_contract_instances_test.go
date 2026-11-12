package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wandelbotsgmbh/nova-cli/pkg/debug"
	"github.com/wandelbotsgmbh/nova-cli/pkg/instances"
	"github.com/wandelbotsgmbh/nova-cli/pkg/oauth"
	"github.com/wandelbotsgmbh/nova-cli/pkg/portal"
)

// ADR-0010 for the instance and k8srun commands: -o json prints a stable shape on
// stdout and, on success, nothing else (no token, no kubeconfig content).

const instancesJSONConfig = `current-instance: lab
instances:
  - name: lab
    kind: host
    host: http://192.168.1.10
    cellname: line1
    nucleus-token: secret-token
  - name: cloudy
    kind: cloud
    host: abc.instance.wandelbots.io
    id: abc
    env: prod
`

// runJSON runs args in a separate stdout/stderr and decodes stdout into v.
func runJSON(t *testing.T, v any, args ...string) (string, string, error) {
	t.Helper()
	t.Setenv(debug.EnvVar, "")
	stdout, stderr, err := runAsProcess(t, args...)
	if strings.Contains(stdout+stderr, "secret-token") {
		t.Errorf("a token was printed:\nstdout: %s\nstderr: %s", stdout, stderr)
	}
	if v != nil && stdout != "" {
		if jerr := json.Unmarshal([]byte(stdout), v); jerr != nil {
			t.Fatalf("stdout is no JSON: %v\n%s", jerr, stdout)
		}
	}
	return stdout, stderr, err
}

func TestInstanceListJSON(t *testing.T) {
	withConfig(t, instancesJSONConfig)
	stdout, stderr, err := runJSON(t, nil, "instance", "list", "-o", "json")
	if err != nil {
		t.Fatalf("error: %v\nstderr: %s", err, stderr)
	}
	want := `[{"name":"lab","kind":"host","host":"http://192.168.1.10","cell":"line1","selected":true,"local":true},` +
		`{"name":"cloudy","kind":"cloud","host":"abc.instance.wandelbots.io","cell":"cell","id":"abc","env":"prod","selected":false,"local":true}]`
	if got := compactJSON(t, stdout); got != want {
		t.Errorf("stdout = %s\nwant      %s", got, want)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want nothing", stderr)
	}

	// the selection, not the override, is "selected"; the note goes to stderr
	stdout, stderr, err = runJSON(t, nil, "instance", "list", "-o", "json", "--instance", "cloudy")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(compactJSON(t, stdout), `"name":"lab","kind":"host","host":"http://192.168.1.10","cell":"line1","selected":true`) {
		t.Errorf("stdout = %s", stdout)
	}
	if !strings.Contains(stderr, `note: this invocation targets "cloudy"`) {
		t.Errorf("stderr = %q, want the override note", stderr)
	}
}

func TestInstanceListJSONEmpty(t *testing.T) {
	withConfig(t, "")
	for _, args := range [][]string{
		{"instance", "list", "-o", "json"},
		{"k8srun", "list", "-o", "json"},
	} {
		stdout, stderr, err := runJSON(t, nil, args...)
		if err != nil {
			t.Fatalf("%v: %v\n%s", args, err, stderr)
		}
		if got := compactJSON(t, stdout); got != "[]" {
			t.Errorf("%v: stdout = %q, want []", args, got)
		}
	}
}

// cloud instances of the account that aren't in the local list come last, local: false
func TestInstanceListJSONUnregistered(t *testing.T) {
	old := portalEnvironment
	portalEnvironment = oauth.AuthDev
	t.Cleanup(func() { portalEnvironment = old })
	r := &instances.Registry{Current: "lab", Instances: []instances.Instance{{Name: "lab", Kind: instances.KindHost, Host: "h"}}}
	b, err := json.Marshal(instanceListJSON(r, []portal.UserInstance{{InstanceId: "xyz", Host: "xyz.instance.dev.wandelbots.io"}}))
	if err != nil {
		t.Fatal(err)
	}
	want := `[{"name":"lab","kind":"host","host":"h","cell":"cell","selected":true,"local":true},` +
		`{"name":"xyz","kind":"cloud","host":"xyz.instance.dev.wandelbots.io","cell":"cell","id":"xyz","env":"dev","selected":false,"local":false}]`
	if string(b) != want {
		t.Errorf("got  %s\nwant %s", b, want)
	}
}

func TestCloudInstanceJSON(t *testing.T) {
	expires := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	b, err := json.Marshal(newCloudInstanceJSON(portal.UserInstance{
		InstanceId: "abc", Host: "abc.instance.wandelbots.io", Status: portal.UserInstanceStatusRunning,
		SandboxName: "sb", ExpiresAt: int(expires.Unix()),
	}))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"id":"abc","host":"abc.instance.wandelbots.io","status":"running","sandbox_name":"sb","expires_at":"2026-11-18T10:00:00Z"}`
	if string(b) != want {
		t.Errorf("got  %s\nwant %s", b, want)
	}
}

func TestInstanceInfoJSON(t *testing.T) {
	withConfig(t, instancesJSONConfig)
	var got instanceInfoJSON
	stdout, stderr, err := runJSON(t, &got, "instance", "info", "lab", "-o", "json", "--show-secrets")
	if err != nil {
		t.Fatalf("error: %v\nstderr: %s", err, stderr)
	}
	want := `{"instance":{"name":"lab","kind":"host","host":"http://192.168.1.10","cell":"line1","selected":true,"local":true}}`
	if c := compactJSON(t, stdout); c != want {
		t.Errorf("stdout = %s\nwant      %s", c, want)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want nothing", stderr)
	}

	// the targeted instance without a name
	got = instanceInfoJSON{}
	if _, stderr, err := runJSON(t, &got, "--instance", "lab", "instance", "info", "-o", "json"); err != nil || got.Instance == nil || got.Instance.Name != "lab" {
		t.Errorf("targeted: %+v, %v\n%s", got, err, stderr)
	}

	stdout, stderr, err = runJSON(t, nil, "instance", "info", "lab", "-o", "json", "--host")
	if err == nil || stdout != "" || !strings.Contains(stderr, "can't be combined with -o json") {
		t.Errorf("--host with -o json: err %v, stdout %q, stderr %q", err, stdout, stderr)
	}
}

func TestInstanceAddJSON(t *testing.T) {
	withConfig(t, "")
	kubeconfig := filepath.Join(t.TempDir(), "k3s.yaml")
	if err := os.WriteFile(kubeconfig, []byte(testKubeconfigYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	var got instanceJSON
	_, stderr, err := runJSON(t, &got, "instance", "add", "lab", "--host", "10.1.2.3", "--kubeconfig", kubeconfig, "-o", "json")
	if err != nil {
		t.Fatalf("error: %v\nstderr: %s", err, stderr)
	}
	home, _ := os.UserHomeDir()
	want := instanceJSON{Name: "lab", Kind: "host", Host: "10.1.2.3", Cell: "cell",
		Kubeconfig: filepath.Join(home, ".nova", "instances", "lab", "kubeconfig"), Selected: true, Local: true}
	if got != want {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
	if !strings.Contains(stderr, `added instance "lab"`) {
		t.Errorf("stderr = %q, want the message", stderr)
	}
}

const testKubeconfigYAML = `apiVersion: v1
kind: Config
clusters:
  - name: default
    cluster:
      server: https://127.0.0.1:6443
      certificate-authority-data: c2VjcmV0
`

func TestK8sRunJSON(t *testing.T) {
	withConfig(t, instancesJSONConfig)
	fake := newFakeK8sRunService(t)
	home, _ := os.UserHomeDir()

	var created k8srunCreatedJSON
	stdout, stderr, err := runJSON(t, &created, "k8srun", "create", "--name", "sim-a", "--no-select", "-o", "json")
	if err != nil {
		t.Fatalf("error: %v\nstderr: %s", err, stderr)
	}
	want := `{"name":"sim-a","kind":"k8srun","host":"10.0.0.1","cell":"cell","id":"id-1","kubeconfig":"` +
		filepath.Join(home, ".nova", "instances", "sim-a", "kubeconfig") + `","selected":false,"local":true}`
	if c := compactJSON(t, stdout); c != want {
		t.Errorf("stdout = %s\nwant      %s", c, want)
	}
	if strings.Contains(stdout, "kubeconfig-1") {
		t.Error("the kubeconfig content was printed")
	}

	// unnamed: also written to ./kubeconfig, reported with an absolute path
	created = k8srunCreatedJSON{}
	if _, stderr, err := runJSON(t, &created, "k8srun", "create", "-o", "json"); err != nil {
		t.Fatalf("error: %v\n%s", err, stderr)
	}
	if !filepath.IsAbs(created.KubeconfigCopy) || filepath.Base(created.KubeconfigCopy) != "kubeconfig" || !created.Selected {
		t.Errorf("unnamed create: %+v", created)
	}

	var list []instanceJSON
	if _, stderr, err := runJSON(t, &list, "k8srun", "list", "-o", "json"); err != nil {
		t.Fatalf("error: %v\n%s", err, stderr)
	}
	if len(list) != 2 || list[0].Name != "sim-a" || list[1].Name != "k8srun" || !list[1].Selected {
		t.Errorf("k8srun list = %+v", list)
	}

	var kc kubeconfigJSON
	stdout, stderr, err = runJSON(t, &kc, "instance", "kubeconfig", "sim-a", "-o", "json")
	if err != nil {
		t.Fatalf("error: %v\n%s", err, stderr)
	}
	if want := (kubeconfigJSON{Name: "sim-a", Kind: "k8srun", Path: filepath.Join(home, ".nova", "instances", "sim-a", "kubeconfig")}); kc != want {
		t.Errorf("kubeconfig = %+v, want %+v", kc, want)
	}
	if stderr != "" || strings.Contains(stdout, "kubeconfig-1") {
		t.Errorf("stdout %q, stderr %q", stdout, stderr)
	}

	// extend: the cluster service doesn't report the expiry, nova sets 12 h
	// (UTC in any local time zone: TestLifetimeJSONIsUTC)
	var ext lifetimeJSON
	before := time.Now()
	if _, stderr, err := runJSON(t, &ext, "instance", "extend", "sim-a", "-o", "json"); err != nil {
		t.Fatalf("error: %v\n%s", err, stderr)
	}
	expires, err := time.Parse(time.RFC3339, ext.ExpiresAt)
	if err != nil || ext.Name != "sim-a" || ext.Kind != "k8srun" || ext.ID != "id-1" || !ext.Extended ||
		expires.Before(before.Add(12*time.Hour-time.Second)) || !strings.HasSuffix(ext.ExpiresAt, "Z") {
		t.Errorf("extend = %+v (%v), want a later expiry in UTC", ext, err)
	}

	// prune: one gone, one unreachable; the JSON comes before the error
	fake.gone["id-1"] = true
	fake.broken["id-2"] = true
	var pruned pruneJSON
	stdout, stderr, err = runJSON(t, &pruned, "k8srun", "prune", "-y", "-o", "json")
	if err == nil {
		t.Error("an unchecked cluster must fail the command")
	}
	want = `{"dry_run":false,"removed":[{"name":"sim-a","kind":"k8srun"}],"unchecked":[{"name":"k8srun","kind":"k8srun","reason":"cluster service answered with status code 500"}]}`
	if c := compactJSON(t, stdout); c != want {
		t.Errorf("stdout = %s\nwant      %s", c, want)
	}
	if strings.Contains(stdout, "removed sim-a") || !strings.Contains(stderr, `keeping "k8srun"`) {
		t.Errorf("stdout %q, stderr %q", stdout, stderr)
	}
}

func TestInstancePruneJSON(t *testing.T) {
	withConfig(t, instancesJSONConfig+`  - {name: gone-dev, kind: cloud, id: ccc, env: dev}
  - {name: stg-nologin, kind: cloud, id: ddd, env: stg}
`)
	fakePortalListings(t, map[oauth.AuthEnvironment][]string{
		oauth.AuthProd: {"abc"},
		oauth.AuthDev:  {},
	})

	var pruned pruneJSON
	stdout, stderr, err := runJSON(t, &pruned, "instance", "prune", "--dry-run", "-o", "json")
	if err == nil {
		t.Error("an unchecked instance must fail the command")
	}
	want := `{"dry_run":true,"removed":[{"name":"gone-dev","kind":"cloud"}],"unchecked":[{"name":"stg-nologin","kind":"cloud","reason":"can't list the stg instances: not logged in"}]}`
	if c := compactJSON(t, stdout); c != want {
		t.Errorf("stdout = %s\nwant      %s", c, want)
	}
	if !strings.Contains(stderr, `keeping "stg-nologin"`) {
		t.Errorf("stderr = %q", stderr)
	}

	// nothing to do: empty arrays, not null
	withConfig(t, "")
	stdout, stderr, err = runJSON(t, nil, "instance", "prune", "-o", "json")
	if err != nil {
		t.Fatalf("error: %v\n%s", err, stderr)
	}
	if c := compactJSON(t, stdout); c != `{"dry_run":false,"removed":[],"unchecked":[]}` {
		t.Errorf("stdout = %s", c)
	}
}

// the text output of the changed commands stays as it was
func TestInstanceTextOutputUnchanged(t *testing.T) {
	withConfig(t, instancesJSONConfig)
	stdout, _, err := runJSON(t, nil, "instance", "list")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(stdout, "   NAME") || !strings.Contains(stdout, "*  lab") {
		t.Errorf("text list changed:\n%s", stdout)
	}
	fake := newFakeK8sRunService(t)
	mustRun(t, "k8srun", "create", "--name", "sim-a")
	fake.gone["id-1"] = true
	stdout, _, err = runJSON(t, nil, "k8srun", "prune", "--dry-run")
	if err != nil || stdout != "would remove sim-a\n" {
		t.Errorf("text prune: %q, %v", stdout, err)
	}
}

func TestRegisterCloudInstanceEntry(t *testing.T) {
	withConfig(t, "")
	mustRun(t, "instance", "add", "abc", "--host", "h")
	var errOut bytes.Buffer
	inst, err := registerCloudInstanceEntry(&errOut, "abc", "abc.instance.wandelbots.io", false)
	if err != nil {
		t.Fatal(err)
	}
	if inst.Name != "abc-2" || inst.ID != "abc" || inst.Kind != instances.KindCloud {
		t.Errorf("entry = %+v, want the free name abc-2", inst)
	}
}

// instance extend -o json reports the expiry in UTC, also when it comes in another
// zone. (Not by changing time.Local in the end-to-end test: the fake servers read
// it concurrently, which -race reports.)
func TestLifetimeJSONIsUTC(t *testing.T) {
	inst := instances.Instance{Name: "sim-a", Kind: instances.KindK8sRun, ID: "id-1"}
	expires := time.Date(2026, 11, 12, 2, 0, 0, 0, time.FixedZone("UTC+2", 2*60*60))
	got := newLifetimeJSON(inst, expires, true)
	if got.ExpiresAt != "2026-11-12T00:00:00Z" {
		t.Errorf("expires_at = %q, want 2026-11-12T00:00:00Z", got.ExpiresAt)
	}
}
