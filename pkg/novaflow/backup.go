package novaflow

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strings"
	"time"
)

// BackupFormat is the version of the archive layout.
const BackupFormat = 1

// Archive layout:
//
//	manifest.json     Manifest
//	globals.json      []Global
//	flows/<id>.json   one Forest record per flow, as the instance sent it
//	objects/<key>     cell object store content (optional, without Forest's trees/)
const (
	manifestFile = "manifest.json"
	globalsFile  = "globals.json"
	flowsDir     = "flows/"
	objectsDir   = "objects/"
)

// Manifest describes a backup.
type Manifest struct {
	Format          int             `json:"format"`
	CreatedAt       time.Time       `json:"created_at"`
	Host            string          `json:"host"`
	Cell            string          `json:"cell"`
	NovaflowVersion string          `json:"novaflow_version,omitempty"`
	Flows           []string        `json:"flows"`
	Globals         int             `json:"globals"`
	Objects         []string        `json:"objects,omitempty"`
	Corrupted       []CorruptedFlow `json:"corrupted,omitempty"`
}

// BackupOptions select what goes into a backup.
type BackupOptions struct {
	IncludeObjects bool
	// ObjectPrefix limits the objects to keys with this prefix.
	ObjectPrefix string
	// Progress, if set, is called before each step.
	Progress func(msg string)
}

// ErrCorruptedFlows means the backup was written, but Forest could not read
// some flows, so they are missing from it.
var ErrCorruptedFlows = errors.New("some flows could not be read and are missing from the backup")

// Backup writes flows, globals and optionally the cell object store to a
// gzipped tar file. When Forest reports unreadable flows, the archive is
// still written and the returned error wraps ErrCorruptedFlows.
func (c *Client) Backup(ctx context.Context, w io.Writer, opts BackupOptions) (*Manifest, error) {
	progress := opts.Progress
	if progress == nil {
		progress = func(string) {}
	}

	m := &Manifest{Format: BackupFormat, CreatedAt: time.Now().UTC(), Host: c.base, Cell: c.cell, Flows: []string{}}
	if f, err := c.FeatureFlags(ctx); err == nil && f.Status != nil {
		m.NovaflowVersion = f.Status.DeployedVersion
	}

	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)

	progress("listing flows")
	list, err := c.ListFlows(ctx)
	if err != nil {
		return nil, err
	}
	m.Corrupted = list.Corrupted
	for i, s := range list.Flows {
		progress(fmt.Sprintf("downloading flow %d/%d: %s", i+1, len(list.Flows), s.ID))
		if s.ID == "" || strings.ContainsAny(s.ID, "/\\") || strings.Contains(s.ID, "..") {
			return nil, fmt.Errorf("unexpected flow id %q", s.ID)
		}
		raw, err := c.GetFlowRaw(ctx, s.ID)
		if err != nil {
			return nil, fmt.Errorf("flow %s: %w", s.ID, err)
		}
		if err := writeTarFile(tw, flowsDir+s.ID+".json", raw); err != nil {
			return nil, err
		}
		m.Flows = append(m.Flows, s.ID)
	}

	progress("downloading globals")
	globals, err := c.ListGlobals(ctx)
	if err != nil {
		return nil, fmt.Errorf("globals: %w", err)
	}
	b, err := json.MarshalIndent(globals, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := writeTarFile(tw, globalsFile, b); err != nil {
		return nil, err
	}
	m.Globals = len(globals)

	if opts.IncludeObjects {
		progress("listing objects")
		keys, err := c.ListObjects(ctx)
		if err != nil {
			return nil, fmt.Errorf("object store: %w", err)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if !strings.HasPrefix(key, opts.ObjectPrefix) || strings.HasPrefix(key, ForestObjectPrefix) {
				continue
			}
			if err := ValidObjectKey(key); err != nil {
				return nil, err
			}
			progress("downloading object " + key)
			data, _, err := c.GetObject(ctx, key)
			if err != nil {
				return nil, fmt.Errorf("object %s: %w", key, err)
			}
			if err := writeTarFile(tw, objectsDir+key, data); err != nil {
				return nil, err
			}
			m.Objects = append(m.Objects, key)
		}
	}

	b, err = json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := writeTarFile(tw, manifestFile, b); err != nil {
		return nil, err
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	if len(m.Corrupted) > 0 {
		ids := make([]string, len(m.Corrupted))
		for i, cf := range m.Corrupted {
			ids[i] = cf.ID
		}
		return m, fmt.Errorf("%w: %s", ErrCorruptedFlows, strings.Join(ids, ", "))
	}
	return m, nil
}

func writeTarFile(tw *tar.Writer, name string, data []byte) error {
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(data)), ModTime: time.Now()}); err != nil {
		return err
	}
	_, err := tw.Write(data)
	return err
}

// BackupContent is a read backup archive.
type BackupContent struct {
	Manifest Manifest
	Flows    map[string]*Flow
	Globals  []Global
	Objects  map[string][]byte
}

// ReadBackup reads a backup archive.
func ReadBackup(filename string) (*BackupContent, error) {
	f, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("%s is not a NovaFlow backup: %w", filename, err)
	}
	tr := tar.NewReader(gz)

	bc := &BackupContent{Flows: map[string]*Flow{}, Objects: map[string][]byte{}}
	haveManifest := false
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", filename, err)
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			return nil, err
		}
		name := path.Clean(h.Name)
		switch {
		case name == manifestFile:
			if err := json.Unmarshal(data, &bc.Manifest); err != nil {
				return nil, fmt.Errorf("%s: %w", manifestFile, err)
			}
			haveManifest = true
		case name == globalsFile:
			if err := json.Unmarshal(data, &bc.Globals); err != nil {
				return nil, fmt.Errorf("%s: %w", globalsFile, err)
			}
		case strings.HasPrefix(name, flowsDir) && strings.HasSuffix(name, ".json"):
			var fl Flow
			if err := json.Unmarshal(data, &fl); err != nil {
				return nil, fmt.Errorf("%s: %w", name, err)
			}
			bc.Flows[fl.ID] = &fl
		case strings.HasPrefix(name, objectsDir):
			key := strings.TrimPrefix(name, objectsDir)
			if err := ValidObjectKey(key); err != nil {
				return nil, err
			}
			bc.Objects[key] = data
		}
	}
	if !haveManifest {
		return nil, fmt.Errorf("%s is not a NovaFlow backup: no %s", filename, manifestFile)
	}
	if bc.Manifest.Format > BackupFormat {
		return nil, fmt.Errorf("%s has backup format %d, this nova version reads up to %d: update nova", filename, bc.Manifest.Format, BackupFormat)
	}
	return bc, nil
}

// RestoreOptions control a restore.
type RestoreOptions struct {
	// Only restores just these flow ids (all if empty).
	Only []string
	// Overwrite replaces existing flows, globals and objects; otherwise they are skipped.
	Overwrite      bool
	SkipGlobals    bool
	IncludeObjects bool
	DryRun         bool
	Progress       func(msg string)
}

// RestoreAction is what a restore did (or would do) with one item.
type RestoreAction string

const (
	Created  RestoreAction = "create"
	Replaced RestoreAction = "replace"
	Skipped  RestoreAction = "skip (exists)"
)

// RestoreItem is one restored item.
type RestoreItem struct {
	Kind   string // "flow", "global" or "object"
	Key    string
	Action RestoreAction
}

// Restore writes a backup to the cell: globals first (flows refer to them),
// then flows, then objects.
func (c *Client) Restore(ctx context.Context, bc *BackupContent, opts RestoreOptions) ([]RestoreItem, error) {
	progress := opts.Progress
	if progress == nil {
		progress = func(string) {}
	}
	var items []RestoreItem

	flowIDs, err := selectFlows(bc, opts.Only)
	if err != nil {
		return nil, err
	}

	if !opts.SkipGlobals && len(bc.Globals) > 0 {
		progress("restoring globals")
		existing, err := c.ListGlobals(ctx)
		if err != nil {
			return nil, fmt.Errorf("globals: %w", err)
		}
		have := map[string]bool{}
		for _, g := range existing {
			have[g.Key] = true
		}
		var write []Global
		for _, g := range bc.Globals {
			action := Created
			if have[g.Key] {
				action = Replaced
				if !opts.Overwrite {
					action = Skipped
				}
			}
			items = append(items, RestoreItem{Kind: "global", Key: g.Key, Action: action})
			if action != Skipped {
				write = append(write, g)
			}
		}
		if !opts.DryRun {
			if err := c.PutGlobals(ctx, write); err != nil {
				return items, fmt.Errorf("globals: %w", err)
			}
		}
	}

	list, err := c.ListFlows(ctx)
	if err != nil {
		return items, err
	}
	have := map[string]bool{}
	for _, s := range list.Flows {
		have[s.ID] = true
	}
	for _, cf := range list.Corrupted {
		have[cf.ID] = true
	}
	for i, id := range flowIDs {
		fl := bc.Flows[id]
		action := Created
		if have[id] {
			action = Replaced
			if !opts.Overwrite {
				action = Skipped
			}
		}
		items = append(items, RestoreItem{Kind: "flow", Key: id, Action: action})
		if opts.DryRun || action == Skipped {
			continue
		}
		progress(fmt.Sprintf("restoring flow %d/%d: %s", i+1, len(flowIDs), id))
		if action == Created {
			err = c.CreateFlow(ctx, fl)
		} else {
			err = c.UpdateFlow(ctx, fl, nil)
		}
		if err != nil {
			return items, fmt.Errorf("flow %s: %w", id, err)
		}
	}

	if opts.IncludeObjects && len(bc.Objects) > 0 {
		progress("listing objects")
		keys, err := c.ListObjects(ctx)
		if err != nil {
			return items, fmt.Errorf("object store: %w", err)
		}
		haveObj := map[string]bool{}
		for _, k := range keys {
			haveObj[k] = true
		}
		objKeys := make([]string, 0, len(bc.Objects))
		for k := range bc.Objects {
			if !strings.HasPrefix(k, ForestObjectPrefix) {
				objKeys = append(objKeys, k)
			}
		}
		sort.Strings(objKeys)
		for _, key := range objKeys {
			action := Created
			if haveObj[key] {
				action = Replaced
				if !opts.Overwrite {
					action = Skipped
				}
			}
			items = append(items, RestoreItem{Kind: "object", Key: key, Action: action})
			if opts.DryRun || action == Skipped {
				continue
			}
			progress("restoring object " + key)
			if err := c.PutObject(ctx, key, bc.Objects[key]); err != nil {
				return items, fmt.Errorf("object %s: %w", key, err)
			}
		}
	}
	return items, nil
}

func selectFlows(bc *BackupContent, only []string) ([]string, error) {
	if len(only) == 0 {
		ids := make([]string, 0, len(bc.Flows))
		for id := range bc.Flows {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		return ids, nil
	}
	for _, id := range only {
		if _, ok := bc.Flows[id]; !ok {
			return nil, fmt.Errorf("flow %q is not in the backup", id)
		}
	}
	return only, nil
}
