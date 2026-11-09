// Package novaapi is the code behind `nova api` (ADR-0013): a client bound to the
// instance and its v2 OpenAPI spec (fetch, cache, search, schema).
package novaapi

import (
	"net/url"
	"sort"
	"strings"
)

// BasePath is where the v2 API lives on an instance. Paths given to `nova api`
// are always complete (they include this prefix), so other APIs on the instance
// (apps, NovaFlow) stay reachable.
const BasePath = "/api/v2"

// Operation is one operation of the v2 spec, with the complete path template.
type Operation struct {
	ID      string `json:"operationId"`
	Method  string `json:"method"`
	Path    string `json:"path"`
	Summary string `json:"summary,omitempty"`
	// Stream marks WebSocket and server-stream endpoints (x-serverstream,
	// x-bidistream in the spec).
	Stream bool `json:"stream,omitempty"`
}

// Match finds the operation of method and path (with or without query). Path
// parameters match one non-empty segment; the template with the most literal
// segments wins (e.g. /ios/values before /{key}). The values of the path
// parameters are returned unescaped.
func Match(ops []Operation, method, path string) (*Operation, map[string]string) {
	if i := strings.IndexByte(path, '?'); i >= 0 {
		path = path[:i]
	}
	segs := splitPath(path)
	method = strings.ToUpper(method)
	var best *Operation
	var bestParams map[string]string
	bestLiterals := -1
	for i := range ops {
		op := &ops[i]
		if op.Method != method {
			continue
		}
		tmpl := splitPath(op.Path)
		if len(tmpl) != len(segs) {
			continue
		}
		params, literals, ok := matchSegments(tmpl, segs)
		if ok && literals > bestLiterals {
			best, bestParams, bestLiterals = op, params, literals
		}
	}
	return best, bestParams
}

func matchSegments(tmpl, segs []string) (map[string]string, int, bool) {
	params := map[string]string{}
	literals := 0
	for i, t := range tmpl {
		s, err := url.PathUnescape(segs[i])
		if err != nil {
			s = segs[i]
		}
		if name, ok := paramName(t); ok {
			if s == "" {
				return nil, 0, false
			}
			params[name] = s
			continue
		}
		if t != s {
			return nil, 0, false
		}
		literals++
	}
	return params, literals, true
}

func paramName(seg string) (string, bool) {
	if strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}") {
		return seg[1 : len(seg)-1], true
	}
	return "", false
}

func splitPath(p string) []string {
	return strings.Split(strings.Trim(p, "/"), "/")
}

func sortOperations(ops []Operation) {
	sort.Slice(ops, func(i, j int) bool {
		if ops[i].Path != ops[j].Path {
			return ops[i].Path < ops[j].Path
		}
		return ops[i].Method < ops[j].Method
	})
}
