// Package nucleusservers keeps the local registry of named Nucleus servers in the CLI
// config and resolves which one a command uses (docs/decisions/0018-nucleus-servers.md).
// A Nucleus server is its own target, independent of the NOVA instance.
package nucleusservers

import (
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/spf13/viper"
	"github.com/wandelbotsgmbh/nova-cli/pkg/configfile"
	"github.com/wandelbotsgmbh/nova-cli/pkg/instances"
)

const (
	// EnvVar overrides the Nucleus server for one invocation.
	EnvVar = "NOVA_NUCLEUS"

	keyServers = "nucleus-servers"
	keyCurrent = "current-nucleus"

	// keys of ADR-0016 (per instance entry) and older versions (top level)
	keyInstances       = "instances"
	keyCurrentInstance = "current-instance"
	legacyHost         = "nucleus-host"
	legacyToken        = "nucleus-token"
)

var (
	ErrNotFound = errors.New("nucleus server not found")
	ErrExists   = errors.New("nucleus server already exists")
	ErrNoServer = errors.New("no Nucleus server selected")
)

// Server is one registry entry.
type Server struct {
	Name  string `mapstructure:"name" yaml:"name"`
	Host  string `mapstructure:"host" yaml:"host"`
	Token string `mapstructure:"token" yaml:"token,omitempty"`
	// TLS is nil for the default (true).
	TLS *bool `mapstructure:"tls" yaml:"tls,omitempty"`
}

// UseTLS reports whether connections use TLS (wss://), true unless disabled.
func (s Server) UseTLS() bool { return s.TLS == nil || *s.TLS }

// Redacted returns a copy with the token replaced, safe to print.
func (s Server) Redacted() Server {
	if s.Token != "" {
		s.Token = instances.Redact
	}
	return s
}

// Registry is the list of servers plus the selected one.
type Registry struct {
	Current string
	Servers []Server
	// Migrated is true when the entries were derived from the Nucleus keys of
	// instance entries or the top level and haven't been saved yet.
	Migrated bool

	// what Load read, to tell this process's changes from those of others in Save
	base        []Server
	baseCurrent string
}

func sameServer(a, b Server) bool {
	return a.Name == b.Name && a.Host == b.Host && a.Token == b.Token && a.UseTLS() == b.UseTLS() && (a.TLS == nil) == (b.TLS == nil)
}

// Load reads the registry from v. Without a nucleus-servers list, the Nucleus keys of
// the instance entries (ADR-0016) and the top-level keys of older versions are migrated
// in memory, one entry per host; they are persisted on the next Save. The old keys are
// never changed, older CLI versions keep reading them.
func Load(v *viper.Viper) (*Registry, error) {
	r, err := load(v)
	if err != nil {
		return nil, err
	}
	r.base, r.baseCurrent = slices.Clone(r.Servers), r.Current
	return r, nil
}

func load(v *viper.Viper) (*Registry, error) {
	r := &Registry{Current: v.GetString(keyCurrent)}
	if v.IsSet(keyServers) {
		if err := v.UnmarshalKey(keyServers, &r.Servers); err != nil {
			return nil, fmt.Errorf("can't read %q from config: %w", keyServers, err)
		}
		return r, nil
	}

	type legacy struct {
		Name         string `mapstructure:"name"`
		NucleusHost  string `mapstructure:"nucleus-host"`
		NucleusToken string `mapstructure:"nucleus-token"`
	}
	var entries []legacy
	if v.IsSet(keyInstances) {
		if err := v.UnmarshalKey(keyInstances, &entries); err != nil {
			return nil, fmt.Errorf("can't read %q from config: %w", keyInstances, err)
		}
	}
	// the selected instance first: its token wins and its server becomes the selected one
	selected := v.GetString(keyCurrentInstance)
	ordered := make([]legacy, 0, len(entries)+1)
	for _, e := range entries {
		if e.Name == selected {
			ordered = append(ordered, e)
		}
	}
	for _, e := range entries {
		if e.Name != selected {
			ordered = append(ordered, e)
		}
	}
	ordered = append(ordered, legacy{NucleusHost: v.GetString(legacyHost), NucleusToken: v.GetString(legacyToken)})

	for _, e := range ordered {
		host := strings.TrimSpace(e.NucleusHost)
		if host == "" {
			continue
		}
		if s, ok := r.FindByHost(host); ok {
			if s.Token == "" && e.NucleusToken != "" {
				s.Token = e.NucleusToken
				_ = r.Put(s)
			}
			continue
		}
		s := Server{Name: r.FreeName(NameFromHost(host)), Host: host, Token: e.NucleusToken}
		r.Servers = append(r.Servers, s)
	}
	if len(r.Servers) > 0 {
		r.Current = r.Servers[0].Name
		r.Migrated = true
	}
	return r, nil
}

// Save persists the registry in v's config file. Other processes may have changed the
// file since Load: only what this registry added, changed or removed is applied to the
// file's current list (configfile.Merge3). The old Nucleus keys are left as they are.
func (r *Registry) Save(v *viper.Viper) error {
	err := configfile.Update(v, func(disk *viper.Viper) error {
		onDisk, err := load(disk)
		if err != nil {
			return err
		}
		if onDisk.Migrated && !r.Migrated {
			// the file has no list of its own yet: its entries are derived, not saved
			onDisk.Servers, onDisk.Current = nil, ""
		}
		r.Servers = configfile.Merge3(r.base, r.Servers, onDisk.Servers, func(s Server) string { return s.Name }, sameServer)
		if r.Current == r.baseCurrent && !onDisk.Migrated {
			r.Current = onDisk.Current
		}
		if _, ok := r.Get(r.Current); !ok {
			r.Current = ""
		}
		list := make([]map[string]any, 0, len(r.Servers))
		for _, s := range r.Servers {
			m := map[string]any{"name": s.Name, "host": s.Host}
			if s.Token != "" {
				m["token"] = s.Token
			}
			if s.TLS != nil {
				m["tls"] = *s.TLS
			}
			list = append(list, m)
		}
		disk.Set(keyServers, list)
		disk.Set(keyCurrent, r.Current)
		return nil
	})
	if err != nil {
		return err
	}
	r.Migrated = false
	r.base, r.baseCurrent = slices.Clone(r.Servers), r.Current
	return nil
}

// NameFromHost derives an entry name from a host: its first label that isn't
// "nucleus" (nucleus.dev.wandelbots.io → dev), else "nucleus".
func NameFromHost(host string) string {
	h := host
	if u, err := url.Parse(host); err == nil && u.Host != "" {
		h = u.Hostname()
	} else if i := strings.IndexAny(h, ":/"); i >= 0 {
		h = h[:i]
	}
	for label := range strings.SplitSeq(strings.ToLower(h), ".") {
		if label != "" && label != "nucleus" && instances.ValidateName(label) == nil {
			return label
		}
	}
	return "nucleus"
}

// SameHost reports whether a and b name the same server (scheme, case, a trailing
// slash and the default port don't matter).
func SameHost(a, b string) bool { return normalizeHost(a) == normalizeHost(b) }

func normalizeHost(h string) string {
	h = strings.ToLower(strings.TrimSpace(h))
	for _, p := range []string{"https://", "http://", "wss://", "ws://"} {
		h = strings.TrimPrefix(h, p)
	}
	h = strings.TrimRight(h, "/")
	h = strings.TrimSuffix(h, ":443")
	return h
}

// Get returns the entry with the given name.
func (r *Registry) Get(name string) (Server, bool) {
	for _, s := range r.Servers {
		if s.Name == name {
			return s, true
		}
	}
	return Server{}, false
}

// FindByHost returns the entry for host.
func (r *Registry) FindByHost(host string) (Server, bool) {
	for _, s := range r.Servers {
		if SameHost(s.Host, host) {
			return s, true
		}
	}
	return Server{}, false
}

// Add appends a new entry; it fails if the name is taken.
func (r *Registry) Add(s Server) error {
	if err := validate(s); err != nil {
		return err
	}
	if _, ok := r.Get(s.Name); ok {
		return fmt.Errorf("%w: %q", ErrExists, s.Name)
	}
	r.Servers = append(r.Servers, s)
	return nil
}

// Put adds or replaces the entry with s's name.
func (r *Registry) Put(s Server) error {
	if err := validate(s); err != nil {
		return err
	}
	for i := range r.Servers {
		if r.Servers[i].Name == s.Name {
			r.Servers[i] = s
			return nil
		}
	}
	r.Servers = append(r.Servers, s)
	return nil
}

func validate(s Server) error {
	if err := instances.ValidateName(s.Name); err != nil {
		return fmt.Errorf("invalid Nucleus server name %q: use letters, digits, '.', '_' or '-', starting with a letter or digit", s.Name)
	}
	if strings.TrimSpace(s.Host) == "" {
		return fmt.Errorf("nucleus server %q needs a host", s.Name)
	}
	return nil
}

// Remove deletes the entry; if it was selected, nothing is selected afterwards.
func (r *Registry) Remove(name string) error {
	for i, s := range r.Servers {
		if s.Name == name {
			r.Servers = append(r.Servers[:i], r.Servers[i+1:]...)
			if r.Current == name {
				r.Current = ""
			}
			return nil
		}
	}
	return notFound(name)
}

// Select makes name the default server.
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

func notFound(name string) error {
	return fmt.Errorf("%w: %q (see 'nova nucleus server list')", ErrNotFound, name)
}

// Resolve picks the server: flag, then env, then the selected entry. An unknown name
// from flag or env is an error and never falls back to another entry. Without any,
// it returns an empty Server and instances.SourceNone.
func (r *Registry) Resolve(flag, env string) (Server, instances.Source, error) {
	name, src := flag, instances.SourceFlag
	if name == "" {
		name, src = env, instances.SourceEnv
	}
	if name == "" {
		name, src = r.Current, instances.SourceConfig
	}
	if name == "" {
		return Server{}, instances.SourceNone, nil
	}
	s, ok := r.Get(name)
	if !ok {
		switch src {
		case instances.SourceFlag:
			return Server{}, src, fmt.Errorf("--nucleus: %w", notFound(name))
		case instances.SourceEnv:
			return Server{}, src, fmt.Errorf("%s: %w", EnvVar, notFound(name))
		default:
			return Server{}, src, fmt.Errorf("selected Nucleus server: %w", notFound(name))
		}
	}
	return s, src, nil
}
