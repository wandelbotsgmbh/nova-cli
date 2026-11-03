package cmd

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// recorder is a fake instance that records the paths it was asked for.
type recorder struct {
	mu    sync.Mutex
	paths []string
}

func (r *recorder) server(t *testing.T) *httptest.Server {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		r.paths = append(r.paths, req.URL.Path)
		r.mu.Unlock()
		http.NotFound(w, req)
	}))
	t.Cleanup(s.Close)
	return s
}

func (r *recorder) requested() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.paths...)
}

// nova flow and nova app update-env read the instance from the resolved target, not
// from the legacy top-level host.
func TestFlowUsesTargetedInstance(t *testing.T) {
	var selected, other recorder
	a, b := selected.server(t), other.server(t)
	withConfig(t, `current-instance: a
instances:
  - name: a
    kind: host
    host: `+a.URL+`
    cellname: cell-a
  - name: b
    kind: host
    host: `+b.URL+`
    cellname: cell-b
`)

	_, _, _ = runWithConfig(t, "--instance", "b", "flow", "list")

	if got := selected.requested(); len(got) != 0 {
		t.Errorf("the selected instance must not be asked with --instance b, got %v", got)
	}
	got := other.requested()
	if len(got) == 0 {
		t.Fatal("instance b was not asked")
	}
	for _, p := range got {
		if !strings.Contains(p, "cell-b") {
			t.Errorf("request %q is not for the cell of instance b", p)
		}
	}
}
