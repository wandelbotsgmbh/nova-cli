package novaapi

import (
	"fmt"
	"sort"
	"strings"
	"unicode"
)

// SearchResult is one hit of Search.
type SearchResult struct {
	Operation
	Score int `json:"score"`
}

// Search ranks the operations by how many query words appear in the operation
// id, path, summary, tags and description. Words count once each; matches in id,
// path and summary weigh more than in the description.
func (s *Spec) Search(query string, limit int) []SearchResult {
	words := tokens(query)
	if len(words) == 0 {
		return []SearchResult{}
	}
	var hits []SearchResult
	for _, op := range s.ops {
		raw := s.operation(op)
		desc, _ := raw["description"].(string)
		var tags []string
		if ts, ok := raw["tags"].([]any); ok {
			for _, t := range ts {
				if t, ok := t.(string); ok {
					tags = append(tags, t)
				}
			}
		}
		fields := []struct {
			toks   map[string]bool
			weight int
		}{
			{tokenSet(op.ID), 4},
			{tokenSet(op.Path), 3},
			{tokenSet(op.Summary), 3},
			{tokenSet(strings.Join(tags, " ")), 2},
			{tokenSet(desc), 1},
		}
		score := 0
		for _, w := range words {
			best := 0
			for _, f := range fields {
				if f.weight > best && f.toks[w] {
					best = f.weight
				}
			}
			score += best
		}
		// a summary made of the query words ("Robot Controller") is the best hit
		summary := tokenSet(op.Summary)
		if score > 0 && len(summary) > 0 && containsAll(summary, words) {
			score += 2
			if len(summary) == len(tokenSet(query)) {
				score += 2
			}
		}
		if score > 0 {
			hits = append(hits, SearchResult{Operation: op, Score: score})
		}
	}
	// ties: reads first, then the shorter path (the resource before its parts)
	sort.SliceStable(hits, func(i, j int) bool {
		a, b := hits[i], hits[j]
		if a.Score != b.Score {
			return a.Score > b.Score
		}
		if (a.Method == "GET") != (b.Method == "GET") {
			return a.Method == "GET"
		}
		return len(a.Path) < len(b.Path)
	})
	if limit > 0 && len(hits) > limit {
		hits = hits[:limit]
	}
	if hits == nil {
		hits = []SearchResult{}
	}
	return hits
}

func containsAll(set map[string]bool, words []string) bool {
	for _, w := range words {
		if !set[w] {
			return false
		}
	}
	return true
}

// tokens splits text into lower-case words: camelCase, path segments and
// punctuation separate words; a trailing plural "s" is dropped.
func tokens(text string) []string {
	var out []string
	var cur []rune
	flush := func() {
		if len(cur) > 1 {
			w := strings.ToLower(string(cur))
			if len(w) > 3 && strings.HasSuffix(w, "s") && !strings.HasSuffix(w, "ss") {
				w = strings.TrimSuffix(w, "s")
			}
			out = append(out, w)
		}
		cur = cur[:0]
	}
	rs := []rune(text)
	for i, r := range rs {
		switch {
		case unicode.IsUpper(r) && i > 0 && unicode.IsLower(rs[i-1]):
			flush()
			cur = append(cur, r)
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			cur = append(cur, r)
		default:
			flush()
		}
	}
	flush()
	return out
}

func tokenSet(text string) map[string]bool {
	m := map[string]bool{}
	for _, t := range tokens(text) {
		m[t] = true
	}
	return m
}

// Find returns the operation with the given id (case-insensitive).
func (s *Spec) Find(id string) (*Operation, bool) {
	for i := range s.ops {
		if strings.EqualFold(s.ops[i].ID, id) {
			return &s.ops[i], true
		}
	}
	return nil, false
}

// operation returns the raw spec object of op.
func (s *Spec) operation(op Operation) map[string]any {
	item := s.pathItem(op.Path)
	raw, _ := item[strings.ToLower(op.Method)].(map[string]any)
	return raw
}

func (s *Spec) pathItem(fullPath string) map[string]any {
	paths, _ := s.doc["paths"].(map[string]any)
	item, _ := paths[strings.TrimPrefix(fullPath, BasePath)].(map[string]any)
	return item
}

// OperationSchema is what `nova api schema` prints: enough to build a request.
type OperationSchema struct {
	Operation
	Refused     string                    `json:"refused,omitempty"`
	Description string                    `json:"description,omitempty"`
	Parameters  []Parameter               `json:"parameters"`
	RequestBody *Body                     `json:"requestBody,omitempty"`
	Responses   map[string]ResponseSchema `json:"responses"`
}

// Parameter is a path, query or header parameter.
type Parameter struct {
	Name        string `json:"name"`
	In          string `json:"in"`
	Required    bool   `json:"required,omitempty"`
	Description string `json:"description,omitempty"`
	Schema      any    `json:"schema,omitempty"`
}

// Body is a request body with its schema ($refs resolved).
type Body struct {
	ContentType string `json:"contentType"`
	Required    bool   `json:"required,omitempty"`
	Schema      any    `json:"schema,omitempty"`
}

// ResponseSchema names the schema of an answer without expanding it.
type ResponseSchema struct {
	Description string `json:"description,omitempty"`
	ContentType string `json:"contentType,omitempty"`
	Schema      string `json:"schema,omitempty"`
}

// maxSchemaDepth limits how deep $refs are expanded; deeper or recursive
// references stay as {"$ref": "<Name>"}.
const maxSchemaDepth = 6

// Schema describes an operation: parameters and request body with all $refs
// expanded, responses by schema name.
func (s *Spec) Schema(op Operation) OperationSchema {
	raw := s.operation(op)
	out := OperationSchema{Operation: op, Parameters: []Parameter{}, Responses: map[string]ResponseSchema{}}
	if IsStream(&op) {
		out.Refused = "stream or long poll: not supported by 'nova api'"
	}
	out.Description, _ = raw["description"].(string)

	var params []any
	if ps, ok := s.pathItem(op.Path)["parameters"].([]any); ok {
		params = append(params, ps...)
	}
	if ps, ok := raw["parameters"].([]any); ok {
		params = append(params, ps...)
	}
	for _, p := range params {
		pm, _ := s.deref(p).(map[string]any)
		if pm == nil {
			continue
		}
		var param Parameter
		param.Name, _ = pm["name"].(string)
		param.In, _ = pm["in"].(string)
		param.Required, _ = pm["required"].(bool)
		param.Description, _ = pm["description"].(string)
		param.Schema = s.expand(pm["schema"], 0, nil)
		out.Parameters = append(out.Parameters, param)
	}

	if rb, ok := s.deref(raw["requestBody"]).(map[string]any); ok {
		required, _ := rb["required"].(bool)
		if content, ok := rb["content"].(map[string]any); ok {
			ct, media := firstContent(content)
			body := &Body{ContentType: ct, Required: required}
			if mm, ok := media.(map[string]any); ok {
				body.Schema = s.expand(mm["schema"], 0, nil)
			}
			out.RequestBody = body
		}
	}

	if rs, ok := raw["responses"].(map[string]any); ok {
		for code, r := range rs {
			rm, _ := s.deref(r).(map[string]any)
			var resp ResponseSchema
			resp.Description, _ = rm["description"].(string)
			if content, ok := rm["content"].(map[string]any); ok {
				ct, media := firstContent(content)
				resp.ContentType = ct
				if mm, ok := media.(map[string]any); ok {
					resp.Schema = schemaName(mm["schema"])
				}
			}
			out.Responses[code] = resp
		}
	}
	return out
}

// firstContent prefers application/json over other media types.
func firstContent(content map[string]any) (string, any) {
	if m, ok := content["application/json"]; ok {
		return "application/json", m
	}
	keys := make([]string, 0, len(content))
	for k := range content {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return "", nil
	}
	return keys[0], content[keys[0]]
}

func schemaName(schema any) string {
	m, _ := schema.(map[string]any)
	if ref, ok := m["$ref"].(string); ok {
		return refName(ref)
	}
	if t, ok := m["type"].(string); ok {
		if t == "array" {
			if n := schemaName(m["items"]); n != "" {
				return n + "[]"
			}
		}
		return t
	}
	return ""
}

func refName(ref string) string {
	return ref[strings.LastIndex(ref, "/")+1:]
}

// lookupRef resolves a local reference like #/components/schemas/Name.
func (s *Spec) lookupRef(ref string) (any, error) {
	if !strings.HasPrefix(ref, "#/") {
		return nil, fmt.Errorf("unsupported $ref %q", ref)
	}
	var cur any = s.doc
	for _, part := range strings.Split(ref[2:], "/") {
		part = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("$ref %q not found", ref)
		}
		cur, ok = m[part]
		if !ok {
			return nil, fmt.Errorf("$ref %q not found", ref)
		}
	}
	return cur, nil
}

// deref follows a top-level $ref (parameters, request bodies, responses).
func (s *Spec) deref(v any) any {
	for i := 0; i < 5; i++ {
		m, ok := v.(map[string]any)
		if !ok {
			return v
		}
		ref, ok := m["$ref"].(string)
		if !ok {
			return v
		}
		r, err := s.lookupRef(ref)
		if err != nil {
			return v
		}
		v = r
	}
	return v
}

// expand copies a schema with $refs inlined, up to maxSchemaDepth and without
// following a reference into itself. Vendor extensions (x-…) are dropped.
func (s *Spec) expand(v any, depth int, seen []string) any {
	switch t := v.(type) {
	case map[string]any:
		if ref, ok := t["$ref"].(string); ok {
			name := refName(ref)
			for _, n := range seen {
				if n == name {
					return map[string]any{"$ref": name, "recursive": true}
				}
			}
			if depth >= maxSchemaDepth {
				return map[string]any{"$ref": name}
			}
			r, err := s.lookupRef(ref)
			if err != nil {
				return t
			}
			return s.expand(r, depth+1, append(seen, name))
		}
		out := make(map[string]any, len(t))
		for k, val := range t {
			if strings.HasPrefix(k, "x-") {
				continue
			}
			out[k] = s.expand(val, depth, seen)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = s.expand(val, depth, seen)
		}
		return out
	}
	return v
}
