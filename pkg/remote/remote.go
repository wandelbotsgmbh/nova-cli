// Package remote transfers files to and runs commands on a Nova instance
// via the system OpenSSH client (ssh/scp), mirroring how the CLI shells out
// to docker and kubectl. No ssh options are injected besides the port, so the
// user's ~/.ssh/config, keys, agent and interactive prompts (password,
// host key, sudo) work as usual.
package remote

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
)

// An ssh/scp destination.
type Target struct {
	// hostname or ip, without scheme
	Host string
	// optional login user; empty lets ssh_config or the current user apply
	User string
	// optional port; 0 lets ssh_config or the default apply
	Port int
}

func (t Target) destination() string {
	if t.User != "" {
		return t.User + "@" + t.Host
	}
	return t.Host
}

func IsOpenSSHInstalled() bool {
	_, sshErr := exec.LookPath("ssh")
	_, scpErr := exec.LookPath("scp")
	return sshErr == nil && scpErr == nil
}

// Copies a local file to remotePath on the target via scp.
// scp's own progress meter is printed to the terminal.
func CopyFile(t Target, localPath string, remotePath string) error {
	args := scpArgs(t, localPath, remotePath)
	cmd := exec.Command("scp", args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to copy %s to %s: %w", localPath, t.Host, err)
	}
	return nil
}

func scpArgs(t Target, localPath string, remotePath string) []string {
	args := []string{}
	if t.Port != 0 {
		args = append(args, "-P", strconv.Itoa(t.Port))
	}
	return append(args, localPath, t.destination()+":"+remotePath)
}

// Runs a command on the target via ssh. allocateTTY forces a tty (-t) so
// interactive prompts like sudo's password prompt work.
func Run(t Target, command string, allocateTTY bool) error {
	args := sshArgs(t, command, allocateTTY)
	cmd := exec.Command("ssh", args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func sshArgs(t Target, command string, allocateTTY bool) []string {
	args := []string{}
	if allocateTTY {
		args = append(args, "-t")
	}
	if t.Port != 0 {
		args = append(args, "-p", strconv.Itoa(t.Port))
	}
	return append(args, t.destination(), command)
}

// RunCapture runs a command on the target via ssh and returns its stdout.
// No TTY is allocated, so stdout stays clean for parsing; stderr streams to the
// terminal so ssh's own auth prompts remain visible. The command must therefore
// not require an interactive sudo prompt (sudo would print to the captured
// stdout under a TTY) — see RemoteContentDigests for how that is handled.
func RunCapture(t Target, command string) (string, error) {
	args := sshArgs(t, command, false)
	cmd := exec.Command("ssh", args...)
	var out bytes.Buffer
	cmd.Stdin = os.Stdin
	cmd.Stdout = &out
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	return out.String(), err
}

type ImportImageOptions struct {
	Target Target
	// remote directory for the temporary image archive, e.g. /tmp
	RemoteDir string
	// run the import command with sudo
	UseSudo bool
	// remote command that reads an image tar from stdin, e.g. "k3s ctr images import"
	ImportCommand string
}

// Copies a gzipped image archive to the target and imports it into containerd
// in a single ssh session (one auth prompt). The remote archive is removed
// afterwards while preserving the import's exit code.
func CopyAndImportImage(opts ImportImageOptions, localArchivePath string) error {
	// path (not filepath) keeps the remote path unix-style on windows builds
	remotePath := path.Join(opts.RemoteDir, filepath.Base(localArchivePath))

	fmt.Printf("Copying image archive to %s...\n", opts.Target.Host)
	if err := CopyFile(opts.Target, localArchivePath, remotePath); err != nil {
		return err
	}

	fmt.Printf("Importing image into containerd on %s...\n", opts.Target.Host)
	if err := Run(opts.Target, importCommand(opts, remotePath), true); err != nil {
		return fmt.Errorf("failed to import image on %s: %w", opts.Target.Host, err)
	}
	return nil
}

func importCommand(opts ImportImageOptions, remotePath string) string {
	importCmd := sudoWrap(opts.ImportCommand, opts.UseSudo)
	// quote the path so a --remote-dir with spaces or shell metacharacters is safe
	p := shellQuote(remotePath)
	inner := fmt.Sprintf("gunzip -c %[1]s | %[2]s -; rc=$?; rm -f %[1]s; exit $rc", p, importCmd)
	return "sh -c " + shellQuote(inner)
}

// sudoWrap prefixes a command with sudo when needed. If the command's binary is
// given by absolute path, sudo runs it directly — this both avoids sudo's
// secure_path (which lacks /usr/local/bin where k3s lives) and lets a
// binary-scoped NOPASSWD sudoers rule match. For a relative binary we fall back
// to carrying the caller's PATH in, which still works with password sudo.
func sudoWrap(command string, useSudo bool) string {
	if !useSudo {
		return command
	}
	if strings.HasPrefix(command, "/") {
		return "sudo " + command
	}
	return `sudo env PATH="$PATH" ` + command
}

// ResolveCommandPath returns the absolute path of a binary on the target using
// the login shell's PATH (`command -v`). Returns an empty string with no error
// if the binary is not found.
func ResolveCommandPath(t Target, binary string) (string, error) {
	if strings.HasPrefix(binary, "/") {
		return binary, nil
	}
	out, err := RunCapture(t, "sh -c "+shellQuote("command -v "+binary))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// RemoteContentDigests returns the set of blob digests ("sha256:<hex>") present
// in the target's containerd content store, via `<ctr> content ls -q`.
//
// The listing needs sudo (the containerd socket is root-only), which requires a
// TTY to prompt for a password. But a TTY merges the prompt into stdout, which
// we need to parse. So the listing is run interactively with its output
// redirected to a user-owned temp file on the remote (only the sudo prompt
// reaches the terminal), then that file is read back over a clean capture.
func RemoteContentDigests(t Target, ctrCommand string, remoteDir string, useSudo bool) (map[string]bool, error) {
	tmp := shellQuote(path.Join(remoteDir, fmt.Sprintf("nova-content-%d.txt", os.Getpid())))

	ls := sudoWrap(ctrCommand+" content ls -q", useSudo)
	// the redirect is performed by the login shell (as the user), so tmp is
	// user-owned and can be read back without sudo; remove tmp if the listing
	// fails so a partial run doesn't leave the (empty) file behind. tmp is
	// shell-quoted so a --remote-dir with spaces/metacharacters is safe.
	if err := Run(t, "sh -c "+shellQuote(ls+" > "+tmp+" || { rm -f "+tmp+"; exit 1; }"), true); err != nil {
		return nil, fmt.Errorf("failed to list remote content store: %w", err)
	}

	out, err := RunCapture(t, "sh -c "+shellQuote("cat "+tmp+"; rm -f "+tmp))
	if err != nil {
		return nil, fmt.Errorf("failed to read remote content store listing: %w", err)
	}

	digests := map[string]bool{}
	for line := range strings.SplitSeq(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "sha256:") {
			digests[line] = true
		}
	}
	return digests, nil
}

type IncrementalImportOptions struct {
	Target       Target
	RemoteDir    string
	UseSudo      bool
	CtrCommand   string   // ctr base, e.g. "k3s ctr"
	FromStoreHex []string // layer hex digests to pull from the remote content store
}

// CopyAndImportIncremental copies a partial OCI archive (containing only the
// blobs missing on the target) and reassembles the full image on the remote by
// fetching the remaining blobs from its content store, then imports it — all in
// a single ssh session. The remote layout dir and archive are always cleaned up
// while preserving the import's exit code.
func CopyAndImportIncremental(opts IncrementalImportOptions, localPartialPath string) error {
	remotePath := path.Join(opts.RemoteDir, filepath.Base(localPartialPath))

	fmt.Printf("Copying missing layers to %s...\n", opts.Target.Host)
	if err := CopyFile(opts.Target, localPartialPath, remotePath); err != nil {
		return err
	}

	fmt.Printf("Reassembling and importing image on %s...\n", opts.Target.Host)
	if err := Run(opts.Target, reassembleCommand(opts, remotePath), true); err != nil {
		return fmt.Errorf("failed to import image on %s: %w", opts.Target.Host, err)
	}
	return nil
}

func reassembleCommand(opts IncrementalImportOptions, remotePath string) string {
	ctr := sudoWrap(opts.CtrCommand, opts.UseSudo)

	// each entry pulls a present-remotely blob out of the content store into the
	// reassembled layout; set -e aborts (→ non-zero → caller falls back) if any
	// expected blob was garbage-collected in the meantime
	var fetches strings.Builder
	for _, hex := range opts.FromStoreHex {
		fmt.Fprintf(&fetches, "%s content get sha256:%s > \"$DIR/blobs/sha256/%s\"\n", ctr, hex, hex)
	}

	// paths are assigned to shell variables (quoted once) and referenced as
	// "$VAR" so a --remote-dir with spaces or metacharacters is safe
	script := fmt.Sprintf(`set -e
ARCHIVE=%[1]s
REMOTEDIR=%[2]s
DIR=$(mktemp -d "$REMOTEDIR/nova-oci-XXXXXX")
trap 'rm -rf "$DIR" "$ARCHIVE"' EXIT
tar xzf "$ARCHIVE" -C "$DIR"
mkdir -p "$DIR/blobs/sha256"
%[3]star c -C "$DIR" . | %[4]s images import -
`, shellQuote(remotePath), shellQuote(opts.RemoteDir), fetches.String(), ctr)

	return "sh -c " + shellQuote(script)
}

// shellQuote wraps s in single quotes for safe use as one ssh remote argument.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
