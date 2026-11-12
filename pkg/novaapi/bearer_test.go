package novaapi

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// A bearer client appends request paths to its base path and sends its token,
// never the instance login, and only to its own host.
func TestBearerClient(t *testing.T) {
	var gotPath, gotAuth string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth = r.URL.RequestURI(), r.Header.Get("Authorization")
		_, _ = io.WriteString(w, "ok")
	}))
	defer s.Close()

	c, err := NewBearerClient(s.URL+"/omniservice/api/v2/", "tok")
	if err != nil {
		t.Fatal(err)
	}
	req, err := c.NewRequest(context.Background(), http.MethodGet, "/status?x=1", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if gotPath != "/omniservice/api/v2/status?x=1" || gotAuth != "Bearer tok" {
		t.Errorf("path %q, auth %q", gotPath, gotAuth)
	}

	for _, p := range []string{"//other.example/x", "status", "@other.example/x"} {
		if _, err := c.NewRequest(context.Background(), http.MethodGet, p, nil); err == nil {
			t.Errorf("%q: no error", p)
		}
	}
}
