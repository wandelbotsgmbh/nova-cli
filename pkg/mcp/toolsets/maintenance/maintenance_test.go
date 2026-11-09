package maintenance_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/internal/livetest"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/novaapi"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/tool"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/toolsets/maintenance"
)

func gzipped(t *testing.T, s string) []byte {
	t.Helper()
	var b bytes.Buffer
	w := gzip.NewWriter(&b)
	_, _ = w.Write([]byte(s))
	_ = w.Close()
	return b.Bytes()
}

// fakeInstance serves the maintenance endpoints and records every write.
type fakeInstance struct {
	t          *testing.T
	archive    []byte
	nextUpdate string // "" answers 204
	mu         sync.Mutex
	writes     []string
	restored   []byte
	updateBody map[string]any
}

func (f *fakeInstance) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v2")
	if r.Method != http.MethodGet {
		f.mu.Lock()
		f.writes = append(f.writes, r.Method+" "+path+"?"+r.URL.RawQuery)
		f.mu.Unlock()
	}
	switch r.Method + " " + path {
	case "GET /system/configuration/resources":
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":"v2/cells/cell.nova","name":"cell","children":[
			{"id":"v2/cells/cell/controllers/ur5e.nova","name":"ur5e","children":[]},
			{"id":"v2/cells/cell/apps/test.nova","name":"test","children":[]}]}]`))
	case "GET /system/configuration/status":
		if r.URL.Query().Get("operation_id") != "b-1" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"not found"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"creating","progress":0.5}`))
	case "GET /system/configuration":
		if r.URL.Query().Get("resources") == "unknown" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"message":"Invalid arguments: unknown resource: unknown"}`))
			return
		}
		w.Header().Set("Content-Type", "application/gzip")
		w.Header().Set("X-Backup-Id", "b-1")
		_, _ = w.Write(f.archive)
	case "POST /system/configuration":
		f.restored, _ = io.ReadAll(r.Body)
	case "GET /system/version":
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("26.8.0"))
	case "GET /system/update":
		if f.nextUpdate == "" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(f.nextUpdate))
	case "PUT /system/update":
		_ = json.NewDecoder(r.Body).Decode(&f.updateBody)
	default:
		f.t.Errorf("unexpected request %s %s", r.Method, r.URL)
		http.NotFound(w, r)
	}
}

type elicitFn func(context.Context, *sdk.ElicitRequest) (*sdk.ElicitResult, error)

func setup(t *testing.T, f *fakeInstance, downloadDir string, elicit elicitFn) *sdk.ClientSession {
	t.Helper()
	f.t = t
	server := httptest.NewServer(f)
	t.Cleanup(server.Close)
	return connect(t, novaapi.NewClient(server.URL+"/api/v2", nil, nil, nil), downloadDir, elicit)
}

func connect(t *testing.T, client *novaapi.Client, downloadDir string, elicit elicitFn) *sdk.ClientSession {
	t.Helper()
	s := sdk.NewServer(&sdk.Implementation{Name: "t", Version: "t"}, nil)
	for _, reg := range maintenance.Tools(tool.Env{Client: client, DownloadDir: downloadDir}) {
		if reg.Toolset != tool.Maintenance {
			t.Fatalf("wrong toolset %q", reg.Toolset)
		}
		reg.Add(s)
	}
	ct, st := sdk.NewInMemoryTransports()
	if _, err := s.Connect(context.Background(), st, nil); err != nil {
		t.Fatal(err)
	}
	opts := &sdk.ClientOptions{}
	if elicit != nil {
		opts.ElicitationHandler = elicit
	}
	cs, err := sdk.NewClient(&sdk.Implementation{Name: "c", Version: "c"}, opts).Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func call(t *testing.T, cs *sdk.ClientSession, name string, args map[string]any, out any) (string, bool) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if out != nil && !res.IsError {
		raw, _ := json.Marshal(res.StructuredContent)
		if err := json.Unmarshal(raw, out); err != nil {
			t.Fatalf("%s: decode %s: %v", name, raw, err)
		}
	}
	return res.Content[0].(*sdk.TextContent).Text, res.IsError
}

func TestReadOnlyFlags(t *testing.T) {
	client := novaapi.NewClient("http://nova.invalid", nil, nil, nil)
	got := map[string]bool{}
	for _, reg := range maintenance.Tools(tool.Env{Client: client}) {
		s := sdk.NewServer(&sdk.Implementation{Name: "p", Version: "p"}, nil)
		reg.Add(s)
		ct, st := sdk.NewInMemoryTransports()
		if _, err := s.Connect(context.Background(), st, nil); err != nil {
			t.Fatal(err)
		}
		cs, err := sdk.NewClient(&sdk.Implementation{Name: "c", Version: "c"}, nil).Connect(context.Background(), ct, nil)
		if err != nil {
			t.Fatal(err)
		}
		res, err := cs.ListTools(context.Background(), nil)
		if err != nil || len(res.Tools) != 1 {
			t.Fatalf("one tool per registration: %v %v", err, res)
		}
		tl := res.Tools[0]
		if tl.Annotations.ReadOnlyHint != reg.ReadOnly {
			t.Errorf("%s: readOnlyHint %v but Registration.ReadOnly %v", tl.Name, tl.Annotations.ReadOnlyHint, reg.ReadOnly)
		}
		got[tl.Name] = reg.ReadOnly
		_ = cs.Close()
	}
	want := map[string]bool{"list_backup_resources": true, "get_backup_status": true,
		"create_backup": false, "restore_backup": false, "update_nova_version": false}
	if len(got) != len(want) {
		t.Fatalf("tools %v, want %v", got, want)
	}
	for name, ro := range want {
		if got[name] != ro {
			t.Errorf("%s: ReadOnly %v, want %v", name, got[name], ro)
		}
	}
}

func TestListResourcesAndStatus(t *testing.T) {
	cs := setup(t, &fakeInstance{}, t.TempDir(), nil)
	var list struct {
		Resources []struct{ ID, Name, Parent string } `json:"resources"`
		Total     int                                 `json:"total"`
	}
	text, isErr := call(t, cs, "list_backup_resources", map[string]any{}, &list)
	if isErr || list.Total != 3 || list.Resources[1].Parent != "v2/cells/cell.nova" {
		t.Fatalf("%s %+v", text, list)
	}

	var status struct {
		Status   string   `json:"status"`
		Progress *float64 `json:"progress"`
	}
	text, isErr = call(t, cs, "get_backup_status", map[string]any{"backup_id": "b-1"}, &status)
	if isErr || status.Status != "creating" || status.Progress == nil || *status.Progress != 0.5 {
		t.Fatalf("%s %+v", text, status)
	}
	if text, isErr := call(t, cs, "get_backup_status", map[string]any{"backup_id": "nope"}, nil); !isErr || !strings.HasPrefix(text, "NOT_FOUND:") {
		t.Errorf("unknown backup: %s", text)
	}
}

func TestCreateBackup_SavesFileNeverContent(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "downloads")
	f := &fakeInstance{archive: gzipped(t, "secret-controller-password")}
	cs := setup(t, f, dir, nil)

	var out struct {
		Path      string `json:"path"`
		SizeBytes int    `json:"size_bytes"`
		BackupID  string `json:"backup_id"`
	}
	res, err := cs.CallTool(context.Background(), &sdk.CallToolParams{Name: "create_backup",
		Arguments: map[string]any{"resources": []string{"v2/cells/cell.nova", " ", "v2/cells/cell/apps/test.nova"}}})
	if err != nil || res.IsError {
		t.Fatalf("%v %+v", err, res)
	}
	raw, _ := json.Marshal(res)
	if bytes.Contains(raw, []byte("secret")) {
		t.Fatalf("the backup content must never reach the client: %s", raw)
	}
	sc, _ := json.Marshal(res.StructuredContent)
	_ = json.Unmarshal(sc, &out)
	if !filepath.IsAbs(out.Path) || filepath.Dir(out.Path) != dir || out.BackupID != "b-1" || out.SizeBytes != len(f.archive) {
		t.Fatalf("unexpected result %+v", out)
	}
	saved, err := os.ReadFile(out.Path)
	if err != nil || !bytes.Equal(saved, f.archive) {
		t.Fatalf("saved file differs: %v", err)
	}
	if info, _ := os.Stat(out.Path); info.Mode().Perm() != 0o600 {
		t.Errorf("backups hold secrets, want mode 0600, got %v", info.Mode().Perm())
	}
	if len(f.writes) != 0 {
		t.Errorf("a backup must not write to the instance: %v", f.writes)
	}

	if text, isErr := call(t, cs, "create_backup", map[string]any{"resources": []string{"unknown"}}, nil); !isErr ||
		!strings.Contains(text, "INVALID_ARGUMENT:") || !strings.Contains(text, "unknown resource") {
		t.Errorf("unknown resource: %s", text)
	}
}

func backupFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "backup.tar.gz")
	if err := os.WriteFile(path, gzipped(t, "archive"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func accept(_ context.Context, _ *sdk.ElicitRequest) (*sdk.ElicitResult, error) {
	return &sdk.ElicitResult{Action: "accept", Content: map[string]any{"approve": true}}, nil
}

func decline(_ context.Context, _ *sdk.ElicitRequest) (*sdk.ElicitResult, error) {
	return &sdk.ElicitResult{Action: "decline"}, nil
}

func TestRestoreBackup_NeedsApproval(t *testing.T) {
	path := backupFile(t)
	for name, elicit := range map[string]elicitFn{"no elicitation": nil, "declined": decline} {
		t.Run(name, func(t *testing.T) {
			f := &fakeInstance{}
			cs := setup(t, f, "", elicit)
			text, isErr := call(t, cs, "restore_backup", map[string]any{"path": path}, nil)
			if !isErr || !strings.HasPrefix(text, "APPROVAL_REQUIRED:") {
				t.Fatalf("got %s", text)
			}
			if len(f.writes) != 0 {
				t.Fatalf("nothing may be sent without approval: %v", f.writes)
			}
		})
	}
}

func TestRestoreBackup_Approved(t *testing.T) {
	path := backupFile(t)
	var asked string
	f := &fakeInstance{}
	cs := setup(t, f, "", func(ctx context.Context, req *sdk.ElicitRequest) (*sdk.ElicitResult, error) {
		asked = req.Params.Message
		return accept(ctx, req)
	})
	text, isErr := call(t, cs, "restore_backup", map[string]any{"path": path, "resources": []string{"v2/cells/cell.nova"}}, nil)
	if isErr || !strings.HasPrefix(text, "OK:") {
		t.Fatalf("got %s", text)
	}
	if len(f.writes) != 1 || f.writes[0] != "POST /system/configuration?resources=v2%2Fcells%2Fcell.nova" {
		t.Fatalf("expected one restore call, got %v", f.writes)
	}
	if want, _ := os.ReadFile(path); !bytes.Equal(f.restored, want) {
		t.Error("the archive must be sent unchanged")
	}
	for _, want := range []string{"127.0.0.1", path, "v2/cells/cell.nova", "overwrites"} {
		if !strings.Contains(asked, want) {
			t.Errorf("approval message must name %q: %q", want, asked)
		}
	}
}

func TestRestoreBackup_ValidatesFileBeforeAsking(t *testing.T) {
	dir := t.TempDir()
	notGzip := filepath.Join(dir, "notes.txt")
	_ = os.WriteFile(notGzip, []byte("hello"), 0o600)
	asked := false
	f := &fakeInstance{}
	cs := setup(t, f, "", func(ctx context.Context, req *sdk.ElicitRequest) (*sdk.ElicitResult, error) {
		asked = true
		return accept(ctx, req)
	})
	for _, p := range []string{"", filepath.Join(dir, "missing.tar.gz"), dir, notGzip} {
		if text, isErr := call(t, cs, "restore_backup", map[string]any{"path": p}, nil); !isErr || !strings.HasPrefix(text, "INVALID_ARGUMENT:") {
			t.Errorf("%q: %s", p, text)
		}
	}
	if asked || len(f.writes) != 0 {
		t.Errorf("invalid input must fail before asking or sending: asked=%v writes=%v", asked, f.writes)
	}
}

func TestUpdateNovaVersion(t *testing.T) {
	t.Run("no update needs no approval", func(t *testing.T) {
		f := &fakeInstance{}
		cs := setup(t, f, "", nil)
		var out struct{ Status string }
		text, isErr := call(t, cs, "update_nova_version", map[string]any{"channel": "stable"}, &out)
		if isErr || out.Status != "up_to_date" || len(f.writes) != 0 {
			t.Fatalf("%s %+v %v", text, out, f.writes)
		}
	})
	for name, elicit := range map[string]elicitFn{"no elicitation": nil, "declined": decline} {
		t.Run(name, func(t *testing.T) {
			f := &fakeInstance{nextUpdate: "26.8.1"}
			cs := setup(t, f, "", elicit)
			if text, isErr := call(t, cs, "update_nova_version", map[string]any{"channel": "stable"}, nil); !isErr || !strings.HasPrefix(text, "APPROVAL_REQUIRED:") {
				t.Fatalf("got %s", text)
			}
			if len(f.writes) != 0 {
				t.Fatalf("nothing may be sent without approval: %v", f.writes)
			}
		})
	}
	t.Run("approved", func(t *testing.T) {
		var asked string
		f := &fakeInstance{nextUpdate: "26.8.1"}
		cs := setup(t, f, "", func(ctx context.Context, req *sdk.ElicitRequest) (*sdk.ElicitResult, error) {
			asked = req.Params.Message
			return accept(ctx, req)
		})
		var out struct {
			Status        string `json:"status"`
			TargetVersion string `json:"target_version"`
		}
		text, isErr := call(t, cs, "update_nova_version", map[string]any{"channel": "stable", "update_cells": false}, &out)
		if isErr || out.Status != "pending" || out.TargetVersion != "26.8.1" || len(f.writes) != 1 {
			t.Fatalf("%s %v", text, f.writes)
		}
		if f.updateBody["channel"] != "stable" || f.updateBody["update_cells"] != false {
			t.Errorf("body %v", f.updateBody)
		}
		for _, want := range []string{"26.8.0", "26.8.1", "stable", "keep their version"} {
			if !strings.Contains(asked, want) {
				t.Errorf("approval message must name %q: %q", want, asked)
			}
		}
	})
	t.Run("invalid channel", func(t *testing.T) {
		cs := setup(t, &fakeInstance{}, "", accept)
		if text, isErr := call(t, cs, "update_nova_version", map[string]any{"channel": "beta"}, nil); !isErr || !strings.HasPrefix(text, "INVALID_ARGUMENT:") {
			t.Errorf("got %s", text)
		}
	})
}

// Live: reads and a backup only. Restore and update are never called against an instance.
func TestLiveBackup(t *testing.T) {
	client := livetest.Client(t)
	dir := t.TempDir()
	cs := connect(t, client, dir, nil)
	var list struct {
		Resources []struct{ ID string } `json:"resources"`
		Total     int                   `json:"total"`
	}
	text, isErr := call(t, cs, "list_backup_resources", map[string]any{}, &list)
	if isErr || list.Total == 0 {
		t.Fatalf("list: %s", text)
	}
	t.Log(text)
	var backup struct {
		Path     string `json:"path"`
		BackupID string `json:"backup_id"`
	}
	text, isErr = call(t, cs, "create_backup", map[string]any{"resources": []string{list.Resources[0].ID}}, &backup)
	if isErr {
		t.Fatalf("backup: %s", text)
	}
	t.Log(text)
	text, isErr = call(t, cs, "get_backup_status", map[string]any{"backup_id": backup.BackupID}, nil)
	if isErr {
		t.Fatalf("status: %s", text)
	}
	t.Log(text)
	if err := os.Remove(backup.Path); err != nil {
		t.Error(err)
	}
}
