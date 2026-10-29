package remote

import (
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// SetupSSHAccess prepares the target for password-free copy-image installs:
//   - installs the local SSH public key on the target (key-based login), and
//   - unless useSudo is false, grants passwordless sudo for the containerd tool
//     (ctrBinary, e.g. "k3s") via a validated /etc/sudoers.d drop-in.
//
// The only interactive prompts are the SSH login password (once, until the key
// is in place) and a single sudo password for the sudoers install; afterwards
// installs run without any prompt.
func SetupSSHAccess(t Target, ctrBinary string, useSudo bool) error {
	fmt.Printf("Installing SSH public key on %s...\n", t.Host)
	if err := installSSHKey(t); err != nil {
		return fmt.Errorf("failed to install ssh key: %w", err)
	}

	if !useSudo {
		// root login: no sudo, so nothing to configure
		return nil
	}

	fmt.Printf("Configuring passwordless sudo for %q on %s...\n", ctrBinary, t.Host)
	if err := configureNopasswdSudo(t, ctrBinary); err != nil {
		return fmt.Errorf("failed to configure passwordless sudo: %w", err)
	}
	return nil
}

// installSSHKey copies the local public key to the target. It prefers the
// system ssh-copy-id and falls back to appending the key over ssh (works on
// platforms without ssh-copy-id, e.g. Windows).
func installSSHKey(t Target) error {
	pubKeyPath, err := ensureLocalPublicKey()
	if err != nil {
		return err
	}

	if _, err := exec.LookPath("ssh-copy-id"); err == nil {
		cmd := exec.Command("ssh-copy-id", sshCopyIDArgs(t, pubKeyPath)...)
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}

	// portable fallback: append the key (idempotently) via ssh
	pubKey, err := os.ReadFile(pubKeyPath)
	if err != nil {
		return err
	}
	return runWithInput(t, "sh -c "+shellQuote(appendKeyScript), string(pubKey))
}

func sshCopyIDArgs(t Target, pubKeyPath string) []string {
	args := []string{"-i", pubKeyPath}
	if t.Port != 0 {
		args = append(args, "-p", strconv.Itoa(t.Port))
	}
	return append(args, t.destination())
}

// appendKeyScript reads a public key from stdin and appends it to
// authorized_keys only if not already present, creating ~/.ssh with safe perms.
const appendKeyScript = `k=$(cat); umask 077; mkdir -p ~/.ssh; touch ~/.ssh/authorized_keys; grep -qxF "$k" ~/.ssh/authorized_keys || printf '%s\n' "$k" >> ~/.ssh/authorized_keys`

// ensureLocalPublicKey returns the path to an existing default public key,
// generating an ed25519 keypair if the user has none.
func ensureLocalPublicKey() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	sshDir := filepath.Join(home, ".ssh")

	for _, name := range []string{"id_ed25519.pub", "id_rsa.pub", "id_ecdsa.pub"} {
		p := filepath.Join(sshDir, name)
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}

	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		return "", err
	}
	keyPath := filepath.Join(sshDir, "id_ed25519")
	fmt.Println("No SSH key found; generating an ed25519 keypair...")
	cmd := exec.Command("ssh-keygen", "-t", "ed25519", "-N", "", "-f", keyPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return keyPath + ".pub", nil
}

// configureNopasswdSudo writes a validated /etc/sudoers.d/nova-cli granting the
// remote user passwordless sudo for the containerd binary only.
func configureNopasswdSudo(t Target, ctrBinary string) error {
	user, binPath, err := resolveUserAndBinary(t, ctrBinary)
	if err != nil {
		return err
	}

	rule := sudoersRuleContent(user, binPath)
	return Run(t, sudoersInstallCommand(rule), true)
}

// resolveUserAndBinary looks up the effective remote username and the absolute
// path of ctrBinary, using the login shell's PATH (which includes /usr/local/bin
// where k3s lives, unlike sudo's secure_path).
func resolveUserAndBinary(t Target, ctrBinary string) (user string, binPath string, err error) {
	// an already-absolute binary needs no lookup
	lookup := "command -v " + ctrBinary
	if strings.HasPrefix(ctrBinary, "/") {
		lookup = "printf '%s\\n' " + shellQuote(ctrBinary)
	}

	out, err := RunCapture(t, "sh -c "+shellQuote("whoami; "+lookup))
	if err != nil {
		return "", "", err
	}

	lines := []string{}
	for l := range strings.SplitSeq(out, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) < 2 {
		return "", "", fmt.Errorf("could not locate %q on the remote (is k3s installed?)", ctrBinary)
	}
	return lines[0], lines[len(lines)-1], nil
}

func sudoersRuleContent(user string, binPath string) string {
	return fmt.Sprintf(`# Added by the nova CLI (nova app install --setup-ssh-key).
# Allows passwordless sudo for k3s so app images can be imported into containerd.
%s ALL=(root) NOPASSWD: %s
`, user, binPath)
}

// sudoersInstallCommand builds a single root command that stages the rule in a
// freshly created, root-owned temp file (mktemp — unpredictable name, 0600 via
// umask, immune to the /tmp symlink race a predictable path would allow),
// validates it with visudo and only then installs it as /etc/sudoers.d/nova-cli.
// The rule is carried base64-encoded so its contents never touch the shell.
func sudoersInstallCommand(rule string) string {
	b64 := base64.StdEncoding.EncodeToString([]byte(rule))
	script := fmt.Sprintf(`set -e
umask 077
t=$(mktemp)
trap 'rm -f "$t"' EXIT
printf %%s %s | base64 -d > "$t"
visudo -cf "$t"
install -m 0440 -o root -g root "$t" /etc/sudoers.d/nova-cli`, shellQuote(b64))
	return "sudo sh -c " + shellQuote(script)
}

// runWithInput runs an ssh command feeding input on its stdin. ssh still reads
// any password from /dev/tty, so login prompts keep working.
func runWithInput(t Target, command string, input string) error {
	args := sshArgs(t, command, false)
	cmd := exec.Command("ssh", args...)
	cmd.Stdin = strings.NewReader(input)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
