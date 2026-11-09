// Package ingress recognizes the answers of the ingress in front of a NOVA
// instance that don't come from the addressed service: the login redirect and
// the error pages, which may arrive with HTTP 200. Never trust the status code
// of an instance answer alone.
//
// The checks match markers a service answer doesn't carry by chance (`nova api`
// passes arbitrary answers, e.g. stored objects), as observed on a cloud
// instance (2026-11):
//
//   - 401 page: text/plain, "Error 401: Unauthorized\n…"
//   - JSON error page (Accept: application/json): {"error": true, "code": 401,
//     "message": "Unauthorized", "description": …, "details": {"ingress_name": …,
//     "original_uri": …, …}}
//   - login redirect: 302 to https://auth.<domain>/oauth2/start?rd=…
package ingress

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
)

// IsJSON reports whether the answer declares a JSON content type.
func IsJSON(h http.Header) bool {
	return strings.Contains(h.Get("Content-Type"), "json")
}

// IsUnauthorizedPage recognizes the plain-text "Error 401: Unauthorized" page the
// ingress serves (with HTTP 200) for a missing or expired token.
func IsUnauthorizedPage(h http.Header, b []byte) bool {
	ct := strings.ToLower(h.Get("Content-Type"))
	if ct != "" && !strings.HasPrefix(ct, "text/plain") {
		return false
	}
	return bytes.HasPrefix(bytes.TrimSpace(b), []byte("Error 401"))
}

// IsLoginRedirect recognizes the redirect of unauthenticated app paths to the
// login page (/oauth2/start).
func IsLoginRedirect(resp *http.Response) bool {
	if resp.StatusCode < 300 || resp.StatusCode >= 400 {
		return false
	}
	return strings.Contains(resp.Header.Get("Location"), "/oauth2/")
}

// ErrorPageCode recognizes the ingress' JSON error page, which (like the
// plain-text one) may come with HTTP 200:
// {"error": true, "code": 401, "message": "Unauthorized", ...}
func ErrorPageCode(h http.Header, b []byte) (int, bool) {
	if !IsJSON(h) || !bytes.HasPrefix(bytes.TrimSpace(b), []byte("{")) {
		return 0, false
	}
	var page struct {
		Error   bool `json:"error"`
		Code    int  `json:"code"`
		Details struct {
			IngressName string `json:"ingress_name"`
			OriginalURI string `json:"original_uri"`
		} `json:"details"`
	}
	if json.Unmarshal(b, &page) != nil || !page.Error || page.Code < 400 {
		return 0, false
	}
	if page.Details.IngressName == "" && page.Details.OriginalURI == "" {
		return 0, false
	}
	return page.Code, true
}

// IsUnauthorized reports whether an answer means "not logged in": the plain-text
// 401 page, the login redirect or the JSON error page with code 401. b needs to
// hold only the start of the body.
func IsUnauthorized(resp *http.Response, b []byte) bool {
	if IsUnauthorizedPage(resp.Header, b) || IsLoginRedirect(resp) {
		return true
	}
	code, ok := ErrorPageCode(resp.Header, b)
	return ok && code == http.StatusUnauthorized
}

// ErrorDetail extracts FastAPI's "detail" or the NOVA "message" field, and
// falls back to the (shortened) body.
func ErrorDetail(b []byte) string {
	var e struct {
		Detail  any    `json:"detail"`
		Message string `json:"message"`
	}
	if json.Unmarshal(b, &e) == nil {
		switch d := e.Detail.(type) {
		case string:
			return d
		case nil:
		default:
			if j, err := json.Marshal(d); err == nil {
				return string(j)
			}
		}
		if e.Message != "" {
			return e.Message
		}
	}
	s := strings.TrimSpace(string(b))
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	return s
}
