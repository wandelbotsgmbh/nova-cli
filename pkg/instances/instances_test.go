package instances

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/viper"
)

func newViper(t *testing.T, content string) (*viper.Viper, string) {
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

func reload(t *testing.T, path string) *viper.Viper {
	t.Helper()
	v := viper.New()
	v.SetConfigFile(path)
	if err := v.ReadInConfig(); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestLoadMigratesLegacyHost(t *testing.T) {
	tests := []struct {
		name   string
		config string
		want   Kind
	}{
		{"cloud host", "host: abc.instance.wandelbots.io\nvirtualid: abc\n", KindCloud},
		{"cluster id", "host: 10.0.0.5\nvirtualid: xyz\n", KindCluster},
		{"plain host", "host: 192.168.1.10\ncellname: line1\nnucleus-token: secret\n", KindHost},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, _ := newViper(t, tt.config)
			r, err := Load(v)
			if err != nil {
				t.Fatal(err)
			}
			if !r.Migrated || r.Current != DefaultName || len(r.Instances) != 1 {
				t.Fatalf("unexpected registry: %+v", r)
			}
			if got := r.Instances[0].Kind; got != tt.want {
				t.Errorf("kind = %q, want %q", got, tt.want)
			}
			if r.Instances[0].Host != v.GetString("host") {
				t.Errorf("host not migrated")
			}
		})
	}
}

func TestLoadMigrationKeepsCellAndNucleus(t *testing.T) {
	v, _ := newViper(t, "host: 192.168.1.10\ncellname: line1\nnucleus-host: nuc:3009\nnucleus-token: secret\n")
	r, _ := Load(v)
	got := r.Instances[0]
	if got.CellName != "line1" || got.NucleusHost != "nuc:3009" || got.NucleusToken != "secret" {
		t.Errorf("unexpected entry: %+v", got)
	}
}

func TestLoadEmptyConfig(t *testing.T) {
	v, _ := newViper(t, "editor: vim\n")
	r, err := Load(v)
	if err != nil {
		t.Fatal(err)
	}
	if r.Migrated || len(r.Instances) != 0 || r.Current != "" {
		t.Errorf("expected empty registry, got %+v", r)
	}
}

func TestSaveRoundTripAndLegacyMirror(t *testing.T) {
	v, path := newViper(t, "host: 192.168.1.10\neditor: vim\n")
	r, _ := Load(v)
	if err := r.Add(Instance{Name: "Lab-IPC", Kind: KindHost, Host: "10.1.2.3", CellName: "c1"}); err != nil {
		t.Fatal(err)
	}
	if err := r.Select("Lab-IPC"); err != nil {
		t.Fatal(err)
	}
	if err := r.Save(v); err != nil {
		t.Fatal(err)
	}

	v2 := reload(t, path)
	r2, err := Load(v2)
	if err != nil {
		t.Fatal(err)
	}
	if r2.Migrated {
		t.Error("saved registry must not be migrated again")
	}
	if len(r2.Instances) != 2 || r2.Current != "Lab-IPC" {
		t.Fatalf("unexpected registry: %+v", r2)
	}
	// names keep their case although viper lowercases map keys
	if _, ok := r2.Get("Lab-IPC"); !ok {
		t.Error("entry name lost its case")
	}
	if v2.GetString("host") != "10.1.2.3" || v2.GetString("cellname") != "c1" {
		t.Errorf("legacy keys not mirrored: host=%q cell=%q", v2.GetString("host"), v2.GetString("cellname"))
	}
	if v2.GetString("editor") != "vim" {
		t.Error("unrelated keys must be kept")
	}
}

func TestRemoveSurvivesSave(t *testing.T) {
	v, path := newViper(t, `current-instance: a
instances:
  - name: a
    kind: host
    host: h-a
  - name: b
    kind: host
    host: h-b
`)
	r, _ := Load(v)
	if err := r.Remove("a"); err != nil {
		t.Fatal(err)
	}
	if r.Current != "" {
		t.Error("removing the selected entry must clear the selection")
	}
	if err := r.Save(v); err != nil {
		t.Fatal(err)
	}
	r2, _ := Load(reload(t, path))
	if len(r2.Instances) != 1 || r2.Instances[0].Name != "b" {
		t.Errorf("unexpected entries after remove: %+v", r2.Instances)
	}

	// removing the last entry must not resurrect the legacy host as "default"
	_ = r2.Remove("b")
	v3 := reload(t, path)
	if err := r2.Save(v3); err != nil {
		t.Fatal(err)
	}
	r3, _ := Load(reload(t, path))
	if len(r3.Instances) != 0 || r3.Migrated {
		t.Errorf("expected no entries, got %+v", r3)
	}
}

func TestResolve(t *testing.T) {
	r := &Registry{Current: "a", Instances: []Instance{
		{Name: "a", Kind: KindHost, Host: "h-a"},
		{Name: "b", Kind: KindCloud, Host: "h-b"},
		{Name: "c", Kind: KindCluster, Host: "h-c"},
	}}
	tests := []struct {
		name, flag, env string
		wantName        string
		wantSrc         Source
		wantErr         string
	}{
		{"config", "", "", "a", SourceConfig, ""},
		{"env wins over config", "", "b", "b", SourceEnv, ""},
		{"flag wins over env", "c", "b", "c", SourceFlag, ""},
		{"unknown flag never falls back", "nope", "b", "", SourceFlag, "--instance"},
		{"unknown env never falls back", "", "nope", "", SourceEnv, EnvVar},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, src, err := r.Resolve(tt.flag, tt.env)
			if tt.wantErr != "" {
				if !errors.Is(err, ErrNotFound) || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want ErrNotFound mentioning %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.Name != tt.wantName || src != tt.wantSrc {
				t.Errorf("got %q/%q, want %q/%q", got.Name, src, tt.wantName, tt.wantSrc)
			}
		})
	}

	empty := &Registry{}
	if _, src, err := empty.Resolve("", ""); err != nil || src != SourceNone {
		t.Errorf("empty registry: src=%q err=%v", src, err)
	}
	stale := &Registry{Current: "gone"}
	if _, _, err := stale.Resolve("", ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("stale selection: err = %v", err)
	}
}

func TestAddValidation(t *testing.T) {
	r := &Registry{}
	if err := r.Add(Instance{Name: "x", Kind: KindHost}); err != nil {
		t.Fatal(err)
	}
	if err := r.Add(Instance{Name: "x", Kind: KindHost}); !errors.Is(err, ErrExists) {
		t.Errorf("duplicate: err = %v", err)
	}
	for _, bad := range []string{"", "-x", "a b", "a/b", "a:b"} {
		if err := r.Add(Instance{Name: bad, Kind: KindHost}); err == nil {
			t.Errorf("name %q should be rejected", bad)
		}
	}
	if err := r.Add(Instance{Name: "y", Kind: "physical"}); err == nil {
		t.Error("unknown kind should be rejected")
	}
}

func TestRedacted(t *testing.T) {
	i := Instance{Name: "a", NucleusToken: "secret"}
	if got := i.Redacted().NucleusToken; got != Redact {
		t.Errorf("token = %q", got)
	}
	if i.NucleusToken != "secret" {
		t.Error("Redacted must not modify the original")
	}
	if (Instance{}).Redacted().NucleusToken != "" {
		t.Error("empty token must stay empty")
	}
}
