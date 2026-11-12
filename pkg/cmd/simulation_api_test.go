package cmd

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/spf13/cobra"
	"github.com/wandelbotsgmbh/nova-cli/pkg/novaapi"
)

// Request flags and paths are checked before the broker is asked (no login
// needed for these errors).
func TestSimulationSessionAPIArgs(t *testing.T) {
	withConfig(t, "")
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"simulation", "session", "api", "s", "omniservice", "-X", "POST"}, "--method needs a path"},
		{[]string{"simulation", "session", "api", "s", "-f", "a=b"}, "--raw-field needs a path"},
		{[]string{"simulation", "session", "api", "s", "omniservice", "status"}, "must start with a single '/'"},
		{[]string{"simulation", "session", "api", "s", "omniservice", "/status", "--spec"}, "don't go together"},
		{[]string{"simulation", "session", "api", "s", "omniservice", "/status", "-H", "bad"}, "expected 'Key: value'"},
		{[]string{"simulation", "session", "api", "s", "omniservice", "--timeout", "5s"}, "--timeout needs a path"},
		{[]string{"simulation", "session", "api", "s", "omniservice", "/../../stream/x"}, "leaves the api's base URL"},
		{[]string{"simulation", "session", "api", "s", "omniservice", "/a/%2e%2e/b"}, "leaves the api's base URL"},
		{[]string{"simulation", "session", "api", "s", "omniservice", "/..#x"}, "has a fragment"},
	} {
		_, _, err := runWithConfig(t, tc.args...)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%v: err %v, want %q", tc.args, err, tc.want)
		}
	}
}

// The session api request goes through the same code as nova api: fields,
// bearer token, answer on stdout, HTTP errors with the login hint.
func TestSessionAPIRequest(t *testing.T) {
	var mu sync.Mutex
	var got []string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		got = append(got, r.Method+" "+r.URL.RequestURI()+" "+r.Header.Get("Authorization")+" "+string(body))
		mu.Unlock()
		switch r.URL.Path {
		case "/omniservice/api/v2/expired":
			_, _ = io.WriteString(w, "Error 401: Unauthorized")
			return
		case "/omniservice/api/v2/elsewhere":
			http.Redirect(w, r, "https://other.example/x", http.StatusTemporaryRedirect)
			return
		}
		if r.URL.Path == "/omniservice/api/v2/secret" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"detail":"bad token"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer s.Close()
	client, err := novaapi.NewBearerClient(s.URL+"/omniservice/api/v2", "tok")
	if err != nil {
		t.Fatal(err)
	}

	run := func(f *requestFlags, path string) (string, error) {
		var out strings.Builder
		cmd := &cobra.Command{}
		cmd.SetOut(&out)
		cmd.SetErr(io.Discard)
		p, err := prepareRequest(f, path)
		if err != nil {
			return "", err
		}
		hint := "run 'nova simulation login'"
		err = p.send(cmd, client, nil, f, apiLogin{hint: hint, on401: hint})
		return out.String(), err
	}

	f := &requestFlags{fields: []fieldArg{{value: "enabled=true", typed: true}}}
	out, err := run(f, "/toggle")
	if err != nil || out != `{"ok":true}` {
		t.Fatalf("out %q, err %v", out, err)
	}
	if len(got) != 1 || got[0] != `POST /omniservice/api/v2/toggle Bearer tok {"enabled":true}` {
		t.Errorf("requests %q", got)
	}

	out, err = run(&requestFlags{}, "/secret")
	if err == nil || !strings.Contains(err.Error(), "HTTP 401: bad token (run 'nova simulation login')") {
		t.Errorf("err %v", err)
	}
	if out != `{"detail":"bad token"}` {
		t.Errorf("out %q", out)
	}

	out, err = run(&requestFlags{dryRun: true, method: "delete"}, "/x")
	if err != nil || !strings.Contains(out, `"method": "DELETE"`) || !strings.Contains(out, `"auth": "bearer"`) || len(got) != 2 {
		t.Errorf("dry run: out %q, err %v, requests %d", out, err, len(got))
	}

	// the ingress' 401 page: typed error without the instance login's wording
	_, err = run(&requestFlags{}, "/expired")
	var typed *typedError
	if !errors.As(err, &typed) || typed.Code != "UNAUTHORIZED" || strings.Contains(typed.Message, "nova login") ||
		typed.Hint != "run 'nova simulation login'" {
		t.Errorf("err %#v", err)
	}

	// a redirect to another host is not followed (the token stays here)
	_, err = run(&requestFlags{}, "/elsewhere")
	if err == nil || !strings.Contains(err.Error(), "not followed") {
		t.Errorf("redirect: err %v", err)
	}
}
