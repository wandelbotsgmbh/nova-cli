package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/wandelbotsgmbh/nova-cli/pkg/novaflow"
)

const fakeFlow = `{"id":"f1","name":"Flow","tree":{"version":"3.0","root":{}},"revision":3}`

// fakeForest serves one flow (id f1, revision 3) and the control plane's
// validation, which rejects trees containing "bad".
type fakeForest struct {
	conflict       bool
	validateBroken bool
	puts           []map[string]any
	ifMatch        []string
}

func (ff *fakeForest) client(t *testing.T) *novaflow.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/cell/forest/trees/f1":
			io.WriteString(w, fakeFlow)
		case r.Method == http.MethodPut && r.URL.Path == "/cell/forest/trees/f1":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			ff.puts = append(ff.puts, body)
			ff.ifMatch = append(ff.ifMatch, r.Header.Get("If-Match"))
			if ff.conflict {
				w.WriteHeader(http.StatusPreconditionFailed)
				io.WriteString(w, `{"detail":"revision mismatch"}`)
				return
			}
			io.WriteString(w, `{}`)
		case r.Method == http.MethodPost && r.URL.Path == "/cell/controlplane/api/v1/validation/tree":
			body, _ := io.ReadAll(r.Body)
			switch {
			case ff.validateBroken:
				w.WriteHeader(http.StatusInternalServerError)
				io.WriteString(w, `{"detail":"boom"}`)
			case strings.Contains(string(body), "unreadable"):
				w.WriteHeader(http.StatusUnprocessableEntity)
				io.WriteString(w, `{"detail":"root: field required"}`)
			case strings.Contains(string(body), "bad"):
				io.WriteString(w, `{"valid":false,"errors":[{"code":"E1","message":"unknown action bad"}],"warnings":[]}`)
			default:
				io.WriteString(w, `{"valid":true,"errors":[],"warnings":[]}`)
			}
		default:
			w.WriteHeader(http.StatusNotFound)
			io.WriteString(w, `{"detail":"not found"}`)
		}
	}))
	t.Cleanup(srv.Close)
	c, err := novaflow.NewClient(srv.URL, "cell")
	if err != nil {
		t.Fatal(err)
	}
	return c
}

type editStep func(shown string) (string, error)

func set(f func(string) string) editStep {
	return func(s string) (string, error) { return f(s), nil }
}

var errEditor = errors.New("exit status 1")

func failEditor(string) (string, error) { return "", errEditor }

// session drives editFlow with scripted editor rounds; questions get answer.
type session struct {
	shown []string
	asked int
	msgs  strings.Builder
}

func (s *session) run(t *testing.T, ff *fakeForest, validate, answer bool, steps ...editStep) error {
	t.Helper()
	return editFlow(context.Background(), ff.client(t), "f1", validate, flowEditor{
		edit: func(content []byte) ([]byte, error) {
			s.shown = append(s.shown, string(content))
			if len(s.shown) > len(steps) {
				t.Fatalf("editor opened %d times", len(s.shown))
			}
			out, err := steps[len(s.shown)-1](string(content))
			return []byte(out), err
		},
		ask: func(string) bool { s.asked++; return answer },
		msg: &s.msgs,
	})
}

func unchanged(s string) string { return s }
func rename(s string) string    { return strings.Replace(s, `"Flow"`, `"Renamed"`, 1) }
func breakTree(s string) string {
	return strings.Replace(rename(s), `"root": {}`, `"root": {"action": "bad"}`, 1)
}

// keptFile returns the content of the file the error says the changes are in.
func keptFile(t *testing.T, err error) (path, content string) {
	t.Helper()
	if err == nil {
		t.Fatal("no error")
	}
	m := regexp.MustCompile(`your changes are in (\S+\.json)`).FindStringSubmatch(err.Error())
	if m == nil {
		t.Fatalf("error doesn't name the kept file: %v", err)
	}
	t.Cleanup(func() { os.Remove(m[1]) })
	data, rerr := os.ReadFile(m[1])
	if rerr != nil {
		t.Fatal(rerr)
	}
	return m[1], string(data)
}

func TestEditFlowUnchanged(t *testing.T) {
	reformat := func(s string) string { // e.g. format on save
		var out bytes.Buffer
		_ = json.Indent(&out, []byte(s), "", "\t")
		return out.String() + "\r\n"
	}
	for name, step := range map[string]editStep{"unchanged": set(unchanged), "reformatted": set(reformat), "emptied": set(func(string) string { return "\n" })} {
		t.Run(name, func(t *testing.T) {
			ff := &fakeForest{}
			var s session
			if err := s.run(t, ff, true, true, step); err != nil {
				t.Fatal(err)
			}
			if len(ff.puts) != 0 {
				t.Errorf("saved: %v", ff.puts)
			}
		})
	}
}

func TestEditFlowSavesAtReadRevision(t *testing.T) {
	ff := &fakeForest{}
	var s session
	// the revision in the file doesn't matter, the one read does
	err := s.run(t, ff, true, true, set(func(s string) string {
		return strings.Replace(rename(s), `"revision": 3`, `"revision": 9`, 1)
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s.shown[0], "\n  \"name\": \"Flow\"") {
		t.Errorf("editor didn't get indented JSON: %s", s.shown[0])
	}
	if len(ff.puts) != 1 || ff.puts[0]["name"] != "Renamed" || ff.ifMatch[0] != `"3"` {
		t.Errorf("puts %v, If-Match %v", ff.puts, ff.ifMatch)
	}
}

func TestEditFlowNotFound(t *testing.T) {
	ff := &fakeForest{}
	err := editFlow(context.Background(), ff.client(t), "nope", true, flowEditor{})
	if err == nil || !strings.Contains(err.Error(), "not found on cell") {
		t.Fatalf("err %v", err)
	}
}

func TestEditFlowEditAgain(t *testing.T) {
	cases := map[string]editStep{
		"invalid JSON": set(func(s string) string { return rename(s)[:len(s)-3] }),
		"invalid tree": set(breakTree),
		"unreadable tree": set(func(s string) string {
			return strings.Replace(rename(s), `"root": {}`, `"unreadable": {}`, 1)
		}),
	}
	for name, first := range cases {
		t.Run(name, func(t *testing.T) {
			ff := &fakeForest{}
			var s session
			err := s.run(t, ff, true, true, first, func(shown string) (string, error) {
				if !strings.Contains(shown, "Renamed") {
					t.Error("second round doesn't show the user's changes")
				}
				fixed, _ := indentJSON([]byte(fakeFlow))
				return rename(string(fixed)), nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if s.asked != 1 || len(ff.puts) != 1 || ff.puts[0]["name"] != "Renamed" {
				t.Errorf("asked %d, puts %v", s.asked, ff.puts)
			}
		})
	}
}

func TestEditFlowFieldProblems(t *testing.T) {
	cases := map[string]struct {
		edit func(string) string
		want string
	}{
		"id changed":   {func(s string) string { return strings.Replace(s, `"f1"`, `"f2"`, 1) }, "can't be changed"},
		"id removed":   {func(s string) string { return strings.Replace(s, `"id": "f1",`, ``, 1) }, `"id" is missing`},
		"name removed": {func(s string) string { return strings.Replace(s, `"name": "Flow",`, ``, 1) }, `"name" is missing`},
		"bare tree":    {func(string) string { return `{"version": "3.0", "root": {}}` }, `"id" is missing`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			ff := &fakeForest{}
			var s session
			err := s.run(t, ff, true, false, set(tc.edit))
			if len(ff.puts) != 0 || s.asked != 1 {
				t.Fatalf("puts %v, asked %d", ff.puts, s.asked)
			}
			if !strings.Contains(s.msgs.String(), tc.want) || strings.Contains(s.msgs.String(), "--id\n") {
				t.Errorf("message %q, want %q", s.msgs.String(), tc.want)
			}
			if _, content := keptFile(t, err); content != tc.edit(s.shown[0]) {
				t.Error("kept file lacks the edit")
			}
		})
	}
}

func TestEditFlowInvalidTree(t *testing.T) {
	ff := &fakeForest{}
	var s session
	err := s.run(t, ff, true, false, set(breakTree))
	if s.asked != 1 || len(ff.puts) != 0 {
		t.Fatalf("asked %d, puts %v", s.asked, ff.puts)
	}
	keptFile(t, err)

	// --no-validate saves it
	s = session{}
	if err := s.run(t, ff, false, false, set(breakTree)); err != nil || len(ff.puts) != 1 {
		t.Fatalf("err %v, puts %v", err, ff.puts)
	}
}

func TestEditFlowStopsWhenClosedUnfixed(t *testing.T) {
	ff := &fakeForest{}
	var s session
	err := s.run(t, ff, true, true, set(breakTree), set(unchanged))
	if s.asked != 1 || len(ff.puts) != 0 {
		t.Fatalf("asked %d, puts %v", s.asked, ff.puts)
	}
	keptFile(t, err)
}

func TestEditFlowEmptiedAfterProblemCancels(t *testing.T) {
	ff := &fakeForest{}
	var s session
	if err := s.run(t, ff, true, true, set(breakTree), set(func(string) string { return "" })); err != nil {
		t.Fatal(err)
	}
	if len(ff.puts) != 0 {
		t.Errorf("saved: %v", ff.puts)
	}
}

func TestEditFlowEditorFails(t *testing.T) {
	ff := &fakeForest{}
	var s session
	err := s.run(t, ff, true, true, failEditor)
	if !errors.Is(err, errEditor) || strings.Contains(err.Error(), "your changes") {
		t.Errorf("first round: %v", err)
	}

	// later rounds: the changes of the rounds before are kept
	s = session{}
	err = s.run(t, ff, true, true, set(breakTree), failEditor)
	if !errors.Is(err, errEditor) {
		t.Errorf("err %v", err)
	}
	if _, content := keptFile(t, err); !strings.Contains(content, "Renamed") {
		t.Error("kept file lacks the first round's changes")
	}
}

func TestEditFlowValidationFails(t *testing.T) {
	ff := &fakeForest{validateBroken: true}
	var s session
	err := s.run(t, ff, true, true, set(rename))
	if err == nil || !strings.Contains(err.Error(), "validating") || len(ff.puts) != 0 {
		t.Fatalf("err %v, puts %v", err, ff.puts)
	}
	keptFile(t, err)
}

func TestEditFlowConflictKeepsChanges(t *testing.T) {
	ff := &fakeForest{conflict: true}
	var s session
	err := s.run(t, ff, true, true, set(rename))
	if !errors.Is(err, novaflow.ErrFlowChanged) {
		t.Fatalf("err %v", err)
	}
	path, content := keptFile(t, err)
	if !strings.Contains(content, "Renamed") {
		t.Error("kept file lacks the edit")
	}
	// the file still has the old revision: only --force can save it
	if !strings.Contains(err.Error(), "nova flow push "+path+" --force") {
		t.Errorf("hint doesn't name the kept file with --force: %v", err)
	}
}
