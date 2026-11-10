package ingress

import (
	"net/http"
	"testing"
)

func header(kv ...string) http.Header {
	h := http.Header{}
	for i := 0; i < len(kv); i += 2 {
		h.Set(kv[i], kv[i+1])
	}
	return h
}

// The pages as a cloud instance serves them (2026-11).
const jsonPage = `{"error": true, "code": 401, "message": "Unauthorized",
  "description": "The requested page needs a username and a password",
  "details": {"host": "x.instance.wandelbots.io", "original_uri": "/api/v2/cells",
    "ingress_name": "api-gateway", "service_name": "api-gateway"}}`

func TestRecognizesIngressPages(t *testing.T) {
	plain := &http.Response{StatusCode: 200, Header: header("Content-Type", "text/plain; charset=utf-8")}
	if !Strict.IsUnauthorized(plain, []byte("Error 401: Unauthorized\nThe requested page needs a username and a password\n")) {
		t.Error("plain-text 401 page not recognized")
	}
	js := &http.Response{StatusCode: 200, Header: header("Content-Type", "application/json; charset=utf-8")}
	if !Strict.IsUnauthorized(js, []byte(jsonPage)) {
		t.Error("JSON 401 page not recognized")
	}
	if code, ok := Strict.ErrorPageCode(js.Header, []byte(`{"error": true, "code": 503, "details": {"ingress_name": "x"}}`)); !ok || code != 503 {
		t.Errorf("JSON 503 page: %d %v", code, ok)
	}
	redirect := &http.Response{StatusCode: 302, Header: header("Location", "https://auth.instance.wandelbots.io/oauth2/start?rd=https://x/cell/app/")}
	if !Strict.IsUnauthorized(redirect, nil) {
		t.Error("login redirect not recognized")
	}
}

// Service answers can contain the same words; they must not be taken for pages.
func TestIgnoresServiceAnswers(t *testing.T) {
	js := &http.Response{StatusCode: 200, Header: header("Content-Type", "application/json")}
	if Strict.IsUnauthorized(js, []byte(`{"error": true, "code": 401, "message": "stored object"}`)) {
		t.Error("JSON without the ingress details taken for the error page")
	}
	if Strict.IsUnauthorized(js, []byte(`"Error 401 is what we log"`)) {
		t.Error("JSON string taken for the plain-text page")
	}
	html := &http.Response{StatusCode: 200, Header: header("Content-Type", "text/html")}
	if Strict.IsUnauthorized(html, []byte("Error 401 explained")) {
		t.Error("HTML taken for the plain-text page")
	}
	redirect := &http.Response{StatusCode: 302, Header: header("Location", "/cell/app/login")}
	if Strict.IsUnauthorized(redirect, nil) {
		t.Error("an app's own login redirect taken for the ingress login")
	}
}

// Known (NovaFlow) keeps matching pages without the markers.
func TestKnownMatchesLoosely(t *testing.T) {
	js := &http.Response{StatusCode: 200, Header: header("Content-Type", "application/json")}
	if !Known.IsUnauthorized(js, []byte(`{"error": true, "code": 401, "message": "Unauthorized"}`)) {
		t.Error("JSON page without details")
	}
	redirect := &http.Response{StatusCode: 302, Header: header("Location", "https://auth.example/login?rd=x")}
	if !Known.IsUnauthorized(redirect, nil) {
		t.Error("login redirect without /oauth2/")
	}
}

func TestMaybeErrorPage(t *testing.T) {
	js := header("Content-Type", "application/json")
	for body, want := range map[string]bool{
		"{\n  \"error\": true, \"code\": 4": true,
		`{"error":`:                         true,
		`{"name": "x"}`:                     false,
		`[{"error": true}]`:                 false,
	} {
		if got := MaybeErrorPage(js, []byte(body)); got != want {
			t.Errorf("%q: %v", body, got)
		}
	}
	if MaybeErrorPage(header("Content-Type", "text/plain"), []byte(`{"error": true}`)) {
		t.Error("text/plain taken for the JSON page")
	}
}
