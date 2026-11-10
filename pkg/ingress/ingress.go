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

// Checks recognizes the ingress' answers. There are two sets: Strict for
// clients that pass on arbitrary answers (`nova api`: a stored object may contain
// "Error 401" or {"error": true, "code": 401}), and Known for clients of endpoints
// whose answers have a known shape (NovaFlow), where a loose match can't hit a
// real answer and also covers ingresses with other markers.
type Checks struct {
	strict bool
}

var (
	// Strict matches only the markers of the real pages (see the package doc).
	Strict = Checks{strict: true}
	// Known matches the pages loosely: "Error 401" at the start, any redirect
	// to a login url, any JSON object with "error" and a "code" >= 400.
	Known = Checks{strict: false}
)

// IsUnauthorizedPage recognizes the plain-text "Error 401: Unauthorized" page the
// ingress serves (with HTTP 200) for a missing or expired token.
func (c Checks) IsUnauthorizedPage(h http.Header, b []byte) bool {
	if c.strict {
		ct := strings.ToLower(h.Get("Content-Type"))
		if ct != "" && !strings.HasPrefix(ct, "text/plain") {
			return false
		}
	}
	return bytes.HasPrefix(bytes.TrimSpace(b), []byte("Error 401"))
}

// IsLoginRedirect recognizes the redirect of unauthenticated app paths to the
// login page (/oauth2/start).
func (c Checks) IsLoginRedirect(resp *http.Response) bool {
	if resp == nil || resp.StatusCode < 300 || resp.StatusCode >= 400 {
		return false
	}
	loc := resp.Header.Get("Location")
	return strings.Contains(loc, "/oauth2/") || (!c.strict && strings.Contains(loc, "login"))
}

// ErrorPageCode recognizes the ingress' JSON error page, which (like the
// plain-text one) may come with HTTP 200:
// {"error": true, "code": 401, "message": "Unauthorized", "details": {…}}.
// b must hold the whole page.
func (c Checks) ErrorPageCode(h http.Header, b []byte) (int, bool) {
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
	if c.strict && page.Details.IngressName == "" && page.Details.OriginalURI == "" {
		return 0, false
	}
	return page.Code, true
}

// IsUnauthorized reports whether an answer means "not logged in": the plain-text
// 401 page, the login redirect or the JSON error page with code 401. For the
// JSON page b must hold the whole body (see MaybeErrorPage).
func (c Checks) IsUnauthorized(resp *http.Response, b []byte) bool {
	if c.IsUnauthorizedPage(resp.Header, b) || c.IsLoginRedirect(resp) {
		return true
	}
	code, ok := c.ErrorPageCode(resp.Header, b)
	return ok && code == http.StatusUnauthorized
}

// MaybeErrorPage reports whether the start of a body could be the JSON error
// page, so a client should read it completely before deciding (the page echoes
// the request uri and can be long).
func MaybeErrorPage(h http.Header, head []byte) bool {
	t := bytes.TrimSpace(head)
	return IsJSON(h) && bytes.HasPrefix(t, []byte("{")) && bytes.HasPrefix(bytes.TrimSpace(bytes.TrimPrefix(t, []byte("{"))), []byte(`"error"`))
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
