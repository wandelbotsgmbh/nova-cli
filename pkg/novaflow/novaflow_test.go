package novaflow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeInstance serves the parts of a NOVA instance with NovaFlow the client uses.
type fakeInstance struct {
	mu        sync.Mutex
	flags     map[string]any
	flows     map[string]map[string]any
	corrupted []CorruptedFlow
	globals   map[string]map[string]any
	objects   map[string][]byte
	// lastFlagsPut is the body of the last feature flag PUT
	lastFlagsPut map[string]any
	// paths of all requests, for asserting on escaping
	paths []string
}

func newFakeInstance() *fakeInstance {
	return &fakeInstance{
		flags:   map[string]any{"controller_connector": true, "novaflow": false, "novaflow_status": map[string]any{"state": "disabled"}},
		flows:   map[string]map[string]any{},
		globals: map[string]map[string]any{},
		objects: map[string][]byte{},
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (f *fakeInstance) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.paths = append(f.paths, r.URL.EscapedPath())
	p := r.URL.Path
	switch {
	case p == "/api/v2/internal/cells/cell/feature-flags":
		if r.Method == http.MethodPut {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.lastFlagsPut = map[string]any{}
			for k, v := range body {
				f.lastFlagsPut[k] = v
			}
			status := map[string]any{"state": "disabled", "enabled": false}
			if body["novaflow"] == true {
				status = map[string]any{"state": "deployed", "enabled": true, "deployed_version": "2026.9.71"}
			}
			body["novaflow_status"] = status
			f.flags = body
		}
		writeJSON(w, 200, f.flags)

	case p == "/cell/forest/trees" && r.Method == http.MethodGet:
		ids := make([]string, 0, len(f.flows))
		for id := range f.flows {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		end := min(offset+limit, len(ids))
		items := []map[string]any{}
		for _, id := range ids[min(offset, len(ids)):end] {
			fl := f.flows[id]
			items = append(items, map[string]any{"id": id, "name": fl["name"], "created_at": fl["created_at"], "updated_at": fl["updated_at"]})
		}
		writeJSON(w, 200, map[string]any{"items": items, "total": len(ids), "limit": limit, "offset": offset, "has_more": end < len(ids), "corrupted": f.corrupted})

	case p == "/cell/forest/trees" && r.Method == http.MethodPost:
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		id := body["id"].(string)
		if _, ok := f.flows[id]; ok {
			writeJSON(w, 409, map[string]any{"detail": "exists"})
			return
		}
		body["revision"] = 1
		body["created_at"] = "2026-11-01T10:00:00Z"
		body["updated_at"] = "2026-11-01T10:00:00Z"
		f.flows[id] = body
		writeJSON(w, 201, body)

	case strings.HasPrefix(p, "/cell/forest/trees/"):
		id := strings.TrimPrefix(p, "/cell/forest/trees/")
		fl, ok := f.flows[id]
		if !ok {
			writeJSON(w, 404, map[string]any{"detail": "not found"})
			return
		}
		if r.Method == http.MethodDelete {
			if im := r.Header.Get("If-Match"); im != "" && im != strconv.Quote(strconv.Itoa(int(toFloat(fl["revision"])))) {
				writeJSON(w, 412, map[string]any{"detail": "precondition failed"})
				return
			}
			delete(f.flows, id)
			writeJSON(w, 200, map[string]any{"message": "deleted"})
			return
		}
		if r.Method == http.MethodPut {
			if im := r.Header.Get("If-Match"); im != "" && im != strconv.Quote(strconv.Itoa(int(toFloat(fl["revision"])))) {
				writeJSON(w, 412, map[string]any{"detail": "precondition failed"})
				return
			}
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if _, ok := body["id"]; ok {
				writeJSON(w, 422, map[string]any{"detail": "id must not be sent on update"})
				return
			}
			for k, v := range body {
				fl[k] = v
			}
			fl["revision"] = toFloat(fl["revision"]) + 1
		}
		writeJSON(w, 200, fl)

	case p == "/cell/controlplane/api/v1/globals" && r.Method == http.MethodGet:
		list := []map[string]any{}
		for k, g := range f.globals {
			e := map[string]any{"key": k, "timestamp": "2026-11-01T10:00:00Z", "json_schema": nil}
			for kk, vv := range g {
				e[kk] = vv
			}
			list = append(list, e)
		}
		writeJSON(w, 200, map[string]any{"globals": list})

	case p == "/cell/controlplane/api/v1/globals" && r.Method == http.MethodPut:
		var body struct {
			Globals map[string]map[string]any `json:"globals"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		for k, g := range body.Globals {
			if _, ok := g["json_schema"]; ok && g["json_schema"] == nil {
				writeJSON(w, 400, map[string]any{"detail": "json_schema null"})
				return
			}
			f.globals[k] = g
		}
		writeJSON(w, 200, map[string]any{"count": len(body.Globals)})

	case p == "/api/v2/cells/cell/store/objects":
		keys := []string{}
		for k := range f.objects {
			keys = append(keys, k)
		}
		writeJSON(w, 200, keys)

	case strings.HasPrefix(p, "/api/v2/cells/cell/store/objects/"):
		key := strings.TrimPrefix(p, "/api/v2/cells/cell/store/objects/")
		if r.Method == http.MethodPut {
			file, _, err := r.FormFile("any_value")
			if err != nil {
				writeJSON(w, 400, map[string]any{"message": err.Error()})
				return
			}
			b, _ := io.ReadAll(file)
			f.objects[key] = b
			w.WriteHeader(204)
			return
		}
		b, ok := f.objects[key]
		if !ok {
			writeJSON(w, 404, map[string]any{"message": "not found"})
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(b)

	default:
		// like the ingress for an app that isn't installed
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(404)
		_, _ = w.Write([]byte("<html>404 page not found</html>"))
	}
}

func toFloat(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	}
	return 0
}

func newTestClient(t *testing.T, h http.Handler) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return newClient(srv.URL, "cell", srv.Client())
}

func TestSetNovaflowKeepsOtherFlags(t *testing.T) {
	fake := newFakeInstance()
	c := newTestClient(t, fake)

	f, err := c.SetNovaflow(context.Background(), true, "2026.9.71")
	if err != nil {
		t.Fatal(err)
	}
	if fake.lastFlagsPut["controller_connector"] != true {
		t.Errorf("controller_connector was not sent back: %v", fake.lastFlagsPut)
	}
	if _, ok := fake.lastFlagsPut["novaflow_status"]; ok {
		t.Errorf("read-only novaflow_status was sent: %v", fake.lastFlagsPut)
	}
	if fake.lastFlagsPut["novaflow"] != true || fake.lastFlagsPut["novaflow_version"] != "2026.9.71" {
		t.Errorf("unexpected PUT body: %v", fake.lastFlagsPut)
	}
	if f.State() != StateDeployed || f.Status.DeployedVersion != "2026.9.71" {
		t.Errorf("unexpected answer: %+v", f.Status)
	}

	// "default" removes the selected version
	if _, err := c.SetNovaflow(context.Background(), true, DefaultVersion); err != nil {
		t.Fatal(err)
	}
	if _, ok := fake.lastFlagsPut["novaflow_version"]; ok {
		t.Errorf("novaflow_version must be removed: %v", fake.lastFlagsPut)
	}
	if _, err := c.SetNovaflow(context.Background(), true, "2026.9.71"); err != nil {
		t.Fatal(err)
	}

	// switching off keeps the selected version
	if _, err := c.SetNovaflow(context.Background(), false, ""); err != nil {
		t.Fatal(err)
	}
	if fake.lastFlagsPut["novaflow"] != false || fake.lastFlagsPut["novaflow_version"] != "2026.9.71" {
		t.Errorf("unexpected PUT body: %v", fake.lastFlagsPut)
	}
}

func TestUnauthorizedPageWithStatus200(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("Error 401: Unauthorized\nThe requested page needs a username and a password\n"))
	}))
	if _, err := c.ListFlows(context.Background()); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("want ErrUnauthorized, got %v", err)
	}
	if _, err := c.Inspect(context.Background()); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("Inspect: want ErrUnauthorized, got %v", err)
	}
}

func TestJSONErrorPageWithStatus200(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"error": true, "code": 401, "message": "Unauthorized",
			"details": map[string]any{"ingress_name": "api-gateway", "original_uri": r.URL.Path}})
	}))
	if _, err := c.FeatureFlags(context.Background()); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("an error page must not be read as feature flags, got %v", err)
	}
	if _, err := c.SetNovaflow(context.Background(), true, ""); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("want ErrUnauthorized, got %v", err)
	}
}

func TestLoginRedirect(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth2/start" {
			w.WriteHeader(406)
			return
		}
		http.Redirect(w, r, "/oauth2/start?rd=x", http.StatusFound)
	}))
	if err := c.Probe(context.Background()); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("want ErrUnauthorized, got %v", err)
	}
}

func TestNotInstalled(t *testing.T) {
	fake := newFakeInstance()
	fake.flows = nil // unused
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/cell/forest") {
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(404)
			return
		}
		fake.ServeHTTP(w, r)
	}))
	inst, err := c.Inspect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if inst.Installed() || inst.ViaFeatureFlag() || !errors.Is(inst.ProbeErr, ErrUnreachable) {
		t.Fatalf("unexpected installation: %+v", inst)
	}
}

func TestListFlowsPaginates(t *testing.T) {
	fake := newFakeInstance()
	for i := range 230 {
		id := "flow-" + strconv.Itoa(1000+i)
		fake.flows[id] = map[string]any{"id": id, "name": id, "created_at": "2026-11-01T10:00:00Z", "updated_at": "2026-11-01T10:00:00Z"}
	}
	fake.corrupted = []CorruptedFlow{{ID: "broken", Error: "bad json"}}
	c := newTestClient(t, fake)

	list, err := c.ListFlows(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Flows) != 230 {
		t.Errorf("got %d flows, want 230", len(list.Flows))
	}
	if len(list.Corrupted) != 1 {
		t.Errorf("corrupted flows reported per page must be merged: %v", list.Corrupted)
	}
}

func TestUpdateFlowRevisionCheck(t *testing.T) {
	fake := newFakeInstance()
	c := newTestClient(t, fake)
	ctx := context.Background()

	f := &Flow{ID: "a", Name: "A", Tree: json.RawMessage(`{"version":"3.0","root":{}}`)}
	if err := c.CreateFlow(ctx, f); err != nil {
		t.Fatal(err)
	}
	if err := c.CreateFlow(ctx, f); !errors.Is(err, ErrFlowExists) {
		t.Fatalf("want ErrFlowExists, got %v", err)
	}
	got, err := c.GetFlow(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.UpdateFlow(ctx, got, got.Revision); err != nil {
		t.Fatal(err)
	}
	// got.Revision is outdated now
	if err := c.UpdateFlow(ctx, got, got.Revision); !errors.Is(err, ErrFlowChanged) {
		t.Fatalf("want ErrFlowChanged, got %v", err)
	}
	if err := c.UpdateFlow(ctx, got, nil); err != nil {
		t.Fatalf("update without revision check: %v", err)
	}
}

func TestDeleteFlowRevisionCheck(t *testing.T) {
	fake := newFakeInstance()
	c := newTestClient(t, fake)
	ctx := context.Background()
	if err := c.CreateFlow(ctx, &Flow{ID: "a", Name: "A", Tree: json.RawMessage(`{"version":"3.0","root":{}}`)}); err != nil {
		t.Fatal(err)
	}
	stale := 0
	if err := c.DeleteFlow(ctx, "a", &stale); !errors.Is(err, ErrFlowChanged) {
		t.Fatalf("want ErrFlowChanged, got %v", err)
	}
	rev := 1
	if err := c.DeleteFlow(ctx, "a", &rev); err != nil {
		t.Fatal(err)
	}
	if _, ok := fake.flows["a"]; ok {
		t.Fatal("flow not deleted")
	}
	if err := c.DeleteFlow(ctx, "a", nil); !IsStatus(err, http.StatusNotFound) {
		t.Fatalf("want 404, got %v", err)
	}
}

func TestObjectKeysKeepSlashes(t *testing.T) {
	fake := newFakeInstance()
	c := newTestClient(t, fake)
	if err := c.PutObject(context.Background(), "maps/hall 1.json", []byte("x")); err != nil {
		t.Fatal(err)
	}
	last := fake.paths[len(fake.paths)-1]
	if last != "/api/v2/cells/cell/store/objects/maps/hall%201.json" {
		t.Errorf("unexpected path %s", last)
	}
	if string(fake.objects["maps/hall 1.json"]) != "x" {
		t.Errorf("object not stored: %v", fake.objects)
	}
}

func TestBackupRestoreRoundTrip(t *testing.T) {
	ctx := context.Background()
	src := newFakeInstance()
	src.flags = map[string]any{"novaflow": true, "novaflow_status": map[string]any{"state": "deployed", "enabled": true, "deployed_version": "2026.9.71"}}
	src.flows["pick"] = map[string]any{"id": "pick", "name": "Pick", "tree": map[string]any{"version": "3.0", "root": map[string]any{"type": "sequence"}},
		"orphans": []any{map[string]any{"x": 1}}, "layouts": map[string]any{"canvas": map[string]any{}}, "metadata": nil,
		"revision": 7, "created_at": "2026-11-01T10:00:00Z", "updated_at": "2026-11-02T10:00:00Z"}
	src.flows["place"] = map[string]any{"id": "place", "name": "Place", "tree": map[string]any{"version": "3.0", "root": map[string]any{}},
		"revision": 2, "created_at": "2026-11-01T10:00:00Z", "updated_at": "2026-11-01T10:00:00Z"}
	src.corrupted = []CorruptedFlow{{ID: "broken", Error: "bad json"}}
	src.globals["home"] = map[string]any{"type": "Pose", "value": map[string]any{"position": []any{1, 2, 3}}}
	src.objects["maps/hall.json"] = []byte(`{"a":1}`)
	src.objects["other-app/data.bin"] = []byte{0, 1, 2}
	src.objects["trees/pick"] = []byte(`{"id":"pick"}`) // Forest's own storage

	var buf bytes.Buffer
	m, err := newTestClient(t, src).Backup(ctx, &buf, BackupOptions{IncludeObjects: true})
	if !errors.Is(err, ErrCorruptedFlows) || m == nil {
		t.Fatalf("want ErrCorruptedFlows with a manifest, got %v", err)
	}
	if m.NovaflowVersion != "2026.9.71" || len(m.Flows) != 2 || m.Globals != 1 || len(m.Objects) != 2 || len(m.Corrupted) != 1 {
		t.Fatalf("unexpected manifest: %+v", m)
	}

	file := filepath.Join(t.TempDir(), "b.tar.gz")
	if err := os.WriteFile(file, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	bc, err := ReadBackup(file)
	if err != nil {
		t.Fatal(err)
	}

	dst := newFakeInstance()
	dst.flows["place"] = map[string]any{"id": "place", "name": "Changed", "tree": map[string]any{"version": "3.0", "root": map[string]any{}},
		"revision": 5, "created_at": "2026-11-01T10:00:00Z", "updated_at": "2026-11-03T10:00:00Z"}
	dc := newTestClient(t, dst)

	items, err := dc.Restore(ctx, bc, RestoreOptions{IncludeObjects: true, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(dst.globals) != 0 || len(dst.flows) != 1 || len(dst.objects) != 0 {
		t.Fatal("dry run changed the instance")
	}
	want := map[string]RestoreAction{"global/home": Created, "flow/pick": Created, "flow/place": Skipped,
		"object/maps/hall.json": Created, "object/other-app/data.bin": Created}
	if got := actions(items); !equalActions(got, want) {
		t.Fatalf("dry run plan %v, want %v", got, want)
	}

	if _, err := dc.Restore(ctx, bc, RestoreOptions{IncludeObjects: true}); err != nil {
		t.Fatal(err)
	}
	if dst.flows["place"]["name"] != "Changed" {
		t.Error("existing flow was replaced without --overwrite")
	}
	pick := dst.flows["pick"]
	if pick["name"] != "Pick" || pick["orphans"] == nil || pick["layouts"] == nil {
		t.Errorf("restored flow lost data: %v", pick)
	}
	if dst.globals["home"]["type"] != "Pose" {
		t.Errorf("global not restored: %v", dst.globals)
	}
	if string(dst.objects["maps/hall.json"]) != `{"a":1}` {
		t.Errorf("object not restored: %v", dst.objects)
	}

	items, err = dc.Restore(ctx, bc, RestoreOptions{Overwrite: true, Only: []string{"place"}, SkipGlobals: true})
	if err != nil {
		t.Fatal(err)
	}
	if dst.flows["place"]["name"] != "Place" {
		t.Error("--overwrite did not replace the flow")
	}
	if got := actions(items); !equalActions(got, map[string]RestoreAction{"flow/place": Replaced}) {
		t.Errorf("unexpected items %v", got)
	}

	if _, err := dc.Restore(ctx, bc, RestoreOptions{Only: []string{"nope"}}); err == nil {
		t.Error("restoring an unknown flow id must fail")
	}
}

func TestBackupObjectPrefix(t *testing.T) {
	src := newFakeInstance()
	src.objects["maps/hall.json"] = []byte("a")
	src.objects["other/x"] = []byte("b")
	src.objects["trees/t"] = []byte("c")
	var buf bytes.Buffer
	m, err := newTestClient(t, src).Backup(context.Background(), &buf, BackupOptions{IncludeObjects: true, ObjectPrefix: "maps/"})
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Objects) != 1 || m.Objects[0] != "maps/hall.json" {
		t.Errorf("unexpected objects %v", m.Objects)
	}
}

func actions(items []RestoreItem) map[string]RestoreAction {
	m := map[string]RestoreAction{}
	for _, it := range items {
		m[it.Kind+"/"+it.Key] = it.Action
	}
	return m
}

func equalActions(a, b map[string]RestoreAction) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func TestWaitForDeployment(t *testing.T) {
	fake := newFakeInstance()
	calls := 0
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/internal/cells/cell/feature-flags" {
			calls++
			state := map[string]any{"state": "updating"}
			if calls > 2 {
				state = map[string]any{"state": "deployed", "enabled": true, "deployed_version": "1.2.3"}
			}
			writeJSON(w, 200, map[string]any{"novaflow": true, "novaflow_status": state})
			return
		}
		fake.ServeHTTP(w, r)
	}))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var states []string
	err := c.WaitForDeployment(ctx, WaitOptions{Enabled: true, Version: "1.2.3", Interval: time.Millisecond, Progress: func(s string) { states = append(states, s) }})
	if err != nil {
		t.Fatal(err)
	}
	if len(states) != 1 || states[0] != "being deployed" {
		t.Errorf("unexpected progress %v", states)
	}

	failing := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"novaflow": true, "novaflow_status": map[string]any{"state": "failed", "reason": "chart not found"}})
	}))
	if err := failing.WaitForDeployment(ctx, WaitOptions{Enabled: true, Interval: time.Millisecond}); err == nil || !strings.Contains(err.Error(), "chart not found") {
		t.Errorf("want the failure reason, got %v", err)
	}
}

func TestWaitForRemovalWaitsUntilForestIsGone(t *testing.T) {
	probes := 0
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/internal/cells/cell/feature-flags" {
			writeJSON(w, 200, map[string]any{"novaflow": false, "novaflow_status": map[string]any{"state": "disabled", "enabled": false}})
			return
		}
		// Forest keeps answering for a moment after the release reports disabled
		probes++
		if probes <= 2 {
			writeJSON(w, 200, map[string]any{"items": []any{}, "total": 0})
			return
		}
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(404)
	}))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var states []string
	if err := c.WaitForDeployment(ctx, WaitOptions{Enabled: false, Interval: time.Millisecond, Progress: func(s string) { states = append(states, s) }}); err != nil {
		t.Fatal(err)
	}
	if probes != 3 {
		t.Errorf("returned after %d probes, want 3 (until Forest is gone)", probes)
	}
	if len(states) != 1 || states[0] != "being removed" {
		t.Errorf("unexpected progress %v", states)
	}
}
