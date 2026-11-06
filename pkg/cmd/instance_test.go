package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	homedir "github.com/mitchellh/go-homedir"
	"github.com/spf13/viper"
	"github.com/wandelbotsgmbh/nova-cli/pkg/instances"
)

// withConfig points the CLI at a temporary home with the given config file content
// and returns the config file path.
func withConfig(t *testing.T, content string) string {
	t.Helper()
	home := t.TempDir()
	dir := filepath.Join(home, ".nova")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv(instances.EnvVar, "")
	t.Setenv(envVarName, "")
	homedir.DisableCache = true
	// a config.yaml in the working directory would shadow the test config
	t.Chdir(home)
	return path
}

// runWithConfig executes the root command like main does against the config from
// withConfig and returns stdout and stderr.
func runWithConfig(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	resetState(rootCmd)

	var stdout, stderr bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&stderr)
	rootCmd.SetArgs(args)
	err := rootCmd.Execute()
	return stdout.String(), stderr.String(), err
}

func mustRun(t *testing.T, args ...string) string {
	t.Helper()
	out, errOut, err := runWithConfig(t, args...)
	if err != nil {
		t.Fatalf("nova %s: %v\n%s", strings.Join(args, " "), err, errOut)
	}
	return out
}

func readConfig(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestLegacyConfigKeepsWorking(t *testing.T) {
	path := withConfig(t, "host: 192.168.1.10\ncellname: line1\nnucleus-host: nucleus.dev.wandelbots.io\nnucleus-token: s3cret\n")
	before := readConfig(t, path)

	out := mustRun(t, "config", "view")
	for _, want := range []string{
		"host: 192.168.1.10\n",
		"cellname: line1\n",
		"nucleus-host: nucleus.dev.wandelbots.io\n",
		"nucleus-token: " + instances.Redact + "\n",
		"nucleus: dev (from config)\n",
		"instance: default (host, from config)\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("config view missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "s3cret") {
		t.Error("config view must redact tokens")
	}
	if readConfig(t, path) != before {
		t.Error("reading the config must not rewrite it")
	}

	if out := mustRun(t, "config", "view", "--show-secrets"); !strings.Contains(out, "nucleus-token: s3cret\n") {
		t.Errorf("--show-secrets must print the token:\n%s", out)
	}
}

func TestConfigSetMigratesIntoSelectedEntry(t *testing.T) {
	path := withConfig(t, "host: 192.168.1.10\neditor: vim\n")
	mustRun(t, "config", "set", "cellname", "line1")

	r, err := instances.Load(viper.GetViper())
	if err != nil {
		t.Fatal(err)
	}
	def, ok := r.Get(instances.DefaultName)
	if !ok || def.Host != "192.168.1.10" || def.CellName != "line1" {
		t.Fatalf("unexpected registry: %+v", r)
	}
	if content := readConfig(t, path); !strings.Contains(content, "editor: vim") || !strings.Contains(content, "host: 192.168.1.10") {
		t.Errorf("legacy and unrelated keys must be kept:\n%s", content)
	}
}

func TestConfigSetHostOnEmptyConfigCreatesDefault(t *testing.T) {
	withConfig(t, "")
	_, errOut, err := runWithConfig(t, "config", "set", "host", "10.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut, `created instance "default"`) {
		t.Errorf("expected a note on stderr, got %q", errOut)
	}
	if out := mustRun(t, "instance", "list"); !strings.Contains(out, "*  default  host  10.0.0.1") {
		t.Errorf("unexpected list:\n%s", out)
	}
}

func TestOverrideDoesNotTouchSelection(t *testing.T) {
	path := withConfig(t, "")
	mustRun(t, "instance", "add", "lab", "--host", "10.0.0.1", "--cell", "line1")
	mustRun(t, "instance", "add", "ipc2", "--host", "10.0.0.2")

	out := mustRun(t, "instance", "list")
	if !strings.Contains(out, "*  lab ") || strings.Contains(out, "*  ipc2") {
		t.Fatalf("the first added instance must be selected:\n%s", out)
	}

	before := readConfig(t, path)
	if out := mustRun(t, "--instance", "ipc2", "config", "view"); !strings.Contains(out, "host: 10.0.0.2\n") || !strings.Contains(out, "from flag") {
		t.Errorf("--instance not applied:\n%s", out)
	}
	t.Setenv(instances.EnvVar, "ipc2")
	if out := mustRun(t, "config", "view"); !strings.Contains(out, "host: 10.0.0.2\n") || !strings.Contains(out, "from env") {
		t.Errorf("%s not applied:\n%s", instances.EnvVar, out)
	}
	if out := mustRun(t, "--instance", "lab", "config", "view"); !strings.Contains(out, "host: 10.0.0.1\n") {
		t.Errorf("flag must win over env:\n%s", out)
	}
	if readConfig(t, path) != before {
		t.Error("an override must not write the config")
	}
}

func TestUnknownOverrideFails(t *testing.T) {
	withConfig(t, "host: 10.0.0.1\n")
	t.Setenv(instances.EnvVar, "nope")
	_, _, err := runWithConfig(t, "config", "view")
	if err == nil || !strings.Contains(err.Error(), instances.EnvVar) {
		t.Fatalf("expected an error naming %s, got %v", instances.EnvVar, err)
	}
	// registry commands still work, e.g. to add the missing entry
	mustRun(t, "instance", "add", "nope", "--host", "10.0.0.9")
	if out := mustRun(t, "config", "view"); !strings.Contains(out, "host: 10.0.0.9\n") {
		t.Errorf("unexpected view:\n%s", out)
	}
}

func TestConfigSetWithOverrideWritesThatEntry(t *testing.T) {
	withConfig(t, "")
	mustRun(t, "instance", "add", "lab", "--host", "10.0.0.1")
	mustRun(t, "instance", "add", "ipc2", "--host", "10.0.0.2")
	mustRun(t, "--instance", "ipc2", "config", "set", "cellname", "line2")

	r, _ := instances.Load(viper.GetViper())
	lab, _ := r.Get("lab")
	ipc2, _ := r.Get("ipc2")
	if lab.CellName != "" || ipc2.CellName != "line2" {
		t.Errorf("cell went to the wrong entry: lab=%+v ipc2=%+v", lab, ipc2)
	}
	if r.Current != "lab" {
		t.Errorf("selection changed to %q", r.Current)
	}
}

func TestCloudOnlyCommandsRejectOtherKinds(t *testing.T) {
	withConfig(t, "")
	mustRun(t, "instance", "add", "lab", "--host", "10.0.0.1")
	for _, args := range [][]string{
		{"instance", "stop", "lab"},
		{"instance", "start", "lab"},
		{"instance", "delete", "lab"},
		{"instance", "stop"},
	} {
		_, _, err := runWithConfig(t, args...)
		if err == nil || !strings.Contains(err.Error(), "only works for cloud instances") || !strings.Contains(err.Error(), "nova instance remove lab") {
			t.Errorf("nova %s: err = %v", strings.Join(args, " "), err)
		}
	}
}

func TestLifetimeCommandsRejectHosts(t *testing.T) {
	withConfig(t, "")
	mustRun(t, "instance", "add", "lab", "--host", "10.0.0.1")
	for _, args := range [][]string{
		{"instance", "extend", "lab"},
		{"instance", "keep-alive", "lab"},
		{"instance", "keep-alive", "lab", "--", "true"},
		{"instance", "keep-alive"},
	} {
		_, _, err := runWithConfig(t, args...)
		if err == nil || !strings.Contains(err.Error(), "only works for cloud and k8srun instances") {
			t.Errorf("nova %s: err = %v", strings.Join(args, " "), err)
		}
	}
}

func TestSelectAndRemove(t *testing.T) {
	withConfig(t, "")
	mustRun(t, "instance", "add", "lab", "--host", "10.0.0.1")
	mustRun(t, "instance", "add", "ipc2", "--host", "10.0.0.2")
	mustRun(t, "instance", "select", "ipc2")
	if out := mustRun(t, "instance", "info", "--host"); out != "10.0.0.2\n" {
		t.Errorf("info --host = %q", out)
	}

	_, _, err := runWithConfig(t, "instance", "add", "lab", "--host", "10.0.0.3")
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("duplicate add: err = %v", err)
	}

	mustRun(t, "instance", "remove", "ipc2")
	out := mustRun(t, "instance", "list")
	if strings.Contains(out, "ipc2") || strings.Contains(out, "*") {
		t.Errorf("removed entry listed or still selected:\n%s", out)
	}
	if _, _, err := runWithConfig(t, "instance", "info"); err == nil {
		t.Error("info without a selection must fail")
	}
}

// Nucleus keys left in an entry for older CLI versions aren't used and not shown
// (ADR-0018), not even with --show-secrets.
func TestInstanceInfoHidesLegacyNucleusKeys(t *testing.T) {
	withConfig(t, `current-instance: lab
instances:
  - name: lab
    kind: host
    host: 10.0.0.1
    nucleus-host: nucleus.dev.wandelbots.io
    nucleus-token: s3cret
`)
	for _, args := range [][]string{{"instance", "info", "lab"}, {"instance", "info", "lab", "--show-secrets"}} {
		out := mustRun(t, args...)
		if strings.Contains(out, "s3cret") || strings.Contains(out, "nucleus") {
			t.Errorf("%v shows the legacy Nucleus keys:\n%s", args, out)
		}
	}
}

func TestRegisterCloudInstanceNameTaken(t *testing.T) {
	withConfig(t, "")
	mustRun(t, "instance", "add", "abc", "--host", "192.168.1.10")
	mustRun(t, "instance", "add", "abc-2", "--host", "192.168.1.11")

	var errOut strings.Builder
	if err := registerCloudInstance(&errOut, "abc", "abc.instance.wandelbots.io", true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut.String(), `added cloud instance abc as "abc-3"`) {
		t.Errorf("expected a note about the name, got %q", errOut.String())
	}
	r, _ := instances.Load(viper.GetViper())
	got, ok := r.Get("abc-3")
	if !ok || got.Kind != instances.KindCloud || got.ID != "abc" || r.Current != "abc-3" {
		t.Fatalf("unexpected registry: %+v", r)
	}
	if h, _ := r.Get("abc"); h.Kind != instances.KindHost || h.Host != "192.168.1.10" {
		t.Errorf("the host entry must stay untouched: %+v", h)
	}

	// registering the same cloud instance again updates its entry instead of adding abc-4
	errOut.Reset()
	if err := registerCloudInstance(&errOut, "abc", "new.instance.wandelbots.io", true); err != nil {
		t.Fatal(err)
	}
	r, _ = instances.Load(viper.GetViper())
	if got, _ := r.Get("abc-3"); got.Host != "new.instance.wandelbots.io" || len(r.Instances) != 3 || errOut.Len() != 0 {
		t.Errorf("unexpected registry after re-register: %+v (note %q)", r, errOut.String())
	}
}

// --no-select on create: other sessions keep their target.
func TestRegisterCloudInstanceWithoutSelecting(t *testing.T) {
	withConfig(t, "")
	mustRun(t, "instance", "add", "lab", "--host", "192.168.1.10")

	var errOut strings.Builder
	if err := registerCloudInstance(&errOut, "abc", "abc.instance.wandelbots.io", false); err != nil {
		t.Fatal(err)
	}
	r, _ := instances.Load(viper.GetViper())
	if got, ok := r.Get("abc"); !ok || got.Kind != instances.KindCloud {
		t.Fatalf("abc not added: %+v", r)
	}
	if r.Current != "lab" {
		t.Errorf("selection changed to %q, want lab", r.Current)
	}
	if !strings.Contains(errOut.String(), "--instance abc") {
		t.Errorf("expected a hint how to target it, got %q", errOut.String())
	}
}

func TestInstanceFlagCompletion(t *testing.T) {
	withConfig(t, "")
	mustRun(t, "instance", "add", "lab-a", "--host", "192.168.1.10")
	mustRun(t, "instance", "add", "lab-b", "--host", "192.168.1.11")
	mustRun(t, "instance", "add", "other", "--host", "192.168.1.12")

	for _, args := range [][]string{
		{"__complete", "--instance", "lab"},
		{"__complete", "app", "list", "--instance", "lab"},
		{"__complete", "app", "list", "--instance=lab"},
	} {
		out := mustRun(t, args...)
		if !strings.Contains(out, "lab-a") || !strings.Contains(out, "lab-b") || strings.Contains(out, "other") {
			t.Errorf("%v: unexpected completion:\n%s", args, out)
		}
	}
	if out := mustRun(t, "__complete", "cell", "list", "--env", ""); !strings.Contains(out, "stg") {
		t.Errorf("--env completion:\n%s", out)
	}
}
