package cmd

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	homedir "github.com/mitchellh/go-homedir"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"github.com/wandelbotsgmbh/nova-cli/pkg/bootstrap"
	"github.com/wandelbotsgmbh/nova-cli/pkg/instances"
	"github.com/wandelbotsgmbh/nova-cli/pkg/oauth"
)

// runNova executes the real command tree in-process. A non-nil error is what
// makes Execute() exit 1; nil means exit 0.
func runNova(t *testing.T, args ...string) (string, error) {
	t.Helper()

	// initConfig creates ~/.nova/config.yaml; keep it out of the real home
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	homedir.DisableCache = true
	t.Cleanup(func() {
		homedir.DisableCache = false
		bootstrap.SetDryRun(false)
		rootCmd.SetArgs(nil)
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
	})

	t.Setenv(instances.EnvVar, "")
	t.Setenv(envVarName, "")
	resetState(rootCmd)

	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs(args)
	err := rootCmd.Execute()
	return out.String(), err
}

// resetState prepares the package-global command tree and config for another
// in-process run, like a fresh process would start.
func resetState(root *cobra.Command) {
	viper.Reset()
	resetTarget()
	envSource = "default"
	oauth.SetActiveEnvironment("")
	rejectUnknownSubcommands(root)
	resetFlags(root)
}

// resetFlags undoes flag values and "changed" state left over from earlier
// runs, since the command tree is package-global.
func resetFlags(c *cobra.Command) {
	reset := func(f *pflag.Flag) {
		if s, ok := f.Value.(pflag.SliceValue); ok {
			_ = s.Replace(nil)
		} else {
			_ = f.Value.Set(f.DefValue)
		}
		f.Changed = false
	}
	c.Flags().VisitAll(reset)
	c.PersistentFlags().VisitAll(reset)
	for _, sub := range c.Commands() {
		resetFlags(sub)
	}
}

func isGroup(c *cobra.Command) bool {
	return c.Args != nil && reflect.ValueOf(c.Args).Pointer() == reflect.ValueOf(groupArgs).Pointer()
}

// commandsWithSubcommands returns all commands below c that have subcommands.
func commandsWithSubcommands(c *cobra.Command) []*cobra.Command {
	var cmds []*cobra.Command
	for _, sub := range c.Commands() {
		if sub.HasSubCommands() {
			cmds = append(cmds, sub)
		}
		cmds = append(cmds, commandsWithSubcommands(sub)...)
	}
	return cmds
}

// Commands like "nova login" run something themselves and also have
// subcommands; they must keep their own argument handling.
func TestRunnableCommandsWithSubcommandsAreNotGroups(t *testing.T) {
	rejectUnknownSubcommands(rootCmd)
	for _, c := range []*cobra.Command{loginCmd, versionCmd} {
		if isGroup(c) {
			t.Errorf("%q was turned into a command group", c.CommandPath())
		}
	}
}

func TestGroupsRejectUnknownSubcommands(t *testing.T) {
	rejectUnknownSubcommands(rootCmd)
	var groups []*cobra.Command
	for _, c := range commandsWithSubcommands(rootCmd) {
		// never execute a command that would run its own RunE (e.g. nova login)
		if isGroup(c) {
			groups = append(groups, c)
		}
	}
	if len(groups) < 10 {
		t.Fatalf("expected at least 10 command groups, found %d", len(groups))
	}

	for _, g := range groups {
		path := g.CommandPath()
		t.Run(path, func(t *testing.T) {
			args := append(strings.Fields(path)[1:], "nosuchcommand")
			out, err := runNova(t, args...)
			if err == nil {
				t.Fatalf("expected an error (exit 1), got none; output:\n%s", out)
			}
			want := `unknown command "nosuchcommand" for "` + path + `"`
			if err.Error() != want {
				t.Errorf("error = %q, want %q", err.Error(), want)
			}
		})
	}
}

func TestGroupTypoSuggestsSubcommand(t *testing.T) {
	out, err := runNova(t, "app", "delte", "myapp")
	if err == nil {
		t.Fatalf("expected an error (exit 1), got none; output:\n%s", out)
	}
	want := "unknown command \"delte\" for \"nova app\"\n\nDid you mean this?\n\tdelete\n"
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err.Error(), want)
	}
	if !strings.Contains(out, "Error: "+want) {
		t.Errorf("output does not contain the error:\n%s", out)
	}
}

func TestGroupWithoutArgsShowsHelp(t *testing.T) {
	out, err := runNova(t, "app")
	if err != nil {
		t.Fatalf("expected no error (exit 0), got %v", err)
	}
	if !strings.Contains(out, "Available Commands:") || !strings.Contains(out, "install") {
		t.Errorf("expected the help of nova app, got:\n%s", out)
	}
}

func TestFlagConstraints(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{
			name:    "model build requires --tag",
			args:    []string{"model", "build", "."},
			wantErr: `required flag(s) "tag" not set`,
		},
		{
			name:    "bootstrap ca-certificates --target-apps and --all-apps",
			args:    []string{"bootstrap", "ca-certificates", "--dry-run", "--all-apps", "--target-apps", "x", "ca.pem"},
			wantErr: "if any flags in the group [target-apps all-apps] are set none of the others can be; [all-apps target-apps] were all set",
		},
		{
			name:    "bootstrap all --ca-target-apps and --ca-all-apps",
			args:    []string{"bootstrap", "all", "--dry-run", "--ca-certificates", "ca.pem", "--ca-all-apps", "--ca-target-apps", "x"},
			wantErr: "if any flags in the group [ca-target-apps ca-all-apps] are set none of the others can be; [ca-all-apps ca-target-apps] were all set",
		},
		{
			name:    "app install --copy-image and --registry-secret",
			args:    []string{"app", "install", "--copy-image", "--registry-secret", "x"},
			wantErr: "if any flags in the group [copy-image registry-secret] are set none of the others can be; [copy-image registry-secret] were all set",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := runNova(t, tt.args...)
			if err == nil {
				t.Fatalf("expected an error (exit 1), got none; output:\n%s", out)
			}
			if err.Error() != tt.wantErr {
				t.Errorf("error = %q, want %q", err.Error(), tt.wantErr)
			}
		})
	}
}
