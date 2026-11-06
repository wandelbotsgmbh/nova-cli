package debug

import (
	"net/url"
	"regexp"
	"strings"
)

const redacted = "[redacted]"

// sensitive matches names of fields, headers and parameters whose values are
// never written: tokens, passwords, keys and kubeconfigs (the k8srun service
// sends a whole kubeconfig, client key included, base64-encoded as "kubeconfig").
// Matching any name that contains one of these also hides harmless fields
// (token_expiry, secretName): in debug output that is the safe side.
const sensitive = `(?i:[\w.-]*(?:token|secret|password|passwd|api[_-]?key|private[_-]?key|` +
	`client-key-data|client-certificate-data|kubeconfig|code_verifier|device_code|authorization|cookie)[\w.-]*)`

var (
	// "name": "value" in JSON; a value cut off by the body limit has no closing quote
	jsonField = regexp.MustCompile(`("` + sensitive + `"\s*:\s*)"(?:[^"\\]|\\.)*(?:"|\\?$)`)
	// name: value in YAML (kubeconfig, manifests)
	yamlField = regexp.MustCompile(`(?m)^(\s*(?:-\s+)?` + sensitive + `:[ \t]*)[^\s#].*$`)
	// name=value in forms, query strings and command lines
	formField = regexp.MustCompile(`((?:^|[&?\s])-{0,2}` + sensitive + `=)[^&\s]*`)
	// a bearer token or JWT anywhere else
	bearer = regexp.MustCompile(`(?i)(bearer\s+)[\w.~+/=-]+`)
	jwt    = regexp.MustCompile(`\beyJ[\w-]+\.[\w-]+\.[\w-]+`)
)

// Redact removes secrets from text: JSON and YAML fields, form and query
// parameters with sensitive names, bearer tokens and JWTs.
func Redact(s string) string {
	s = jsonField.ReplaceAllString(s, `$1"`+redacted+`"`)
	s = yamlField.ReplaceAllString(s, `${1}`+redacted)
	s = formField.ReplaceAllString(s, `${1}`+redacted)
	s = bearer.ReplaceAllString(s, `${1}`+redacted)
	return jwt.ReplaceAllString(s, redacted)
}

var sensitiveName = regexp.MustCompile(`^` + sensitive + `$`)

// RedactURL returns u without a password and with sensitive query values removed.
func RedactURL(u *url.URL) string {
	if u == nil {
		return ""
	}
	c := *u
	if _, ok := c.User.Password(); ok {
		c.User = url.UserPassword(c.User.Username(), redacted)
	}
	if c.RawQuery != "" {
		q := c.Query()
		for k := range q {
			if sensitiveName.MatchString(k) {
				q[k] = []string{redacted}
			}
		}
		c.RawQuery = q.Encode()
	}
	s := c.String()
	// keep the placeholder readable, Encode escapes the brackets
	return strings.ReplaceAll(s, url.QueryEscape(redacted), redacted)
}

// RedactHeader returns the value of header name for logging. Credentials keep
// only their scheme, e.g. "Bearer [redacted]".
func RedactHeader(name, value string) string {
	if !sensitiveName.MatchString(name) {
		return Redact(value)
	}
	if scheme, _, ok := strings.Cut(value, " "); ok && !strings.Contains(scheme, "=") {
		return scheme + " " + redacted
	}
	return redacted
}
