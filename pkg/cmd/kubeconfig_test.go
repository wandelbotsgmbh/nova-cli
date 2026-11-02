package cmd

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/spf13/viper"
	"github.com/wandelbotsgmbh/nova-cli/pkg/instances"
	"gopkg.in/yaml.v3"
)

const k3sKubeconfig = `apiVersion: v1
clusters:
- cluster:
    certificate-authority-data: Q0E=
    server: https://127.0.0.1:6443
  name: default
contexts:
- context:
    cluster: default
    user: default
  name: default
current-context: default
kind: Config
users:
- name: default
  user:
    client-key-data: S0VZ
`

// fakeSSH puts an "ssh" on PATH that answers like a k3s host and logs its arguments.
func fakeSSH(t *testing.T) (logFile string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake ssh is a shell script")
	}
	dir := t.TempDir()
	logFile = filepath.Join(dir, "ssh.log")
	script := `#!/bin/sh
echo "$@" >> ` + logFile + `
for last; do :; done
case "$last" in
  *"command -v k3s"*) echo /usr/local/bin/k3s ;;
  *"kubectl config view --raw"*) cat <<'KUBE'
` + k3sKubeconfig + `KUBE
  ;;
esac
`
	for _, name := range []string{"ssh", "scp"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return logFile
}

func TestRewriteKubeconfigServer(t *testing.T) {
	out, err := rewriteKubeconfigServer([]byte(k3sKubeconfig), "192.168.1.10")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Clusters []struct {
			Cluster struct {
				Server string `yaml:"server"`
				CA     string `yaml:"certificate-authority-data"`
			} `yaml:"cluster"`
		} `yaml:"clusters"`
	}
	if err := yaml.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	if got := doc.Clusters[0].Cluster.Server; got != "https://192.168.1.10:6443" {
		t.Errorf("server = %q", got)
	}
	if doc.Clusters[0].Cluster.CA != "Q0E=" {
		t.Error("other fields must be kept")
	}

	if _, err := rewriteKubeconfigServer([]byte("sudo: a password is required\n"), "h"); err == nil {
		t.Error("garbage must be rejected")
	}
}

func TestHostname(t *testing.T) {
	for in, want := range map[string]string{
		"192.168.1.10":              "192.168.1.10",
		"http://192.168.1.10:8080/": "192.168.1.10",
		"https://ipc.example.com":   "ipc.example.com",
		"ipc.example.com/api":       "ipc.example.com",
	} {
		if got := hostname(in); got != want {
			t.Errorf("hostname(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestKubeconfigOfHostOverSSH(t *testing.T) {
	withConfig(t, "")
	log := fakeSSH(t)
	mustRun(t, "instance", "add", "lab", "--host", "http://192.168.1.10", "--ssh-user", "admin")

	path := strings.TrimSpace(mustRun(t, "instance", "kubeconfig", "lab"))
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "server: https://192.168.1.10:6443") {
		t.Errorf("server not rewritten:\n%s", content)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Errorf("kubeconfig mode = %v", fi.Mode().Perm())
	}
	calls, _ := os.ReadFile(log)
	if !strings.Contains(string(calls), "admin@192.168.1.10 sudo -n /usr/local/bin/k3s kubectl config view --raw") {
		t.Errorf("unexpected ssh calls:\n%s", calls)
	}

	// stored: no second fetch unless --refresh
	_ = os.Remove(log)
	mustRun(t, "instance", "kubeconfig", "lab")
	if _, err := os.Stat(log); err == nil {
		t.Error("stored kubeconfig must not be fetched again")
	}
	mustRun(t, "instance", "kubeconfig", "lab", "--refresh", "--no-sudo")
	if calls, _ := os.ReadFile(log); !strings.Contains(string(calls), "admin@192.168.1.10 k3s kubectl config view --raw") {
		t.Errorf("--refresh --no-sudo: unexpected ssh calls:\n%s", calls)
	}

	r, _ := instances.Load(viper.GetViper())
	if lab, _ := r.Get("lab"); lab.Kubeconfig != path {
		t.Errorf("kubeconfig path not stored: %+v", lab)
	}

	mustRun(t, "instance", "remove", "lab")
	if _, err := os.Stat(path); err == nil {
		t.Error("remove must delete the stored kubeconfig")
	}
}

func TestInstanceEnv(t *testing.T) {
	withConfig(t, "")
	fakeSSH(t)
	t.Setenv("SHELL", "/bin/zsh")
	mustRun(t, "instance", "add", "lab", "--host", "192.168.1.10")
	path := strings.TrimSpace(mustRun(t, "instance", "kubeconfig", "lab"))

	out := mustRun(t, "instance", "env", "lab")
	for _, want := range []string{
		"export NOVA_INSTANCE='lab'\n",
		"export KUBECONFIG='" + path + "'\n",
		`# eval "$(nova instance env lab)"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("sh output missing %q:\n%s", want, out)
		}
	}
	if out := mustRun(t, "instance", "env", "lab", "--shell", "fish"); !strings.Contains(out, "set -gx NOVA_INSTANCE 'lab';") {
		t.Errorf("fish output:\n%s", out)
	}
	if out := mustRun(t, "instance", "env", "lab", "--shell", "powershell"); !strings.Contains(out, "$Env:KUBECONFIG = '"+path+"'") {
		t.Errorf("powershell output:\n%s", out)
	}
	if out := mustRun(t, "instance", "env", "--unset"); out != "unset NOVA_INSTANCE\nunset KUBECONFIG\n" {
		t.Errorf("unset output: %q", out)
	}
	if _, _, err := runNova(t, "instance", "env", "lab", "--shell", "tcsh"); err == nil {
		t.Error("unknown shell must fail")
	}
}

func TestKubeconfigOfOtherKinds(t *testing.T) {
	withConfig(t, `current-instance: c
instances:
  - name: c
    kind: cloud
    host: abc.instance.wandelbots.io
    id: abc
`)
	if _, _, err := runNova(t, "instance", "kubeconfig", "c"); err == nil || !strings.Contains(err.Error(), "no kubeconfig") {
		t.Errorf("cloud: err = %v", err)
	}
	out, errOut, err := runNova(t, "instance", "env")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "KUBECONFIG") || !strings.Contains(out, "NOVA_INSTANCE='c'") || !strings.Contains(errOut, "no kubeconfig") {
		t.Errorf("cloud env:\n%s\n%s", out, errOut)
	}
}

func TestKubeconfigOfK8sRun(t *testing.T) {
	withConfig(t, "")
	newFakeK8sRunService(t)
	mustRun(t, "k8srun", "create", "--name", "sim")
	path := strings.TrimSpace(mustRun(t, "instance", "kubeconfig"))
	if b, err := os.ReadFile(path); err != nil || string(b) != "kubeconfig-1" {
		t.Errorf("k8srun kubeconfig: %q, %v", b, err)
	}
}
