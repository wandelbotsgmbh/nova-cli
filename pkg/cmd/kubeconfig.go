package cmd

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	homedir "github.com/mitchellh/go-homedir"
	"github.com/spf13/cobra"
	"github.com/wandelbotsgmbh/nova-cli/pkg/instances"
	"github.com/wandelbotsgmbh/nova-cli/pkg/remote"
	"gopkg.in/yaml.v3"
)

// flags
var (
	kubeconfigSSHUser  string
	kubeconfigSSHPort  int
	kubeconfigNoSudo   bool
	kubeconfigRefresh  bool
	kubeconfigSetupSSH bool
	envShell           string
	envUnset           bool
)

// k3s keeps the admin kubeconfig root-readable at /etc/rancher/k3s/k3s.yaml;
// `k3s kubectl config view --raw` prints it including the client certificates.
const k3sKubeconfigCommand = "kubectl config view --raw"

var kubeconfigInstanceCmd = &cobra.Command{
	Use:               "kubeconfig [name]",
	Args:              cobra.MaximumNArgs(1),
	ValidArgsFunction: completeLocalInstance,
	Short:             "Prints the path of the instance's kubeconfig, fetching it over ssh for hosts",
	Long: `Prints the path of the kubeconfig of an instance (default: the targeted one).

- k8srun: the kubeconfig stored when the cluster was created.
- host: fetched once over ssh from k3s on the host ('k3s kubectl config view --raw',
  needs sudo), with the server address changed from 127.0.0.1 to the instance's host,
  and stored in ~/.nova/instances/<name>/kubeconfig (mode 0600). Use --refresh to fetch
  it again. The ssh user is remembered for the instance.
- cloud: not available.

Only the path is printed, never the content. The kubeconfig grants admin access to the
instance's Kubernetes cluster: don't hand it to an agent working unattended on an
instance that drives real robots.

Without a TTY, sudo can't prompt for a password. Run once with --setup-ssh-key to install
your ssh key and a k3s-only passwordless sudo rule (the same as
'nova app install --setup-ssh-key'), or use --no-sudo when logging in as root.`,
	Example: `$ nova instance kubeconfig lab --ssh-user admin --setup-ssh-key
$ kubectl --kubeconfig "$(nova instance kubeconfig lab)" get pods -A

# or set KUBECONFIG and NOVA_INSTANCE for this shell
$ eval "$(nova instance env lab)"`,
	RunE: func(cmd *cobra.Command, args []string) error {
		path, err := instanceKubeconfig(cmd, args)
		if err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), path)
		return nil
	},
}

var envInstanceCmd = &cobra.Command{
	Use:               "env [name]",
	Args:              cobra.MaximumNArgs(1),
	ValidArgsFunction: completeLocalInstance,
	Short:             "Prints shell commands that point this shell to an instance",
	Long: `Prints shell commands that set NOVA_INSTANCE and, where available, KUBECONFIG for an
instance (default: the targeted one). Evaluate them to point the current shell, and
everything started from it (nova, kubectl, helm, k9s), to that instance without changing
the selection of other sessions.

For host instances the kubeconfig is fetched over ssh if needed (see
'nova instance kubeconfig'); cloud instances have no kubeconfig, so only NOVA_INSTANCE is
set. --unset prints the commands that undo it.`,
	Example: `# bash / zsh
$ eval "$(nova instance env lab)"
$ eval "$(nova instance env --unset)"

# fish
$ nova instance env lab --shell fish | source

# PowerShell
PS> nova instance env lab --shell powershell | Invoke-Expression`,
	RunE: func(cmd *cobra.Command, args []string) error {
		shell := envShell
		if shell == "" {
			shell = detectShell()
		}
		if shell != "sh" && shell != "fish" && shell != "powershell" {
			return fmt.Errorf("invalid --shell %q: use sh, fish or powershell", shell)
		}
		out := cmd.OutOrStdout()

		if envUnset {
			writeEnv(out, shell, []envVar{{"NOVA_INSTANCE", ""}, {"KUBECONFIG", ""}}, true)
			return nil
		}

		inst, err := instanceArg(args)
		if err != nil {
			return err
		}
		vars := []envVar{{instances.EnvVar, inst.Name}}
		if inst.Kind != instances.KindCloud {
			path, err := instanceKubeconfig(cmd, args)
			if err != nil {
				return err
			}
			vars = append(vars, envVar{"KUBECONFIG", path})
		} else {
			fmt.Fprintf(cmd.ErrOrStderr(), "note: cloud instances have no kubeconfig, only %s is set\n", instances.EnvVar)
		}
		writeEnv(out, shell, vars, false)
		fmt.Fprintf(out, "# Run this command to configure your shell:\n# %s\n", evalHint(shell, inst.Name))
		return nil
	},
}

func init() {
	instanceCmd.AddCommand(kubeconfigInstanceCmd)
	instanceCmd.AddCommand(envInstanceCmd)

	for _, c := range []*cobra.Command{kubeconfigInstanceCmd, envInstanceCmd} {
		c.Flags().StringVar(&kubeconfigSSHUser, "ssh-user", "", "SSH user for fetching the kubeconfig of a host instance (remembered for the instance). If empty, the remembered user, your ssh config or the current user applies.")
		c.Flags().IntVar(&kubeconfigSSHPort, "ssh-port", 0, "SSH port. If 0, your ssh config or the default applies.")
		c.Flags().BoolVar(&kubeconfigNoSudo, "no-sudo", false, "Read the kubeconfig without sudo (e.g. when connecting as root).")
		c.Flags().BoolVar(&kubeconfigRefresh, "refresh", false, "Fetch the kubeconfig of a host instance again.")
		c.Flags().BoolVar(&kubeconfigSetupSSH, "setup-ssh-key", false, "Before fetching, install your SSH public key and (unless --no-sudo) a passwordless sudo rule for k3s on the host. Needs the SSH and one sudo password once.")
	}
	envInstanceCmd.Flags().StringVar(&envShell, "shell", "", "Shell syntax: sh (bash, zsh), fish or powershell. Detected from $SHELL if empty.")
	envInstanceCmd.Flags().BoolVar(&envUnset, "unset", false, "Print the commands that unset NOVA_INSTANCE and KUBECONFIG.")
}

// instanceArg returns the named entry, or the targeted one without a name.
func instanceArg(args []string) (instances.Instance, error) {
	if len(args) > 0 {
		r, err := loadRegistry()
		if err != nil {
			return instances.Instance{}, err
		}
		inst, ok := r.Get(strings.TrimSpace(args[0]))
		if !ok {
			return instances.Instance{}, fmt.Errorf("%w: %q (see 'nova instance list')", instances.ErrNotFound, args[0])
		}
		return inst, nil
	}
	t, err := ensureTarget()
	if err != nil {
		return instances.Instance{}, err
	}
	if t.Name == "" {
		return instances.Instance{}, fmt.Errorf("%w: select one with 'nova instance select <name>'", instances.ErrNoTarget)
	}
	return t, nil
}

// instanceKubeconfig returns the path of the instance's kubeconfig, fetching it over
// ssh for host instances when it isn't stored yet or --refresh is given.
func instanceKubeconfig(cmd *cobra.Command, args []string) (string, error) {
	inst, err := instanceArg(args)
	if err != nil {
		return "", err
	}

	stored := inst.Kubeconfig != "" && fileExists(inst.Kubeconfig)
	switch inst.Kind {
	case instances.KindCloud:
		return "", fmt.Errorf("cloud instance %q has no kubeconfig", inst.Name)
	case instances.KindK8sRun:
		if !stored {
			return "", fmt.Errorf("kubeconfig of %q is missing; recreate it with 'nova k8srun create --name %s --replace'", inst.Name, inst.Name)
		}
		return inst.Kubeconfig, nil
	}
	if stored && !kubeconfigRefresh {
		return inst.Kubeconfig, nil
	}

	if inst.Host == "" {
		return "", fmt.Errorf("instance %q has no host", inst.Name)
	}
	if !remote.IsOpenSSHInstalled() {
		return "", errors.New("fetching the kubeconfig requires the OpenSSH client (ssh) in PATH")
	}
	user := kubeconfigSSHUser
	if user == "" {
		user = inst.SSHUser
	}
	target := remote.Target{Host: hostname(inst.Host), User: user, Port: kubeconfigSSHPort}
	useSudo := !kubeconfigNoSudo

	if kubeconfigSetupSSH {
		if err := remote.SetupSSHAccess(target, "k3s", useSudo); err != nil {
			return "", err
		}
	}

	fmt.Fprintf(cmd.ErrOrStderr(), "Fetching the kubeconfig of %q from %s over ssh...\n", inst.Name, target.Host)
	content, err := remote.RunCapture(target, k3sKubeconfigRemoteCommand(target, useSudo))
	if err != nil {
		hint := ""
		if useSudo {
			hint = " (sudo needs a password? run once with --setup-ssh-key, or use --no-sudo when logging in as root)"
		}
		return "", fmt.Errorf("fetching the kubeconfig from %s failed: %w%s", target.Host, err, hint)
	}

	rewritten, err := rewriteKubeconfigServer([]byte(content), target.Host)
	if err != nil {
		return "", err
	}
	path, err := writeInstanceKubeconfig(inst.Name, rewritten)
	if err != nil {
		return "", err
	}

	r, err := loadRegistry()
	if err != nil {
		return "", err
	}
	if stored, ok := r.Get(inst.Name); ok {
		stored.Kubeconfig = path
		if kubeconfigSSHUser != "" {
			stored.SSHUser = kubeconfigSSHUser
		}
		if err := r.Put(stored); err != nil {
			return "", err
		}
		if err := saveRegistry(r); err != nil {
			return "", err
		}
	}
	return path, nil
}

// k3sKubeconfigRemoteCommand builds the remote command. With sudo, k3s is called by
// absolute path so the k3s-scoped NOPASSWD rule matches; -n fails instead of waiting
// for a password prompt that can't be shown without a TTY.
func k3sKubeconfigRemoteCommand(target remote.Target, useSudo bool) string {
	if !useSudo {
		return "k3s " + k3sKubeconfigCommand
	}
	k3s := "k3s"
	if abs, err := remote.ResolveCommandPath(target, "k3s"); err == nil && abs != "" {
		k3s = abs
	}
	if strings.HasPrefix(k3s, "/") {
		return "sudo -n " + k3s + " " + k3sKubeconfigCommand
	}
	return `sudo -n env PATH="$PATH" k3s ` + k3sKubeconfigCommand
}

// rewriteKubeconfigServer points clusters that use a loopback address (as k3s writes
// it) to host, keeping scheme and port.
func rewriteKubeconfigServer(content []byte, host string) ([]byte, error) {
	var doc map[string]any
	if err := yaml.Unmarshal(content, &doc); err != nil {
		return nil, fmt.Errorf("the host returned no valid kubeconfig: %w", err)
	}
	clusters, _ := doc["clusters"].([]any)
	if len(clusters) == 0 {
		return nil, errors.New("the host returned a kubeconfig without clusters")
	}
	for _, c := range clusters {
		entry, _ := c.(map[string]any)
		cluster, _ := entry["cluster"].(map[string]any)
		server, _ := cluster["server"].(string)
		u, err := url.Parse(server)
		if err != nil || u.Host == "" {
			continue
		}
		switch u.Hostname() {
		case "127.0.0.1", "localhost", "::1", "0.0.0.0":
			if port := u.Port(); port != "" {
				u.Host = net.JoinHostPort(host, port)
			} else {
				u.Host = host
			}
			cluster["server"] = u.String()
		}
	}
	return yaml.Marshal(doc)
}

// hostname strips scheme, port and path from an instance host.
func hostname(host string) string {
	h := stripHostScheme(host)
	if i := strings.Index(h, "/"); i >= 0 {
		h = h[:i]
	}
	if hst, _, err := net.SplitHostPort(h); err == nil {
		return hst
	}
	return h
}

func instanceDir(name string) (string, error) {
	home, err := homedir.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".nova", "instances", name), nil
}

func writeInstanceKubeconfig(name string, content []byte) (string, error) {
	dir, err := instanceDir(name)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "kubeconfig")
	return path, os.WriteFile(path, content, 0o600)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

type envVar struct{ name, value string }

func writeEnv(out io.Writer, shell string, vars []envVar, unset bool) {
	for _, v := range vars {
		switch {
		case shell == "fish" && unset:
			fmt.Fprintf(out, "set -e %s;\n", v.name)
		case shell == "fish":
			fmt.Fprintf(out, "set -gx %s %s;\n", v.name, shQuote(v.value))
		case shell == "powershell" && unset:
			fmt.Fprintf(out, "Remove-Item Env:\\%s -ErrorAction SilentlyContinue\n", v.name)
		case shell == "powershell":
			fmt.Fprintf(out, "$Env:%s = %s\n", v.name, psQuote(v.value))
		case unset:
			fmt.Fprintf(out, "unset %s\n", v.name)
		default:
			fmt.Fprintf(out, "export %s=%s\n", v.name, shQuote(v.value))
		}
	}
}

func evalHint(shell, name string) string {
	switch shell {
	case "fish":
		return "nova instance env " + name + " --shell fish | source"
	case "powershell":
		return "nova instance env " + name + " --shell powershell | Invoke-Expression"
	}
	return `eval "$(nova instance env ` + name + `)"`
}

func detectShell() string {
	if strings.HasSuffix(os.Getenv("SHELL"), "fish") {
		return "fish"
	}
	if runtime.GOOS == "windows" && os.Getenv("SHELL") == "" {
		return "powershell"
	}
	return "sh"
}

// shQuote single-quotes s for sh and fish.
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func psQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}
