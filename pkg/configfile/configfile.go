// Package configfile changes the CLI config file (~/.nova/config.yaml) safely while
// other nova processes use it: shells, agents and long-running commands (nova mcp,
// keep-alive) share one file.
//
// viper alone loses updates: WriteConfig truncates the file and writes the whole
// in-memory copy of the process, which may be minutes old. So every write goes through
// Update: it locks the file, reads it as it is now, applies only the caller's change
// and replaces the file atomically.
package configfile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/viper"
)

var (
	// lockTimeout is how long Update waits for another process to finish its change. A
	// change is a read and a write of a small file; a token refresh adds one request.
	lockTimeout = 30 * time.Second
	// legacyWriteWait is how long Update waits for an empty file to get its content:
	// CLI versions before this package truncate the file and then write it, without
	// the lock.
	legacyWriteWait = time.Second
)

// ErrUnchanged is returned by an Update function that has nothing to write.
var ErrUnchanged = errors.New("config unchanged")

// Update changes the config file of v. fn gets the file's current content (not v's
// possibly older copy) and changes it with Set; the result replaces the file. v then
// re-reads the file, so the process sees the change and what others changed meanwhile.
// When fn fails nothing is written; ErrUnchanged is no failure.
//
// fn runs under the file lock: keep it short, and never start another Update in it.
//
// A v without a config file (tests) is changed in memory only.
//
// The lock file and the temporary file are created next to the config, so its
// directory must be writable.
func Update(v *viper.Viper, fn func(disk *viper.Viper) error) error {
	file := v.ConfigFileUsed()
	if file == "" {
		if err := fn(v); !errors.Is(err, ErrUnchanged) {
			return err
		}
		return nil
	}
	// a symlinked config (dotfiles) keeps its link: the target is replaced
	if resolved, err := filepath.EvalSymlinks(file); err == nil {
		file = resolved
	}
	unlock, err := lock(file + ".lock")
	if err != nil {
		return err
	}
	defer unlock()

	disk, err := read(file, hasFileContent(v))
	if err != nil {
		return fmt.Errorf("can't read config: %w", err)
	}
	switch err := fn(disk); {
	case errors.Is(err, ErrUnchanged):
	case err != nil:
		return err
	default:
		if err := replace(disk, file); err != nil {
			return fmt.Errorf("can't write config: %w", err)
		}
	}
	return Reload(v)
}

// Reload makes v read its config file again, to see what other processes changed. An
// empty file although v has content from it is being written by an older CLI version
// (see legacyWriteWait): v keeps what it has if the content doesn't show up in time.
func Reload(v *viper.Viper) error {
	file := v.ConfigFileUsed()
	if file == "" {
		return nil
	}
	if hasFileContent(v) {
		disk, err := read(file, true)
		if err != nil {
			return fmt.Errorf("can't read config: %w", err)
		}
		if len(disk.AllKeys()) == 0 {
			return nil
		}
	}
	if err := v.ReadInConfig(); err != nil {
		return fmt.Errorf("can't read config: %w", err)
	}
	return nil
}

// hasFileContent reports whether v read anything from its config file (defaults and
// flags don't count).
func hasFileContent(v *viper.Viper) bool {
	for _, key := range v.AllKeys() {
		if v.InConfig(key) {
			return true
		}
	}
	return false
}

// read reads file. An empty file although the process has settings is most likely
// being written by an older CLI version right now: wait a moment for its content, so
// the change isn't applied to nothing.
func read(file string, expectContent bool) (*viper.Viper, error) {
	deadline := time.Now().Add(legacyWriteWait)
	for {
		disk := viper.New()
		disk.SetConfigFile(file)
		disk.SetConfigType("yaml")
		if err := disk.ReadInConfig(); err != nil {
			return nil, err
		}
		if len(disk.AllKeys()) > 0 || !expectContent || time.Now().After(deadline) {
			return disk, nil
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// Set sets one key in the config file of v.
func Set(v *viper.Viper, key string, value any) error {
	return Update(v, func(disk *viper.Viper) error {
		disk.Set(key, value)
		return nil
	})
}

// replace writes disk to a temporary file next to file and renames it over file, so a
// reader sees the old or the new content, never a part of one.
func replace(disk *viper.Viper, file string) error {
	tmp, err := os.CreateTemp(filepath.Dir(file), "."+filepath.Base(file)+".tmp-*.yaml") // viper takes the format from the extension
	if err != nil {
		return err
	}
	name := tmp.Name()
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	// CreateTemp creates the file with 0600: the config holds logins
	if err := disk.WriteConfigAs(name); err != nil {
		os.Remove(name)
		return err
	}
	// a reader on Windows may hold the file open for a moment
	for attempt := 0; ; attempt++ {
		err = os.Rename(name, file)
		if err == nil || attempt == 20 {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if err != nil {
		os.Remove(name)
	}
	return err
}

// lock takes the exclusive lock on path, creating it if needed, and returns the
// function that releases it. The lock is advisory and ends with the process.
func lock(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("can't lock config: %w", err)
	}
	deadline := time.Now().Add(lockTimeout)
	for {
		err := tryLock(f)
		if err == nil {
			return func() {
				_ = unlockFile(f)
				_ = f.Close()
			}, nil
		}
		if !errors.Is(err, errLocked) {
			f.Close()
			return nil, fmt.Errorf("can't lock config: %w", err)
		}
		if time.Now().After(deadline) {
			f.Close()
			return nil, fmt.Errorf("can't lock config: another nova process holds %s for more than %s", path, lockTimeout)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// errLocked means another process holds the lock.
var errLocked = errors.New("locked")

// Merge3 merges the changes of two processes to a list of named entries. base is the
// list this process loaded, mine what it made of it, disk what the file holds now.
// Entries this process added, changed or removed are taken from mine; all others are
// taken from disk, so what other processes added, changed or removed is kept. The
// order is that of disk, followed by the entries added here.
func Merge3[T any](base, mine, disk []T, name func(T) string, equal func(a, b T) bool) []T {
	index := func(list []T) map[string]T {
		m := make(map[string]T, len(list))
		for _, e := range list {
			m[name(e)] = e
		}
		return m
	}
	baseBy, mineBy := index(base), index(mine)
	// changed reports whether this process touched the entry, and its new value
	touched := func(n string) (value T, exists, changed bool) {
		b, inBase := baseBy[n]
		m, inMine := mineBy[n]
		switch {
		case inBase && inMine:
			return m, true, !equal(b, m)
		case inMine:
			return m, true, true // added
		case inBase:
			return m, false, true // removed
		}
		return m, false, false
	}
	merged := make([]T, 0, len(disk)+len(mine))
	seen := make(map[string]bool, len(disk))
	for _, d := range disk {
		n := name(d)
		seen[n] = true
		value, exists, changed := touched(n)
		switch {
		case !changed:
			merged = append(merged, d)
		case exists:
			merged = append(merged, value)
		}
	}
	for _, m := range mine {
		n := name(m)
		if seen[n] {
			continue
		}
		// not in the file: added here, or changed here and removed elsewhere (the
		// change wins: the caller just worked on the entry). Unchanged entries the
		// file no longer has were removed elsewhere.
		if _, _, changed := touched(n); changed {
			merged = append(merged, m)
		}
	}
	return merged
}
