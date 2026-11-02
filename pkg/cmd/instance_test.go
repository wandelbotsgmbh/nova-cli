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
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv(instances.EnvVar, "")
	homedir.DisableCache = true
	// a config.yaml in the working directory would shadow the test config
	t.Chdir(home)
	return path
}

// run executes the root command like main does and returns stdout and stderr.
func runNova(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	viper.Reset()
	resetTarget()
	instanceFlag = ""
	showSecretsConfigView = false
	showSecretsInstanceInfoCmd = false
	printHostInstanceInfoCmd, printInstanceIdInstanceInfoCmd = false, false
	listCloudInstances, listVerboseInstances = false, false
	k8srunName, k8srunReplace = "", false
	hostAddInstanceCmd, cellAddInstanceCmd, selectAddInstanceCmd, sshUserAddInstanceCmd = "", "", false, ""
	kubeconfigSSHUser, kubeconfigSSHPort, kubeconfigNoSudo, kubeconfigRefresh, kubeconfigSetupSSH = "", 0, false, false, false
	envShell, envUnset = "", false

	var stdout, stderr bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&stderr)
	rootCmd.SetArgs(args)
	err := rootCmd.Execute()
	return stdout.String(), stderr.String(), err
}

func mustRun(t *testing.T, args ...string) string {
	t.Helper()
	out, errOut, err := runNova(t, args...)
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
	path := withConfig(t, "host: 192.168.1.10\ncellname: line1\nnucleus-token: s3cret\n")
	before := readConfig(t, path)

	out := mustRun(t, "config", "view")
	for _, want := range []string{
		"host: 192.168.1.10\n",
		"cellname: line1\n",
		"nucleus-token: " + instances.Redact + "\n",
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
	mustRun(t, "config", "set", "nucleus-host", "nucleus.example.com")

	r, err := instances.Load(viper.GetViper())
	if err != nil {
		t.Fatal(err)
	}
	def, ok := r.Get(instances.DefaultName)
	if !ok || def.Host != "192.168.1.10" || def.NucleusHost != "nucleus.example.com" {
		t.Fatalf("unexpected registry: %+v", r)
	}
	if content := readConfig(t, path); !strings.Contains(content, "editor: vim") || !strings.Contains(content, "host: 192.168.1.10") {
		t.Errorf("legacy and unrelated keys must be kept:\n%s", content)
	}
}

func TestConfigSetHostOnEmptyConfigCreatesDefault(t *testing.T) {
	withConfig(t, "")
	_, errOut, err := runNova(t, "config", "set", "host", "10.0.0.1")
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
	_, _, err := runNova(t, "config", "view")
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
	mustRun(t, "--instance", "ipc2", "config", "set", "nucleus-token", "tok2")

	r, _ := instances.Load(viper.GetViper())
	lab, _ := r.Get("lab")
	ipc2, _ := r.Get("ipc2")
	if lab.NucleusToken != "" || ipc2.NucleusToken != "tok2" {
		t.Errorf("token went to the wrong entry: lab=%+v ipc2=%+v", lab, ipc2)
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
		_, _, err := runNova(t, args...)
		if err == nil || !strings.Contains(err.Error(), "only works for cloud instances") || !strings.Contains(err.Error(), "nova instance remove lab") {
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

	_, _, err := runNova(t, "instance", "add", "lab", "--host", "10.0.0.3")
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("duplicate add: err = %v", err)
	}

	mustRun(t, "instance", "remove", "ipc2")
	out := mustRun(t, "instance", "list")
	if strings.Contains(out, "ipc2") || strings.Contains(out, "*") {
		t.Errorf("removed entry listed or still selected:\n%s", out)
	}
	if _, _, err := runNova(t, "instance", "info"); err == nil {
		t.Error("info without a selection must fail")
	}
}

func TestInstanceInfoRedacts(t *testing.T) {
	withConfig(t, "")
	mustRun(t, "instance", "add", "lab", "--host", "10.0.0.1")
	mustRun(t, "config", "set", "nucleus-token", "s3cret")
	out := mustRun(t, "instance", "info", "lab")
	if strings.Contains(out, "s3cret") || !strings.Contains(out, instances.Redact) {
		t.Errorf("info must redact:\n%s", out)
	}
	if out := mustRun(t, "instance", "info", "lab", "--show-secrets"); !strings.Contains(out, "s3cret") {
		t.Errorf("--show-secrets must print the token:\n%s", out)
	}
}
