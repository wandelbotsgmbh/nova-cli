package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wandelbotsgmbh/nova-cli/pkg/debug"
)

const fakeSessionID = "66416162-239c-4aa0-b212-02b1323102cf"

// fakeBroker answers the streaming session broker's API with fixed data. No
// real session is created.
func fakeBroker(t *testing.T, emptyLists bool) *httptest.Server {
	t.Helper()
	lease := map[string]any{
		"id": fakeSessionID, "name": "my-session", "status": "running", "configName": "default",
		"userId": "user-1", "createdAt": "2026-11-11T10:00:00Z", "startedAt": "2026-11-11T10:00:00Z",
		"expiresAt": "2026-11-11T12:00:00Z",
	}
	session := map[string]any{}
	for k, v := range lease {
		session[k] = v
	}
	session["ready"] = true
	session["condition"] = "ready"
	session["routes"] = map[string]any{"stream.example.com": map[string]any{"routes": []any{
		map[string]any{"description": "signaling", "protocol": "TCP", "sourcePort": 49100, "destinationPort": 49100},
	}}}
	extended := map[string]any{}
	for k, v := range lease {
		extended[k] = v
	}
	extended["expiresAt"] = "2026-11-11T14:00:00Z"

	list := func(items ...any) map[string]any {
		if emptyLists {
			items = []any{}
		}
		return map[string]any{"value": items}
	}
	answers := map[string]any{
		"GET /sessions":                                list(lease),
		"GET /sessions/" + fakeSessionID:               session,
		"POST /sessions":                               session,
		"POST /sessions/" + fakeSessionID + "/extend":  extended,
		"POST /sessions/" + fakeSessionID + "/suspend": lease,
		"POST /sessions/" + fakeSessionID + "/resume":  lease,
		"PATCH /sessions/" + fakeSessionID:             lease,
		"GET /applications": list(map[string]any{"id": "nova-x-isaac", "name": "Isaac Sim", "description": "NOVA x Isaac Sim",
			"versions": []any{map[string]any{"version": "1.0.0", "profiles": []any{
				map[string]any{"id": "isaac-24gb", "name": "24GB", "description": "MIG slice"}}}}}),
		"GET /capacity/isaac-24gb":                     map[string]any{"profile": "isaac-24gb", "gpu": "24GB", "free": 3},
		"GET /capacity/cpu":                            map[string]any{"profile": "cpu"},
		"GET /configs":                                 list(map[string]any{"app": "nova-x-isaac", "name": "default"}),
		"GET /configs/nova-x-isaac/default":            map[string]any{"stage": "omniverse://x/y.usd"},
		"DELETE /sessions/" + fakeSessionID:            nil,
		"POST /sessions/" + fakeSessionID + "/restart": nil,
	}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-access-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		a, ok := answers[r.Method+" "+r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		if a == nil {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost && r.URL.Path == "/sessions" {
			w.WriteHeader(http.StatusCreated)
		}
		_ = json.NewEncoder(w).Encode(a)
	}))
	t.Cleanup(s.Close)
	return s
}

// withFakeBroker logs in to a fake broker with a token that doesn't need a refresh.
func withFakeBroker(t *testing.T, emptyLists bool) {
	t.Helper()
	s := fakeBroker(t, emptyLists)
	withConfig(t, `simulation-broker: `+s.URL+`
simulation-token:
  environment: dev
  oauth2token:
    accesstoken: test-access-token
    tokentype: Bearer
    refreshtoken: test-refresh-token
    expiry: `+time.Now().Add(time.Hour).UTC().Format(time.RFC3339)+`
`)
	t.Setenv(debug.EnvVar, "")
}

// ADR-0010: -o json on the simulation commands prints CLI-defined shapes in
// snake_case (sessions; apps, configs and capacity have one-word fields) on stdout
// and nothing on stderr. config get prints the config document as stored.
func TestSimulationCommandsJSON(t *testing.T) {
	leaseJSON := `{"id":"` + fakeSessionID + `","name":"my-session","status":"running","config_name":"default","user_id":"user-1",` +
		`"created_at":"2026-11-11T10:00:00Z","started_at":"2026-11-11T10:00:00Z","expires_at":"2026-11-11T12:00:00Z"}`
	sessionJSON := `{"id":"` + fakeSessionID + `","name":"my-session","status":"running","condition":"ready","ready":true,` +
		`"config_name":"default","user_id":"user-1",` +
		`"created_at":"2026-11-11T10:00:00Z","started_at":"2026-11-11T10:00:00Z","expires_at":"2026-11-11T12:00:00Z",` +
		`"routes":[{"host":"stream.example.com","description":"signaling","protocol":"TCP","source_port":49100,"destination_port":49100}]}`
	for _, tc := range []struct {
		args  []string
		empty bool
		want  string
	}{
		{args: []string{"simulation", "session", "list", "-o", "json"}, want: "[" + leaseJSON + "]"},
		{args: []string{"simulation", "session", "list", "-o", "json"}, empty: true, want: `[]`},
		{args: []string{"simulation", "session", "get", "my-session", "-o", "json"}, want: sessionJSON},
		{args: []string{"simulation", "session", "create", "my-session", "--app", "nova-x-isaac", "--profile", "isaac-24gb", "-o", "json"}, want: sessionJSON},
		{args: []string{"simulation", "session", "extend", fakeSessionID, "--minutes", "120", "-o", "json"},
			want: strings.Replace(leaseJSON, "12:00:00Z", "14:00:00Z", 1)},
		{args: []string{"simulation", "session", "rename", fakeSessionID, "my-session", "-o", "json"}, want: leaseJSON},
		{args: []string{"simulation", "session", "suspend", fakeSessionID, "-o", "json"}, want: leaseJSON},
		{args: []string{"simulation", "session", "resume", fakeSessionID, "-o", "json"}, want: leaseJSON},
		{args: []string{"simulation", "apps", "-o", "json"},
			want: `[{"description":"NOVA x Isaac Sim","id":"nova-x-isaac","name":"Isaac Sim","versions":[{"profiles":[{"description":"MIG slice","id":"isaac-24gb","name":"24GB"}],"version":"1.0.0"}]}]`},
		{args: []string{"simulation", "apps", "-o", "json"}, empty: true, want: `[]`},
		{args: []string{"simulation", "capacity", "isaac-24gb", "-o", "json"}, want: `{"profile":"isaac-24gb","gpu":"24GB","free":3}`},
		// no GPU, no quota: the fields stay, as null
		{args: []string{"simulation", "capacity", "cpu", "-o", "json"}, want: `{"profile":"cpu","gpu":null,"free":null}`},
		{args: []string{"simulation", "config", "list", "-o", "json"}, want: `[{"app":"nova-x-isaac","name":"default"}]`},
		{args: []string{"simulation", "config", "list", "-o", "json"}, empty: true, want: `[]`},
		{args: []string{"simulation", "config", "get", "nova-x-isaac/default", "-o", "json"}, want: `{"stage":"omniverse://x/y.usd"}`},
	} {
		t.Run(strings.Join(tc.args[1:], " "), func(t *testing.T) {
			withFakeBroker(t, tc.empty)
			stdout, stderr, err := runAsProcess(t, tc.args...)
			if err != nil {
				t.Fatalf("error: %v\nstderr: %s", err, stderr)
			}
			if !json.Valid([]byte(stdout)) {
				t.Fatalf("stdout is no JSON: %q", stdout)
			}
			if got := compactJSON(t, stdout); got != tc.want {
				t.Errorf("stdout = %s\nwant     %s", got, tc.want)
			}
			if stderr != "" {
				t.Errorf("stderr = %q, want nothing", stderr)
			}
			if strings.Contains(stdout, "test-access-token") || strings.Contains(stdout, "test-refresh-token") {
				t.Error("stdout carries a token")
			}
		})
	}
}

// The text output stays as it was.
func TestSimulationCommandsText(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"simulation", "capacity", "isaac-24gb"}, "profile: isaac-24gb\ngpu: 24GB\nfree: 3\n"},
		{[]string{"simulation", "capacity", "cpu"}, "profile: cpu\ngpu: -\nfree: unlimited\n"},
		{[]string{"simulation", "config", "list"}, "nova-x-isaac/default\n"},
		{[]string{"simulation", "config", "get", "nova-x-isaac/default"}, "{\n  \"stage\": \"omniverse://x/y.usd\"\n}\n\n"}, // the broker's trailing newline, as before
		{[]string{"simulation", "apps"}, "APP           VERSION  PROFILE     DESCRIPTION\nnova-x-isaac  1.0.0    isaac-24gb  24GB\n"},
		{[]string{"simulation", "session", "delete", fakeSessionID}, ""},
	} {
		t.Run(strings.Join(tc.args[1:], " "), func(t *testing.T) {
			withFakeBroker(t, false)
			stdout, stderr, err := runAsProcess(t, tc.args...)
			if err != nil {
				t.Fatalf("error: %v\nstderr: %s", err, stderr)
			}
			if stdout != tc.want {
				t.Errorf("stdout = %q, want %q", stdout, tc.want)
			}
		})
	}
}

// delete and restart return nothing (204), so they have no -o json; the api
// commands use -o for a file.
func TestSimulationOutputFlagOnlyWhereThereIsData(t *testing.T) {
	for _, args := range [][]string{
		{"simulation", "session", "delete"},
		{"simulation", "session", "restart"},
	} {
		c, _, err := rootCmd.Find(args)
		if err != nil {
			t.Fatal(err)
		}
		if c.Flags().Lookup("output") != nil {
			t.Errorf("%s has --output", strings.Join(args, " "))
		}
	}
	for _, args := range [][]string{{"simulation", "api"}, {"simulation", "session", "api"}} {
		c, _, err := rootCmd.Find(args)
		if err != nil {
			t.Fatal(err)
		}
		if f := c.Flags().Lookup("output"); f == nil || f.Value.Type() != "string" {
			t.Errorf("%s: -o should name a file", strings.Join(args, " "))
		}
	}
}
