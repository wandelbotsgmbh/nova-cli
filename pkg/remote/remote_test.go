package remote

import (
	"reflect"
	"strings"
	"testing"
)

func TestScpArgs(t *testing.T) {
	tests := []struct {
		name     string
		target   Target
		expected []string
	}{
		{"Host only", Target{Host: "172.31.13.182"},
			[]string{"app.tar.gz", "172.31.13.182:/tmp/app.tar.gz"}},
		{"User and port", Target{Host: "nova.local", User: "nova.admin", Port: 2222},
			[]string{"-P", "2222", "app.tar.gz", "nova.admin@nova.local:/tmp/app.tar.gz"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			actual := scpArgs(tc.target, "app.tar.gz", "/tmp/app.tar.gz")
			if !reflect.DeepEqual(actual, tc.expected) {
				t.Errorf("scpArgs() = %v, expected %v", actual, tc.expected)
			}
		})
	}
}

func TestSshArgs(t *testing.T) {
	tests := []struct {
		name     string
		target   Target
		tty      bool
		expected []string
	}{
		{"Host only", Target{Host: "172.31.13.182"}, false,
			[]string{"172.31.13.182", "uname -a"}},
		{"User, port and tty", Target{Host: "nova.local", User: "root", Port: 2222}, true,
			[]string{"-t", "-p", "2222", "root@nova.local", "uname -a"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			actual := sshArgs(tc.target, "uname -a", tc.tty)
			if !reflect.DeepEqual(actual, tc.expected) {
				t.Errorf("sshArgs() = %v, expected %v", actual, tc.expected)
			}
		})
	}
}

func TestImportCommand(t *testing.T) {
	tests := []struct {
		name    string
		opts    ImportImageOptions
		wantCmd string // import-command fragment expected in the pipeline
	}{
		{"With sudo", ImportImageOptions{UseSudo: true, ImportCommand: "k3s ctr images import"},
			`sudo env PATH="$PATH" k3s ctr images import -`},
		{"Without sudo", ImportImageOptions{UseSudo: false, ImportCommand: "k3s ctr images import"},
			"k3s ctr images import -"},
		{"Custom import command", ImportImageOptions{UseSudo: true, ImportCommand: "ctr -n k8s.io images import"},
			`sudo env PATH="$PATH" ctr -n k8s.io images import -`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := importCommand(tc.opts, "/tmp/app.tar.gz")
			for _, want := range []string{"sh -c ", "gunzip -c", tc.wantCmd, "/tmp/app.tar.gz", "rm -f"} {
				if !strings.Contains(got, want) {
					t.Errorf("importCommand() missing %q\nfull: %s", want, got)
				}
			}
		})
	}
}

func TestImportCommandQuotesPath(t *testing.T) {
	// a remote path with a space must be quoted so it stays a single argument.
	// The inner quotes are escaped once more for the outer `sh -c '...'` wrapper.
	got := importCommand(ImportImageOptions{UseSudo: false, ImportCommand: "k3s ctr images import"}, "/tmp/nova images/app.tar.gz")
	if !strings.Contains(got, `'\''/tmp/nova images/app.tar.gz'\''`) {
		t.Errorf("path with space must be quoted, got: %s", got)
	}
}

func TestSudoWrap(t *testing.T) {
	tests := []struct {
		name    string
		command string
		useSudo bool
		want    string
	}{
		{"no sudo", "k3s ctr images import", false, "k3s ctr images import"},
		{"relative gets env PATH", "k3s ctr images import", true, `sudo env PATH="$PATH" k3s ctr images import`},
		{"absolute runs directly", "/usr/local/bin/k3s ctr images import", true, "sudo /usr/local/bin/k3s ctr images import"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := sudoWrap(tc.command, tc.useSudo); got != tc.want {
				t.Errorf("sudoWrap(%q, %v) = %q, want %q", tc.command, tc.useSudo, got, tc.want)
			}
		})
	}
}

func TestImportCommandAbsolutePathNoEnv(t *testing.T) {
	// an absolute import command must not get the env PATH wrapper, so a
	// binary-scoped NOPASSWD sudoers rule can match
	got := importCommand(ImportImageOptions{UseSudo: true, ImportCommand: "/usr/local/bin/k3s ctr images import"}, "/tmp/a.tar.gz")
	if strings.Contains(got, "env PATH") {
		t.Errorf("absolute import command should not use env PATH wrapper: %s", got)
	}
	if !strings.Contains(got, "sudo /usr/local/bin/k3s ctr images import -") {
		t.Errorf("expected direct sudo of absolute path: %s", got)
	}
}

func TestReassembleCommand(t *testing.T) {
	opts := IncrementalImportOptions{
		RemoteDir:    "/tmp",
		UseSudo:      true,
		CtrCommand:   "k3s ctr",
		FromStoreHex: []string{"aaa", "bbb"},
	}
	got := reassembleCommand(opts, "/tmp/partial.tar.gz")

	mustContain := []string{
		"sh -c '",
		"set -e",
		// paths carried in quoted shell vars, referenced as "$VAR"
		`DIR=$(mktemp -d "$REMOTEDIR/nova-oci-XXXXXX")`,
		// inner single quotes get escaped by shellQuote for the outer sh -c '...'
		`trap '\''rm -rf "$DIR" "$ARCHIVE"'\'' EXIT`,
		`tar xzf "$ARCHIVE" -C "$DIR"`,
		`sudo env PATH="$PATH" k3s ctr content get sha256:aaa > "$DIR/blobs/sha256/aaa"`,
		`sudo env PATH="$PATH" k3s ctr content get sha256:bbb > "$DIR/blobs/sha256/bbb"`,
		`tar c -C "$DIR" . | sudo env PATH="$PATH" k3s ctr images import -`,
	}
	for _, m := range mustContain {
		if !strings.Contains(got, m) {
			t.Errorf("reassembleCommand() missing %q\nfull: %s", m, got)
		}
	}
}

func TestReassembleCommandQuotesRemoteDir(t *testing.T) {
	// a --remote-dir with a space must be quoted, not split into two args
	opts := IncrementalImportOptions{RemoteDir: "/tmp/nova images", UseSudo: false, CtrCommand: "k3s ctr"}
	got := reassembleCommand(opts, "/tmp/nova images/p.tar.gz")
	// inner quotes are escaped once more for the outer `sh -c '...'` wrapper
	if !strings.Contains(got, `REMOTEDIR='\''/tmp/nova images'\''`) {
		t.Errorf("remote dir with space must be quoted, got: %s", got)
	}
}

func TestReassembleCommandNoSudo(t *testing.T) {
	opts := IncrementalImportOptions{RemoteDir: "/tmp", UseSudo: false, CtrCommand: "k3s ctr", FromStoreHex: nil}
	got := reassembleCommand(opts, "/tmp/p.tar.gz")
	if strings.Contains(got, "sudo") {
		t.Errorf("expected no sudo, got: %s", got)
	}
	if !strings.Contains(got, `tar c -C "$DIR" . | k3s ctr images import -`) {
		t.Errorf("missing import pipeline, got: %s", got)
	}
}

func TestRemoteContentDigestsParsing(t *testing.T) {
	// exercise the parsing logic directly (RunCapture is not invoked)
	out := "sha256:aaa\n  sha256:bbb  \ngarbage\n\nsha256:ccc\n"
	digests := map[string]bool{}
	for line := range strings.SplitSeq(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "sha256:") {
			digests[line] = true
		}
	}
	for _, d := range []string{"sha256:aaa", "sha256:bbb", "sha256:ccc"} {
		if !digests[d] {
			t.Errorf("expected %s parsed", d)
		}
	}
	if len(digests) != 3 {
		t.Errorf("expected 3 digests, got %d", len(digests))
	}
}
