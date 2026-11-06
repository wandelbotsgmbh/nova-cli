package cmd

import (
	"errors"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/wandelbotsgmbh/nova-cli/pkg/nucleusservers"
)

func withStdin(t *testing.T, input string) {
	t.Helper()
	rootCmd.SetIn(strings.NewReader(input))
	t.Cleanup(func() { rootCmd.SetIn(nil) })
}

func loadServers(t *testing.T) *nucleusservers.Registry {
	t.Helper()
	r, err := nucleusservers.Load(viper.GetViper())
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestNucleusServerCommands(t *testing.T) {
	path := withConfig(t, "")
	withStdin(t, "devtok\n")
	mustRun(t, "nucleus", "server", "add", "dev", "--host", "nucleus.dev.wandelbots.io", "--token-stdin")
	mustRun(t, "nucleus", "server", "add", "wb", "--host", "nucleus.wb.wandelbots.io")

	r := loadServers(t)
	if r.Current != "dev" {
		t.Errorf("the first server must be selected, current %q", r.Current)
	}
	if dev, _ := r.Get("dev"); dev.Token != "devtok" || !dev.UseTLS() {
		t.Errorf("dev = %+v", dev)
	}

	out := mustRun(t, "nucleus", "server", "list")
	var rows []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n")[1:] {
		rows = append(rows, strings.Join(strings.Fields(line), " "))
	}
	if want := "* dev nucleus.dev.wandelbots.io true set|wb nucleus.wb.wandelbots.io true -"; strings.Join(rows, "|") != want {
		t.Errorf("list rows = %q, want %q", rows, want)
	}
	if strings.Contains(out, "devtok") || strings.Contains(readConfig(t, path), "nucleus-host") {
		t.Errorf("token printed, or old keys written:\n%s", out)
	}

	mustRun(t, "nucleus", "server", "select", "wb")
	if r := loadServers(t); r.Current != "wb" {
		t.Errorf("select: current %q", r.Current)
	}
	_, errOut, err := runWithConfig(t, "nucleus", "server", "remove", "wb")
	if err != nil || !strings.Contains(errOut, "removed the selected Nucleus server") {
		t.Errorf("remove: %v %q", err, errOut)
	}
	if r := loadServers(t); r.Current != "" || len(r.Servers) != 1 {
		t.Errorf("after remove: %+v", r)
	}

	if _, _, err := runWithConfig(t, "nucleus", "server", "add", "dev", "--host", "x"); !errors.Is(err, nucleusservers.ErrExists) {
		t.Errorf("duplicate add: %v", err)
	}
	if _, _, err := runWithConfig(t, "nucleus", "server", "select", "nope"); !errors.Is(err, nucleusservers.ErrNotFound) {
		t.Errorf("select unknown: %v", err)
	}
}

// 'nova nucleus' doesn't resolve an instance: a broken --instance doesn't matter.
func TestNucleusDoesNotNeedAnInstance(t *testing.T) {
	withConfig(t, "")
	if _, errOut, err := runWithConfig(t, "--instance", "nosuch", "nucleus", "server", "list"); err != nil {
		t.Errorf("nucleus with an unknown --instance: %v %s", err, errOut)
	}
}

func TestConfigSetNucleusKeys(t *testing.T) {
	withConfig(t, "")
	mustRun(t, "instance", "add", "lab", "--host", "10.0.0.1")

	if _, _, err := runWithConfig(t, "config", "set", "nucleus-token", "tok"); !errors.Is(err, nucleusservers.ErrNoServer) {
		t.Fatalf("token without a server: %v", err)
	}
	mustRun(t, "config", "set", "nucleus-host", "nucleus.dev.wandelbots.io")
	mustRun(t, "config", "set", "nucleus-token", "tok", "--nucleus", "dev")
	// the pre-ADR-0018 form must not silently write the selected server
	if _, _, err := runWithConfig(t, "--instance", "lab", "config", "set", "nucleus-token", "other"); err == nil || !strings.Contains(err.Error(), "--nucleus") {
		t.Errorf("--instance with a Nucleus key: %v", err)
	}

	r := loadServers(t)
	if dev, ok := r.Get("dev"); !ok || r.Current != "dev" || dev.Token != "tok" {
		t.Fatalf("unexpected registry: %+v", r)
	}
	out := mustRun(t, "config", "view")
	for _, want := range []string{"nucleus: dev (from config)\n", "nucleus-host: nucleus.dev.wandelbots.io\n", "nucleus-token: <redacted>\n", "instance: lab"} {
		if !strings.Contains(out, want) {
			t.Errorf("config view lacks %q:\n%s", want, out)
		}
	}

	// a new host drops the token, it belongs to the old one
	_, errOut, err := runWithConfig(t, "config", "set", "nucleus-host", "nucleus.wb.wandelbots.io")
	if err != nil || !strings.Contains(errOut, "was removed") {
		t.Fatalf("change host: %v %q", err, errOut)
	}
	if dev, _ := loadServers(t).Get("dev"); dev.Token != "" || dev.Host != "nucleus.wb.wandelbots.io" {
		t.Errorf("after host change: %+v", dev)
	}
}

// nucleusConfig: the resolved server, overrides by flags and env, and a token only
// ever goes to its own host.
func TestNucleusConfigResolution(t *testing.T) {
	withConfig(t, `nucleus-servers:
  - name: dev
    host: nucleus.dev.wandelbots.io
    token: devtok
  - name: wb
    host: nucleus.wb.wandelbots.io
    token: wbtok
  - name: lan
    host: 10.0.0.5:3009
    tls: false
  - name: notoken
    host: nucleus.example.com
current-nucleus: wb
`)
	mustRun(t, "nucleus", "server", "list") // loads the config

	for _, tc := range []struct {
		name, server, env, nucleusToken string
		flags                           []string
		host, token                     string
		tls                             bool
		err                             string
	}{
		{name: "selected", host: "nucleus.wb.wandelbots.io", token: "wbtok", tls: true},
		{name: "--nucleus", server: "dev", host: "nucleus.dev.wandelbots.io", token: "devtok", tls: true},
		{name: "NOVA_NUCLEUS", env: "dev", host: "nucleus.dev.wandelbots.io", token: "devtok", tls: true},
		{name: "server without TLS", server: "lan", host: "10.0.0.5:3009", token: "", err: "has no API token"},
		{name: "NUCLEUS_TOKEN", server: "lan", nucleusToken: "envtok", host: "10.0.0.5:3009", token: "envtok", tls: false},
		{name: "--nucleus-tls", flags: []string{"--nucleus-tls=false"}, host: "nucleus.wb.wandelbots.io", token: "wbtok", tls: false},
		{name: "same host keeps the token", flags: []string{"--nucleus-host", "https://nucleus.wb.wandelbots.io/"}, host: "nucleus.wb.wandelbots.io", token: "wbtok", tls: true},
		{name: "other host gets no token", flags: []string{"--nucleus-host", "evil.example.com"}, err: "only sent to its own host"},
		{name: "other host with its own token", flags: []string{"--nucleus-host", "other.example.com", "--nucleus-token", "t2"}, host: "other.example.com", token: "t2", tls: true},
		{name: "unknown server", server: "nope", err: "--nucleus: nucleus server not found"},
		{name: "server without token", server: "notoken", err: `nucleus server "notoken" has no API token`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			nucleusFlag = tc.server
			t.Cleanup(func() { nucleusFlag = "" })
			t.Setenv(nucleusservers.EnvVar, tc.env)
			t.Setenv("NUCLEUS_TOKEN", tc.nucleusToken)

			c := &cobra.Command{Use: "x", RunE: func(*cobra.Command, []string) error { return nil }}
			c.Flags().String("nucleus-host", "", "")
			c.Flags().String("nucleus-token", "", "")
			c.Flags().Bool("nucleus-tls", true, "")
			if err := c.ParseFlags(tc.flags); err != nil {
				t.Fatal(err)
			}

			cfg, err := nucleusConfig(c)
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("err = %v, want %q", err, tc.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Host != tc.host || cfg.APIToken != tc.token || cfg.TLS != tc.tls {
				t.Errorf("got host=%q token=%q tls=%v, want %q %q %v", cfg.Host, cfg.APIToken, cfg.TLS, tc.host, tc.token, tc.tls)
			}
		})
	}
}

func TestNucleusConfigWithoutServer(t *testing.T) {
	withConfig(t, "")
	mustRun(t, "nucleus", "server", "list")
	c := &cobra.Command{Use: "x"}
	c.Flags().String("nucleus-host", "", "")
	c.Flags().String("nucleus-token", "", "")
	c.Flags().Bool("nucleus-tls", true, "")
	if _, err := nucleusConfig(c); !errors.Is(err, nucleusservers.ErrNoServer) {
		t.Errorf("err = %v, want ErrNoServer", err)
	}
}
