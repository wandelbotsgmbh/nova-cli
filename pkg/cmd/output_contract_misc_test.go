package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wandelbotsgmbh/nova-cli/pkg/debug"
)

// fakeAPI answers the given paths with the given bodies (JSON unless the body is
// binary), everything else with 404.
func fakeAPI(t *testing.T, answers map[string]string) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a, ok := answers[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		if json.Valid([]byte(a)) {
			w.Header().Set("Content-Type", "application/json")
		} else {
			w.Header().Set("Content-Type", "application/octet-stream")
		}
		if r.URL.Path == "/api/v2/system/configuration" {
			w.Header().Set("X-Backup-Id", "0b6c1f4e-5f0a-4c55-9a51-2f1f0c6f3e11")
		}
		_, _ = w.Write([]byte(a))
	}))
	t.Cleanup(s.Close)
	return s
}

// withFakeAPI selects an instance answered by fakeAPI.
func withFakeAPI(t *testing.T, answers map[string]string) {
	t.Helper()
	s := fakeAPI(t, answers)
	withConfig(t, `current-instance: lab
instances:
  - name: lab
    kind: host
    host: `+s.URL+`
`)
	t.Setenv(debug.EnvVar, "")
	t.Cleanup(func() { debug.Enable(false) })
}

// runJSONMisc runs a command that must succeed with only JSON on stdout (progress
// may go to stderr), decodes the result into v and returns stdout.
func runJSONMisc(t *testing.T, v any, args ...string) string {
	t.Helper()
	stdout, stderr, err := runAsProcess(t, args...)
	if err != nil {
		t.Fatalf("error: %v\nstderr: %s", err, stderr)
	}
	if err := json.Unmarshal([]byte(stdout), v); err != nil {
		t.Fatalf("stdout is no JSON result: %v\n%s", err, stdout)
	}
	return stdout
}

func withFakeCatalog(t *testing.T) {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/alpha/manifest.yaml" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`name: alpha
version: 1.2.0
description: Does alpha things
homepage: https://example.com/alpha
supportsCloud: true
dependencies:
  - name: beta
app:
  name: alpha
  port: 8080
  appIcon: icon.png
  containerImage:
    image: registry.example.com/alpha:1.2.0
`))
	}))
	t.Cleanup(s.Close)
	withConfig(t, "catalog:\n  hosts:\n    - name: mine\n      url: "+s.URL+"/\n")
	t.Setenv(debug.EnvVar, "")
}

func TestCatalogInfoJSON(t *testing.T) {
	withFakeCatalog(t)
	var got map[string]any
	stdout := runJSONMisc(t, &got, "catalog", "info", "mine/alpha", "-o", "json")
	want := `{"name":"alpha","catalog":"mine","version":"1.2.0","description":"Does alpha things","homepage":"https://example.com/alpha","image":"registry.example.com/alpha:1.2.0","supports_cloud":true,"dependencies":[{"name":"beta"}]}`
	if c := compactJSON(t, stdout); c != want {
		t.Errorf("stdout = %s, want %s", c, want)
	}

	stdout, _, err := runAsProcess(t, "catalog", "info", "mine/alpha")
	if err != nil {
		t.Fatal(err)
	}
	if stdout != "Does alpha things\n" {
		t.Errorf("text stdout = %q", stdout)
	}
}

func TestCatalogHostsJSON(t *testing.T) {
	withConfig(t, "catalog:\n  hosts:\n    - name: mine\n      url: https://catalog.example.com/\n")
	t.Setenv(debug.EnvVar, "")
	var got []catalogHost
	stdout := runJSONMisc(t, &got, "catalog", "hosts", "-o", "json")
	if c := compactJSON(t, stdout); c != `[{"name":"mine","url":"https://catalog.example.com/"}]` {
		t.Errorf("stdout = %s", c)
	}

	withConfig(t, "")
	stdout = runJSONMisc(t, &got, "catalog", "hosts", "-o", "json")
	if c := compactJSON(t, stdout); c != `[]` {
		t.Errorf("no catalogs: stdout = %s, want []", c)
	}
}

func TestVersionJSON(t *testing.T) {
	withFakeAPI(t, map[string]string{"/api/v2/system/version": `"26.1.0"`})
	var got versions
	runJSONMisc(t, &got, "version", "-o", "json")
	if got.CLI == "" || got.Nova != "" {
		t.Errorf("without --verbose: %+v, want only the cli version", got)
	}

	got = versions{}
	runJSONMisc(t, &got, "version", "-v", "-o", "json")
	if got.CLI == "" || got.Nova != "26.1.0" || got.AvailableStable != "" || got.AvailableNext != "" {
		t.Errorf("with --verbose: %+v", got)
	}
}

func TestLicenseGetJSON(t *testing.T) {
	license := `{"product_name":"NOVA","license_key":"K-1","status":"OK","allowed_activations":1,"consumed_activations":1}`
	withFakeAPI(t, map[string]string{
		"/api/v2/system/version": "26.1.0",
		"/api/v2/license":        license,
	})
	var got map[string]any
	stdout := runJSONMisc(t, &got, "license", "get", "-o", "json")
	if c := compactJSON(t, stdout); c != license {
		t.Errorf("stdout = %s, want the license object %s", c, license)
	}
}

func TestBackupDownloadJSON(t *testing.T) {
	withFakeAPI(t, map[string]string{
		"/api/v2/system/version":                 "26.1.0",
		"/api/v2/system/configuration/resources": `[{"id":"cells","name":"Cells"}]`,
		"/api/v2/system/configuration":           "backup bytes",
	})
	var got downloadedBackup
	runJSONMisc(t, &got, "backup", "download", "b.tar.gz", "-o", "json")
	if got.ID != "0b6c1f4e-5f0a-4c55-9a51-2f1f0c6f3e11" || !filepath.IsAbs(got.Path) || filepath.Base(got.Path) != "b.tar.gz" || got.Bytes != int64(len("backup bytes")) {
		t.Errorf("result = %+v", got)
	}
	if b, err := os.ReadFile(got.Path); err != nil || string(b) != "backup bytes" {
		t.Errorf("the named file: %q, %v", b, err)
	}
}

func TestDiagnosisDownloadJSON(t *testing.T) {
	withFakeAPI(t, map[string]string{
		"/api/v2/system/version":               "26.1.0",
		"/api/v2/system/diagnosis-package/zip": "zip bytes",
	})
	var got downloadedDiagnosis
	runJSONMisc(t, &got, "diagnosis", "download", "-o", "json")
	if !filepath.IsAbs(got.Path) || filepath.Base(got.Path) != "diagnosis.zip" || got.Bytes != int64(len("zip bytes")) {
		t.Errorf("result = %+v", got)
	}
	if b, err := os.ReadFile(got.Path); err != nil || string(b) != "zip bytes" {
		t.Errorf("the named file: %q, %v", b, err)
	}

	// text prints nothing, as before
	stdout, stderr, err := runAsProcess(t, "diagnosis", "download")
	if err != nil || stdout != "" || stderr != "" {
		t.Errorf("text: stdout = %q, stderr = %q, err = %v", stdout, stderr, err)
	}
}

const configViewTestConfig = `current-instance: lab
editor: vim
instances:
  - name: lab
    kind: host
    host: lab.local
    cellname: line1
nucleus-servers:
  - name: dev
    host: nucleus.example.com
    token: s3cret-nucleus-token
current-nucleus: dev
`

func TestConfigViewJSONRedactsTokens(t *testing.T) {
	withConfig(t, configViewTestConfig)
	t.Setenv(debug.EnvVar, "")
	t.Setenv("NOVA_ENV", "")
	var got configView
	stdout := runJSONMisc(t, &got, "config", "view", "-o", "json")
	if strings.Contains(stdout, "s3cret") {
		t.Fatalf("the token leaks without --show-secrets:\n%s", stdout)
	}
	if got.Instance == nil || *got.Instance != (configInstance{Name: "lab", Kind: "host", Source: "config"}) {
		t.Errorf("instance = %+v", got.Instance)
	}
	if got.Nucleus == nil || got.Nucleus.Name != "dev" || got.Env.Name == "" || got.Env.Source == "" {
		t.Errorf("nucleus = %+v, env = %+v", got.Nucleus, got.Env)
	}
	for key, want := range map[string]string{
		"host":          "lab.local",
		"cellname":      "line1",
		"editor":        "vim",
		"nucleus-host":  "nucleus.example.com",
		"nucleus-token": "<redacted>",
	} {
		if got.Settings[key] != want {
			t.Errorf("settings[%s] = %q, want %q", key, got.Settings[key], want)
		}
	}

	got = configView{}
	runJSONMisc(t, &got, "config", "view", "-o", "json", "--show-secrets")
	if got.Settings["nucleus-token"] != "s3cret-nucleus-token" {
		t.Errorf("--show-secrets: nucleus-token = %q", got.Settings["nucleus-token"])
	}

	// no instance selected: null, not an empty object
	withConfig(t, "")
	stdout = runJSONMisc(t, &got, "config", "view", "-o", "json")
	var raw map[string]json.RawMessage
	_ = json.Unmarshal([]byte(stdout), &raw)
	if string(raw["instance"]) != "null" || string(raw["nucleus"]) != "null" {
		t.Errorf("instance = %s, nucleus = %s, want null", raw["instance"], raw["nucleus"])
	}
}

func TestConfigViewTextUnchanged(t *testing.T) {
	withConfig(t, configViewTestConfig)
	t.Setenv(debug.EnvVar, "")
	t.Setenv("NOVA_ENV", "")
	stdout, _, err := runAsProcess(t, "config", "view")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"instance: lab (host, from config)\n",
		"nucleus: dev (from config)\n",
		"nucleus-token: <redacted>\n",
		"host: lab.local\n",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("text lacks %q:\n%s", want, stdout)
		}
	}
}

// app update-env names the file and the variables, never their values.
func TestAppUpdateEnvJSON(t *testing.T) {
	withFakeInstance(t)
	if err := os.MkdirAll("demo", 0o700); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		".nova":      "name: demo\n",
		"go.mod":     "module demo\n",
		".env.local": "NOVA_API=\"http://elsewhere\"\nLOG_LEVEL=\"warn\"\n",
	} {
		if err := os.WriteFile(filepath.Join("demo", name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	var got appEnvResult
	stdout := runJSONMisc(t, &got, "app", "update-env", "demo", "-o", "json")
	if !filepath.IsAbs(got.Path) || filepath.Base(got.Path) != ".env.local" || got.Token != "not_needed" {
		t.Errorf("result = %+v", got)
	}
	want := `[{"name":"ACCESS_TOKEN","status":"set","differs":false},` +
		`{"name":"CELL_NAME","status":"set","differs":false},` +
		`{"name":"BASE_PATH","status":"set","differs":false},` +
		`{"name":"NOVA_API","status":"kept","differs":true},` +
		`{"name":"LOG_LEVEL","status":"kept","differs":false}]`
	vars, _ := json.Marshal(got.Variables)
	if string(vars) != want {
		t.Errorf("variables = %s, want %s", vars, want)
	}
	for _, leak := range []string{"elsewhere", "127.0.0.1", "/cell/demo"} {
		if strings.Contains(stdout, leak) {
			t.Errorf("stdout carries the value %q:\n%s", leak, stdout)
		}
	}

	got = appEnvResult{}
	runJSONMisc(t, &got, "app", "update-env", "demo", "--reset", "-o", "json")
	for _, v := range got.Variables {
		if v.Differs || (v.Name == "NOVA_API" && v.Status != "set") {
			t.Errorf("after --reset: %+v", v)
		}
	}
}

func TestAppUpdateEnvTextUnchanged(t *testing.T) {
	withFakeInstance(t)
	if err := os.MkdirAll("demo", 0o700); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{".nova": "name: demo\n", "go.mod": "module demo\n"} {
		if err := os.WriteFile(filepath.Join("demo", name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	stdout, stderr, err := runAsProcess(t, "app", "update-env", "demo")
	if err != nil {
		t.Fatal(err)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want nothing", stdout)
	}
	if !strings.HasPrefix(stderr, "Updated demo/.env.local: no access token needed for http://127.0.0.1:") {
		t.Errorf("stderr = %q", stderr)
	}
}
