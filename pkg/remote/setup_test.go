package remote

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestSudoersRuleContent(t *testing.T) {
	got := sudoersRuleContent("nova.admin", "/usr/local/bin/k3s")

	if !strings.Contains(got, "nova.admin ALL=(root) NOPASSWD: /usr/local/bin/k3s") {
		t.Errorf("rule missing expected NOPASSWD line:\n%s", got)
	}
	if !strings.HasPrefix(got, "#") {
		t.Errorf("rule should start with an explanatory comment:\n%s", got)
	}
	if !strings.HasSuffix(got, "\n") {
		t.Errorf("rule should end with a newline")
	}
}

func TestSudoersInstallCommand(t *testing.T) {
	rule := "nova.admin ALL=(root) NOPASSWD: /usr/local/bin/k3s\n"
	got := sudoersInstallCommand(rule)

	for _, want := range []string{
		"sudo sh -c '",
		"umask 077",
		"t=$(mktemp)",                  // unpredictable, root-owned staging (no /tmp race)
		`trap '\''rm -f "$t"'\'' EXIT`, // always cleaned up (inner quotes escaped for outer sh -c)
		`visudo -cf "$t"`,              // validate before install
		`install -m 0440 -o root -g root "$t" /etc/sudoers.d/nova-cli`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("install command missing %q\nfull: %s", want, got)
		}
	}
	// the rule must be carried base64-encoded, not interpolated into the shell
	b64 := base64.StdEncoding.EncodeToString([]byte(rule))
	if !strings.Contains(got, b64) {
		t.Errorf("expected base64-encoded rule %q in command:\n%s", b64, got)
	}
	if strings.Contains(got, "NOPASSWD") {
		t.Errorf("raw rule text must not appear in the shell command:\n%s", got)
	}
	// no predictable /tmp path
	if strings.Contains(got, "nova-sudoers-") {
		t.Errorf("staging must use mktemp, not a predictable path:\n%s", got)
	}
}

func TestSSHCopyIDArgs(t *testing.T) {
	tests := []struct {
		name   string
		target Target
		want   []string
	}{
		{"host only", Target{Host: "h"}, []string{"-i", "/k.pub", "h"}},
		{"user and port", Target{Host: "h", User: "u", Port: 2222}, []string{"-i", "/k.pub", "-p", "2222", "u@h"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := sshCopyIDArgs(tc.target, "/k.pub")
			if strings.Join(got, " ") != strings.Join(tc.want, " ") {
				t.Errorf("sshCopyIDArgs = %v, want %v", got, tc.want)
			}
		})
	}
}

// The drop-in filename must not contain a dot (sudo ignores such files).
func TestSudoersDropInFilename(t *testing.T) {
	got := sudoersInstallCommand("/tmp/x")
	if !strings.Contains(got, "/etc/sudoers.d/nova-cli") {
		t.Fatalf("unexpected drop-in path in %q", got)
	}
	if strings.Contains(got, "/etc/sudoers.d/nova.cli") {
		t.Errorf("drop-in filename must not contain a dot")
	}
}
