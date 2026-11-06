package nucleusservers

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/viper"
	"github.com/wandelbotsgmbh/nova-cli/pkg/instances"
)

func loadYAML(t *testing.T, content string) (*viper.Viper, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	v := viper.New()
	v.SetConfigFile(path)
	if err := v.ReadInConfig(); err != nil {
		t.Fatal(err)
	}
	return v, path
}

// The Nucleus keys of instance entries (ADR-0016) and of older versions (top level)
// become one server per host; the selected instance's comes first.
func TestLoadMigratesLegacyKeys(t *testing.T) {
	v, path := loadYAML(t, `current-instance: lab
nucleus-host: nucleus.dev.wandelbots.io
nucleus-token: top-level
instances:
  - name: old
    kind: cloud
    host: old.instance.wandelbots.io
    nucleus-host: nucleus.dev.wandelbots.io
    nucleus-token: from-old
  - name: lab
    kind: host
    host: 10.0.0.1
    nucleus-host: nucleus.wb.wandelbots.io
    nucleus-token: from-lab
  - name: noauth
    kind: host
    host: 10.0.0.2
    nucleus-host: https://Nucleus.WB.wandelbots.io/
`)
	before, _ := os.ReadFile(path)
	r, err := Load(v)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Migrated || r.Current != "wb" {
		t.Errorf("Migrated=%v Current=%q, want true and the selected instance's server wb", r.Migrated, r.Current)
	}
	want := []Server{
		{Name: "wb", Host: "nucleus.wb.wandelbots.io", Token: "from-lab"},
		{Name: "dev", Host: "nucleus.dev.wandelbots.io", Token: "from-old"},
	}
	if len(r.Servers) != len(want) {
		t.Fatalf("servers = %+v, want %+v", r.Servers, want)
	}
	for i := range want {
		if r.Servers[i].Name != want[i].Name || r.Servers[i].Host != want[i].Host || r.Servers[i].Token != want[i].Token {
			t.Errorf("server %d = %+v, want %+v", i, r.Servers[i], want[i])
		}
	}
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Error("loading must not write the config")
	}
}

func TestLoadWithoutNucleusKeys(t *testing.T) {
	v, _ := loadYAML(t, "host: 10.0.0.1\n")
	r, err := Load(v)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Servers) != 0 || r.Current != "" || r.Migrated {
		t.Errorf("unexpected registry: %+v", r)
	}
}

// After the first save, the old keys are left alone and not migrated again, even if
// every server was removed; they stay for older CLI versions.
func TestSaveKeepsLegacyKeysAndStopsMigration(t *testing.T) {
	v, path := loadYAML(t, "nucleus-host: nucleus.dev.wandelbots.io\nnucleus-token: tok\n")
	r, _ := Load(v)
	off := false
	if err := r.Put(Server{Name: "lan", Host: "10.0.0.5:3009", TLS: &off}); err != nil {
		t.Fatal(err)
	}
	if err := r.Remove("dev"); err != nil {
		t.Fatal(err)
	}
	if err := r.Save(v); err != nil {
		t.Fatal(err)
	}
	content, _ := os.ReadFile(path)
	for _, want := range []string{"nucleus-host: nucleus.dev.wandelbots.io", "nucleus-token: tok", "tls: false"} {
		if !strings.Contains(string(content), want) {
			t.Errorf("config lacks %q:\n%s", want, content)
		}
	}

	v2, _ := loadYAML(t, string(content))
	r2, _ := Load(v2)
	if len(r2.Servers) != 1 || r2.Servers[0].Name != "lan" || r2.Servers[0].UseTLS() || r2.Migrated {
		t.Errorf("reloaded registry: %+v", r2)
	}

	if err := r2.Remove("lan"); err != nil {
		t.Fatal(err)
	}
	if err := r2.Save(v2); err != nil {
		t.Fatal(err)
	}
	content, _ = os.ReadFile(v2.ConfigFileUsed())
	v3, _ := loadYAML(t, string(content))
	if r3, _ := Load(v3); len(r3.Servers) != 0 {
		t.Errorf("an empty list must not be migrated again: %+v", r3)
	}
}

func TestResolve(t *testing.T) {
	r := &Registry{Current: "wb", Servers: []Server{{Name: "dev", Host: "d"}, {Name: "wb", Host: "w"}}}
	for _, tc := range []struct {
		flag, env, want string
		src             instances.Source
	}{
		{"", "", "wb", instances.SourceConfig},
		{"", "dev", "dev", instances.SourceEnv},
		{"dev", "wb", "dev", instances.SourceFlag},
	} {
		s, src, err := r.Resolve(tc.flag, tc.env)
		if err != nil || s.Name != tc.want || src != tc.src {
			t.Errorf("Resolve(%q, %q) = %q, %s, %v; want %q, %s", tc.flag, tc.env, s.Name, src, err, tc.want, tc.src)
		}
	}
	for _, tc := range []struct{ flag, env, msg string }{
		{"nope", "", "--nucleus: "},
		{"", "nope", EnvVar + ": "},
	} {
		_, _, err := r.Resolve(tc.flag, tc.env)
		if !errors.Is(err, ErrNotFound) || !strings.HasPrefix(err.Error(), tc.msg) {
			t.Errorf("Resolve(%q, %q) error = %v, want a not-found error starting with %q", tc.flag, tc.env, err, tc.msg)
		}
	}
	if s, src, err := (&Registry{}).Resolve("", ""); err != nil || s.Name != "" || src != instances.SourceNone {
		t.Errorf("empty registry: %+v, %s, %v", s, src, err)
	}
}

func TestNameFromHost(t *testing.T) {
	for host, want := range map[string]string{
		"nucleus.dev.wandelbots.io":    "dev",
		"nucleus.wb.wandelbots.io":     "wb",
		"https://nucleus.example.com/": "example",
		"omni.lab.local:3009":          "omni",
		"nucleus":                      "nucleus",
		"192.168.1.20":                 "192",
	} {
		if got := NameFromHost(host); got != want {
			t.Errorf("NameFromHost(%q) = %q, want %q", host, got, want)
		}
	}
}

func TestSameHost(t *testing.T) {
	if !SameHost("https://Nucleus.Dev.wandelbots.io/", "nucleus.dev.wandelbots.io") {
		t.Error("scheme, case and trailing slash must not matter")
	}
	if SameHost("nucleus.dev.wandelbots.io", "nucleus.wb.wandelbots.io") {
		t.Error("different hosts")
	}
}

func TestAddValidates(t *testing.T) {
	r := &Registry{}
	if err := r.Add(Server{Name: "dev", Host: "h"}); err != nil {
		t.Fatal(err)
	}
	if err := r.Add(Server{Name: "dev", Host: "h2"}); !errors.Is(err, ErrExists) {
		t.Errorf("duplicate name: %v", err)
	}
	if err := r.Add(Server{Name: "-bad", Host: "h"}); err == nil {
		t.Error("invalid name accepted")
	}
	if err := r.Add(Server{Name: "nohost"}); err == nil {
		t.Error("server without host accepted")
	}
}
