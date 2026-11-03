package cmd

import (
	"strings"
	"testing"
)

func TestParseFlowFile(t *testing.T) {
	f, err := parseFlowFile([]byte(`{"id":"a","name":"A","tree":{"version":"3.0","root":{}},"revision":4}`), "", "")
	if err != nil {
		t.Fatal(err)
	}
	if f.ID != "a" || f.Name != "A" || f.Revision == nil || *f.Revision != 4 {
		t.Errorf("unexpected flow %+v", f)
	}

	f, err = parseFlowFile([]byte(`{"version":"3.0","root":{}}`), "b", "")
	if err != nil {
		t.Fatal(err)
	}
	if f.ID != "b" || f.Name != "b" || f.Revision != nil || !strings.Contains(string(f.Tree), `"root"`) {
		t.Errorf("bare tree not wrapped: %+v", f)
	}

	if _, err := parseFlowFile([]byte(`{"version":"3.0","root":{}}`), "", ""); err == nil {
		t.Error("a bare tree needs --id")
	}
	if _, err := parseFlowFile([]byte(`{"foo":1}`), "x", ""); err == nil {
		t.Error("unknown content must be rejected")
	}
}
