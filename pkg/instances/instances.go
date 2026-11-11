// Package instances keeps the local registry of named NOVA instances in the CLI config
// and resolves which one a command targets (see docs/decisions/0016-named-instances.md).
package instances

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/spf13/viper"
	"github.com/wandelbotsgmbh/nova-cli/pkg/configfile"
)

// Kind says where an instance runs. It never says whether its robots are real.
type Kind string

const (
	// KindCloud is an instance on portal.wandelbots.io.
	KindCloud Kind = "cloud"
	// KindHost is any reachable NOVA host, e.g. an on-prem IPC.
	KindHost Kind = "host"
	// KindK8sRun is a Wandelbots-internal Kubernetes cluster with NOVA (k8srun), with a kubeconfig.
	KindK8sRun Kind = "k8srun"
)

// Kinds lists all kinds in display order.
var Kinds = []Kind{KindCloud, KindHost, KindK8sRun}

const (
	// EnvVar overrides the target instance for one invocation.
	EnvVar = "NOVA_INSTANCE"
	// DefaultName is the entry created when migrating a config with a bare host.
	DefaultName = "default"
	// DefaultCell is used when an entry has no cell configured.
	DefaultCell = "cell"

	keyInstances = "instances"
	keyCurrent   = "current-instance"

	// legacy single-instance keys, mirrored for older CLI versions
	legacyHost      = "host"
	legacyCell      = "cellname"
	legacyID        = "virtualid"
	legacyNucHost   = "nucleus-host"
	legacyNucToken  = "nucleus-token"
	legacyLoginEnv  = "token.authenvironment"
	cloudHostSuffix = "wandelbots.io"
)

// Instance is one registry entry.
type Instance struct {
	Name         string `mapstructure:"name" yaml:"name"`
	Kind         Kind   `mapstructure:"kind" yaml:"kind"`
	Host         string `mapstructure:"host" yaml:"host,omitempty"`
	CellName     string `mapstructure:"cellname" yaml:"cellname,omitempty"`
	ID           string `mapstructure:"id" yaml:"id,omitempty"`
	NucleusHost  string `mapstructure:"nucleus-host" yaml:"nucleus-host,omitempty"`
	NucleusToken string `mapstructure:"nucleus-token" yaml:"nucleus-token,omitempty"`
	Kubeconfig   string `mapstructure:"kubeconfig" yaml:"kubeconfig,omitempty"`
	SSHUser      string `mapstructure:"ssh-user" yaml:"ssh-user,omitempty"`
	// Env is the portal environment (prod, stg, dev) of a cloud instance.
	Env string `mapstructure:"env" yaml:"env,omitempty"`
}

// Cell returns the configured cell or DefaultCell.
func (i Instance) Cell() string {
	if i.CellName == "" {
		return DefaultCell
	}
	return i.CellName
}

// Redacted returns a copy with secrets replaced, safe to print.
func (i Instance) Redacted() Instance {
	if i.NucleusToken != "" {
		i.NucleusToken = Redact
	}
	return i
}

// Redact is printed in place of a secret value.
const Redact = "<redacted>"

// Source says where the target instance was chosen.
type Source string

const (
	SourceFlag   Source = "flag"
	SourceEnv    Source = "env"
	SourceConfig Source = "config"
	SourceNone   Source = "none"
)

var (
	ErrNotFound = errors.New("instance not found")
	ErrExists   = errors.New("instance already exists")
	ErrNoTarget = errors.New("no instance selected")

	nameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
)

// ValidateName checks that name is usable on the command line and in env vars.
func ValidateName(name string) error {
	if !nameRe.MatchString(name) || len(name) > 128 {
		return fmt.Errorf("invalid instance name %q: use letters, digits, '.', '_' or '-', starting with a letter or digit", name)
	}
	return nil
}

// ValidateKind checks that k is a known kind.
func ValidateKind(k Kind) error {
	for _, known := range Kinds {
		if k == known {
			return nil
		}
	}
	return fmt.Errorf("invalid instance kind %q", k)
}

// GuessKind derives a kind for a host without further information.
func GuessKind(host, id string) Kind {
	switch {
	case strings.Contains(host, cloudHostSuffix):
		return KindCloud
	case id != "":
		return KindK8sRun
	default:
		return KindHost
	}
}

// Registry is the list of instances plus the selected one.
type Registry struct {
	Current   string
	Instances []Instance
	// Migrated is true when the entries were derived from a legacy single-host config
	// and haven't been saved yet.
	Migrated bool

	// what Load read, to tell this process's changes from those of others in Save
	base        []Instance
	baseCurrent string
}

// Load reads the registry from v. A config without an instances list but with a
// top-level host is migrated in memory into an entry named DefaultName; it is
// persisted on the next Save.
func Load(v *viper.Viper) (*Registry, error) {
	r, err := load(v)
	if err != nil {
		return nil, err
	}
	r.base, r.baseCurrent = slices.Clone(r.Instances), r.Current
	return r, nil
}

func load(v *viper.Viper) (*Registry, error) {
	r := &Registry{Current: v.GetString(keyCurrent)}
	if v.IsSet(keyInstances) {
		if err := v.UnmarshalKey(keyInstances, &r.Instances); err != nil {
			return nil, fmt.Errorf("can't read %q from config: %w", keyInstances, err)
		}
		return r, nil
	}

	host := v.GetString(legacyHost)
	if host == "" {
		return r, nil
	}
	id := v.GetString(legacyID)
	kind := GuessKind(host, id)
	env := ""
	if kind == KindCloud {
		// the environment of the legacy login, which older CLI versions used for it
		env = v.GetString(legacyLoginEnv)
		if env == "" {
			env = "prod"
		}
	}
	r.Instances = []Instance{{
		Name:         DefaultName,
		Kind:         kind,
		Env:          env,
		Host:         host,
		CellName:     v.GetString(legacyCell),
		ID:           id,
		NucleusHost:  v.GetString(legacyNucHost),
		NucleusToken: v.GetString(legacyNucToken),
	}}
	r.Current = DefaultName
	r.Migrated = true
	return r, nil
}

// Save persists the registry in v's config file. Other processes may have changed the
// file since Load: only what this registry added, changed or removed is applied to the
// file's current list, and the selection is only written if it was changed here
// (configfile.Merge3). The registry holds the merged result afterwards.
//
// The legacy single-instance keys are set from the selected entry, so older CLI
// versions keep working against the same file; nothing else is removed.
func (r *Registry) Save(v *viper.Viper) error {
	err := configfile.Update(v, func(disk *viper.Viper) error {
		onDisk, err := load(disk)
		if err != nil {
			return err
		}
		r.Instances = configfile.Merge3(r.base, r.Instances, onDisk.Instances,
			func(i Instance) string { return i.Name }, func(a, b Instance) bool { return a == b })
		if r.Current == r.baseCurrent {
			r.Current = onDisk.Current
		}
		if _, ok := r.Get(r.Current); !ok {
			r.Current = "" // selected elsewhere, removed here (or the other way round)
		}

		list := make([]map[string]any, 0, len(r.Instances))
		for _, i := range r.Instances {
			list = append(list, toMap(i))
		}
		disk.Set(keyInstances, list)
		disk.Set(keyCurrent, r.Current)
		if cur, ok := r.Get(r.Current); ok {
			disk.Set(legacyHost, cur.Host)
			disk.Set(legacyCell, cur.Cell()) // older versions have no default for an empty cell
			disk.Set(legacyID, cur.ID)
		} else {
			disk.Set(legacyHost, "")
			disk.Set(legacyID, "")
		}
		return nil
	})
	if err != nil {
		return err
	}
	r.Migrated = false
	r.base, r.baseCurrent = slices.Clone(r.Instances), r.Current
	return nil
}

func toMap(i Instance) map[string]any {
	m := map[string]any{"name": i.Name, "kind": string(i.Kind)}
	for k, val := range map[string]string{
		"host":          i.Host,
		"cellname":      i.CellName,
		"id":            i.ID,
		"nucleus-host":  i.NucleusHost,
		"nucleus-token": i.NucleusToken,
		"kubeconfig":    i.Kubeconfig,
		"ssh-user":      i.SSHUser,
		"env":           i.Env,
	} {
		if val != "" {
			m[k] = val
		}
	}
	return m
}

// Get returns the entry with the given name.
func (r *Registry) Get(name string) (Instance, bool) {
	for _, i := range r.Instances {
		if i.Name == name {
			return i, true
		}
	}
	return Instance{}, false
}

// Add appends a new entry; it fails if the name is taken.
func (r *Registry) Add(i Instance) error {
	if err := ValidateName(i.Name); err != nil {
		return err
	}
	if err := ValidateKind(i.Kind); err != nil {
		return err
	}
	if _, ok := r.Get(i.Name); ok {
		return fmt.Errorf("%w: %q", ErrExists, i.Name)
	}
	r.Instances = append(r.Instances, i)
	return nil
}

// Put adds or replaces the entry with i's name.
func (r *Registry) Put(i Instance) error {
	if err := ValidateName(i.Name); err != nil {
		return err
	}
	if err := ValidateKind(i.Kind); err != nil {
		return err
	}
	for idx := range r.Instances {
		if r.Instances[idx].Name == i.Name {
			r.Instances[idx] = i
			return nil
		}
	}
	r.Instances = append(r.Instances, i)
	return nil
}

// Remove deletes the entry; if it was selected, nothing is selected afterwards.
func (r *Registry) Remove(name string) error {
	for idx, i := range r.Instances {
		if i.Name == name {
			r.Instances = append(r.Instances[:idx], r.Instances[idx+1:]...)
			if r.Current == name {
				r.Current = ""
			}
			return nil
		}
	}
	return notFound(name)
}

// Select makes name the default target.
func (r *Registry) Select(name string) error {
	if _, ok := r.Get(name); !ok {
		return notFound(name)
	}
	r.Current = name
	return nil
}

// FreeName returns base if no entry has that name, otherwise the first free name of
// base-2, base-3, …
func (r *Registry) FreeName(base string) string {
	name := base
	for n := 2; ; n++ {
		if _, taken := r.Get(name); !taken {
			return name
		}
		name = fmt.Sprintf("%s-%d", base, n)
	}
}

// FindByID returns the entry of the given kind with the given remote ID.
func (r *Registry) FindByID(kind Kind, id string) (Instance, bool) {
	for _, i := range r.Instances {
		if i.Kind == kind && i.ID == id && id != "" {
			return i, true
		}
	}
	return Instance{}, false
}

func notFound(name string) error {
	return fmt.Errorf("%w: %q (see 'nova instance list')", ErrNotFound, name)
}

// Resolve picks the target: flag, then env, then the selected entry. An unknown
// name from flag or env is an error and never falls back to another entry.
func (r *Registry) Resolve(flag, env string) (Instance, Source, error) {
	name, src := flag, SourceFlag
	if name == "" {
		name, src = env, SourceEnv
	}
	if name == "" {
		name, src = r.Current, SourceConfig
	}
	if name == "" {
		return Instance{}, SourceNone, nil
	}
	i, ok := r.Get(name)
	if !ok {
		switch src {
		case SourceFlag:
			return Instance{}, src, fmt.Errorf("--instance: %w", notFound(name))
		case SourceEnv:
			return Instance{}, src, fmt.Errorf("%s: %w", EnvVar, notFound(name))
		default:
			return Instance{}, src, fmt.Errorf("selected instance: %w", notFound(name))
		}
	}
	return i, src, nil
}
