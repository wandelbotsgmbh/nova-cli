// Package ingress recognizes the answers of the ingress in front of a NOVA
// instance that don't come from the addressed service: the login redirect and
// the error pages, which may arrive with HTTP 200. Never trust the status code
// of an instance answer alone.
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
func IsUnauthorizedPage(b []byte) bool {
	return bytes.HasPrefix(bytes.TrimSpace(b), []byte("Error 401"))
}

// IsLoginRedirect recognizes the redirect of unauthenticated app paths to the
// login page (/oauth2/start).
func IsLoginRedirect(resp *http.Response) bool {
	if resp.StatusCode < 300 || resp.StatusCode >= 400 {
		return false
	}
	loc := resp.Header.Get("Location")
	return strings.Contains(loc, "/oauth2/") || strings.Contains(loc, "login")
}

// ErrorPageCode recognizes the ingress' JSON error page, which (like the
// plain-text one) may come with HTTP 200:
// {"error": true, "code": 401, "message": "Unauthorized", ...}
func ErrorPageCode(h http.Header, b []byte) (int, bool) {
	if !IsJSON(h) || !bytes.HasPrefix(bytes.TrimSpace(b), []byte("{")) {
		return 0, false
	}
	var page struct {
		Error bool `json:"error"`
		Code  int  `json:"code"`
	}
	if json.Unmarshal(b, &page) != nil || !page.Error || page.Code < 400 {
		return 0, false
	}
	return page.Code, true
}

// IsUnauthorized reports whether an answer means "not logged in": the plain-text
// 401 page, the login redirect or the JSON error page with code 401. b needs to
// hold only the start of the body.
func IsUnauthorized(resp *http.Response, b []byte) bool {
	if IsUnauthorizedPage(b) || IsLoginRedirect(resp) {
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
