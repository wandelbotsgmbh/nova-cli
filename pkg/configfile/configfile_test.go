package configfile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spf13/viper"
)

// open returns the config of a "process": its own viper on the shared file.
func open(t *testing.T, file string) *viper.Viper {
	t.Helper()
	v := viper.New()
	v.SetConfigFile(file)
	if err := v.ReadInConfig(); err != nil {
		t.Fatal(err)
	}
	return v
}

// open2 is open without reading, for use outside the test goroutine.
func open2(file string) *viper.Viper {
	v := viper.New()
	v.SetConfigFile(file)
	return v
}

func newConfig(t *testing.T, content string) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return file
}

// The lost update: two processes read the config, each changes something else, both
// write. With viper.WriteConfig the second write drops the first change.
func TestUpdate_KeepsChangesOfOtherProcesses(t *testing.T) {
	file := newConfig(t, "editor: vi\n")
	a, b := open(t, file), open(t, file) // both hold the same old copy

	if err := Set(a, "image-registry", "ghcr.io/a"); err != nil {
		t.Fatal(err)
	}
	if err := Set(b, "container-runtime", "podman"); err != nil {
		t.Fatal(err)
	}

	got := open(t, file)
	for key, want := range map[string]string{"editor": "vi", "image-registry": "ghcr.io/a", "container-runtime": "podman"} {
		if got.GetString(key) != want {
			t.Errorf("%s = %q, want %q", key, got.GetString(key), want)
		}
	}
	// the process sees what the other one wrote before it
	if b.GetString("image-registry") != "ghcr.io/a" {
		t.Errorf("b doesn't see a's change")
	}
}

func TestUpdate_ManyWritersAtOnce(t *testing.T) {
	file := newConfig(t, "editor: vi\n")
	const writers = 30
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for i := range writers {
		v := open(t, file)
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- Set(v, fmt.Sprintf("key%d", i), i)
		}()
	}
	// a reader never sees an empty or cut-off file while they write
	stop := make(chan struct{})
	readerDone := make(chan error, 1)
	go func() {
		for {
			select {
			case <-stop:
				readerDone <- nil
				return
			default:
			}
			raw, err := os.ReadFile(file)
			if err != nil {
				continue // Windows: not readable in the moment of a rename, see Read
			}
			if !strings.Contains(string(raw), "editor: vi") {
				readerDone <- fmt.Errorf("partial config read: %q", raw)
				return
			}
			// and a process starting now can read it
			if err := Read(open2(file)); err != nil {
				readerDone <- fmt.Errorf("config not readable while others write: %w", err)
				return
			}
		}
	}()
	wg.Wait()
	close(stop)
	if err := <-readerDone; err != nil {
		t.Error(err)
	}
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	got := open(t, file)
	for i := range writers {
		if !got.IsSet(fmt.Sprintf("key%d", i)) {
			t.Errorf("key%d was lost", i)
		}
	}
	if leftovers, _ := filepath.Glob(filepath.Join(filepath.Dir(file), ".config.yaml.tmp-*")); len(leftovers) > 0 {
		t.Errorf("temporary files left behind: %v", leftovers)
	}
}

func TestUpdate_FailureAndUnchangedWriteNothing(t *testing.T) {
	file := newConfig(t, "editor: vi\n")
	v := open(t, file)
	before, _ := os.ReadFile(file)

	boom := errors.New("boom")
	if err := Update(v, func(disk *viper.Viper) error { disk.Set("editor", "emacs"); return boom }); !errors.Is(err, boom) {
		t.Fatalf("expected the function's error, got %v", err)
	}
	if err := Update(v, func(disk *viper.Viper) error { disk.Set("editor", "emacs"); return ErrUnchanged }); err != nil {
		t.Fatalf("ErrUnchanged is no failure, got %v", err)
	}
	if after, _ := os.ReadFile(file); string(after) != string(before) {
		t.Errorf("the file was written:\n%s", after)
	}
}

func TestUpdate_KeepsSymlinkAndPermissions(t *testing.T) {
	file := newConfig(t, "editor: vi\n")
	link := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.Symlink(file, link); err != nil {
		t.Skip("no symlinks here:", err)
	}
	if err := Set(open(t, link), "editor", "emacs"); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("the config is no symlink any more (%v)", err)
	}
	if got := open(t, file).GetString("editor"); got != "emacs" {
		t.Errorf("the link's target wasn't changed, editor = %q", got)
	}
	// Windows doesn't use these bits
	if info, _ := os.Stat(file); runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		t.Errorf("config readable by others: %v", info.Mode().Perm())
	}
}

// CLI versions before this package truncate the file and then write it: a change must
// wait for their content instead of being applied to nothing.
func TestUpdate_WaitsForAnOlderVersionsWrite(t *testing.T) {
	file := newConfig(t, "editor: vi\ninstances:\n  - name: a\n")
	v := open(t, file)
	if err := os.WriteFile(file, nil, 0o600); err != nil { // truncated ...
		t.Fatal(err)
	}
	go func() { // ... and written a moment later
		time.Sleep(100 * time.Millisecond)
		_ = os.WriteFile(file, []byte("editor: vi\ninstances:\n  - name: a\n  - name: b\n"), 0o600)
	}()
	if err := Set(v, "editor", "emacs"); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(file)
	if !strings.Contains(string(raw), "name: b") || !strings.Contains(string(raw), "editor: emacs") {
		t.Errorf("the other writer's content was lost:\n%s", raw)
	}
}

func TestUpdate_LockTimeout(t *testing.T) {
	old := lockTimeout
	lockTimeout = 100 * time.Millisecond
	t.Cleanup(func() { lockTimeout = old })
	file := newConfig(t, "editor: vi\n")
	unlock, err := lock(file + ".lock")
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if err := Set(open(t, file), "editor", "emacs"); err == nil || !strings.Contains(err.Error(), "another nova process") {
		t.Errorf("expected a lock timeout, got %v", err)
	}
}

func TestMerge3(t *testing.T) {
	type entry struct{ name, value string }
	name := func(e entry) string { return e.name }
	equal := func(a, b entry) bool { return a == b }
	list := func(s string) []entry { // "a=1 b=2"
		var out []entry
		for _, f := range strings.Fields(s) {
			n, v, _ := strings.Cut(f, "=")
			out = append(out, entry{n, v})
		}
		return out
	}
	show := func(l []entry) string {
		parts := make([]string, len(l))
		for i, e := range l {
			parts[i] = e.name + "=" + e.value
		}
		return strings.Join(parts, " ")
	}
	for _, tc := range []struct{ what, base, mine, disk, want string }{
		{"nothing changed", "a=1 b=1", "a=1 b=1", "a=1 b=1", "a=1 b=1"},
		{"added here and elsewhere", "a=1", "a=1 b=1", "a=1 c=1", "a=1 c=1 b=1"},
		{"changed here, other changed elsewhere", "a=1 b=1", "a=2 b=1", "a=1 b=2", "a=2 b=2"},
		{"removed here", "a=1 b=1", "b=1", "a=1 b=1 c=1", "b=1 c=1"},
		{"removed elsewhere", "a=1 b=1", "a=1 b=1", "b=1", "b=1"},
		{"changed here, removed elsewhere: the change wins", "a=1 b=1", "a=2 b=1", "b=1", "b=1 a=2"},
		{"same name added on both sides: this one wins", "", "a=mine", "a=theirs", "a=mine"},
		{"removed on both sides", "a=1 b=1", "b=1", "b=1", "b=1"},
	} {
		if got := show(Merge3(list(tc.base), list(tc.mine), list(tc.disk), name, equal)); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.what, got, tc.want)
		}
	}
}

// viper.WriteConfig writes the whole in-memory copy of a process over the file: every
// write has to go through Update.
func TestNoDirectConfigWrites(t *testing.T) {
	root := filepath.Join("..", "..")
	allowed := map[string]bool{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); name == ".git" || name == "node_modules" || name == "_site" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || allowed[rel] || strings.HasPrefix(rel, filepath.Join("pkg", "configfile")) {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, call := range []string{".WriteConfig(", ".SafeWriteConfig(", ".WriteConfigAs(", ".SafeWriteConfigAs("} {
			if strings.Contains(string(src), call) {
				t.Errorf("%s calls %s: use configfile.Update or configfile.Set", rel, strings.Trim(call, ".("))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestUpdate_WithoutConfigFile(t *testing.T) {
	v := viper.New()
	if err := Update(v, func(*viper.Viper) error { return ErrUnchanged }); err != nil {
		t.Errorf("ErrUnchanged is no failure without a file either, got %v", err)
	}
	if err := Set(v, "editor", "vi"); err != nil || v.GetString("editor") != "vi" {
		t.Errorf("expected the change in memory, got %v / %q", err, v.GetString("editor"))
	}
}

// An older CLI version truncated the file and hasn't written it yet: a process that
// reloads in that moment keeps what it has.
func TestReload_KeepsContentWhileFileIsEmpty(t *testing.T) {
	old := legacyWriteWait
	legacyWriteWait = 50 * time.Millisecond
	t.Cleanup(func() { legacyWriteWait = old })
	file := newConfig(t, "editor: vi\n")
	v := open(t, file)
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Reload(v); err != nil || v.GetString("editor") != "vi" {
		t.Errorf("expected the old content, got %v / %q", err, v.GetString("editor"))
	}
	if err := os.WriteFile(file, []byte("editor: emacs\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Reload(v); err != nil || v.GetString("editor") != "emacs" {
		t.Errorf("expected the new content, got %v / %q", err, v.GetString("editor"))
	}
}
