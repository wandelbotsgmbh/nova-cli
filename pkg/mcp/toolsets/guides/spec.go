package guides

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/novaapi"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/tool"
	"gopkg.in/yaml.v3"
)

// specPath is the instance's public API description, relative to /api/v2.
const specPath = "/ui/public.openapi.yaml"

// specCache loads the instance's API description once per instance (use_instance can
// switch it); a failed load is retried on the next call.
type specCache struct {
	client *novaapi.Client
	mu     sync.Mutex
	spec   *apiSpec
	base   string
}

func (c *specCache) get(ctx context.Context, toolName string) (*apiSpec, *novaapi.Error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	base := c.client.BaseURL(ctx)
	if c.spec != nil && c.base == base {
		return c.spec, nil
	}
	res, apiErr := c.client.Do(ctx, novaapi.Call{Tool: toolName, Method: http.MethodGet, Path: specPath,
		NotFound: "this instance doesn't serve its API description at /api/v2" + specPath})
	if apiErr != nil {
		return nil, apiErr
	}
	spec, err := parseSpec(res.Body)
	if err != nil {
		return nil, c.client.UnexpectedAnswer(ctx, toolName, res.Status, res.Body)
	}
	c.spec, c.base = spec, base
	return spec, nil
}

type operation struct {
	ID           string
	Method       string
	Path         string
	Summary      string
	Description  string
	Tags         []string
	Scope        string
	WebSocket    bool
	Experimental bool
	Deprecated   bool
	node         map[string]any
	pathParams   []any
}

type apiSpec struct {
	Version    string
	ops        []operation
	components map[string]any
}

var methods = []string{"get", "put", "post", "delete", "patch", "head", "options", "trace"}

func parseSpec(raw []byte) (*apiSpec, error) {
	var doc any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	root, ok := normalize(doc).(map[string]any)
	if !ok {
		return nil, fmt.Errorf("API description is not an object")
	}
	paths, ok := root["paths"].(map[string]any)
	if !ok || len(paths) == 0 {
		return nil, fmt.Errorf("API description has no paths")
	}
	spec := &apiSpec{components: asMap(root["components"])}
	spec.Version = str(asMap(root["info"])["version"])
	for path, item := range paths {
		pathItem := asMap(item)
		pathParams, _ := pathItem["parameters"].([]any)
		for _, method := range methods {
			op := asMap(pathItem[method])
			id := str(op["operationId"])
			if id == "" {
				continue
			}
			description := str(op["description"])
			var tags []string
			for _, t := range asSlice(op["tags"]) {
				tags = append(tags, str(t))
			}
			deprecated, _ := op["deprecated"].(bool)
			spec.ops = append(spec.ops, operation{
				ID: id, Method: strings.ToUpper(method), Path: path,
				Summary: str(op["summary"]), Description: description, Tags: tags,
				Scope:        str(op["x-scope"]),
				WebSocket:    strings.Contains(strings.ToLower(description), "websocket endpoint"),
				Experimental: strings.Contains(path, "/experimental/"),
				Deprecated:   deprecated,
				node:         op, pathParams: pathParams,
			})
		}
	}
	sort.Slice(spec.ops, func(i, j int) bool { return spec.ops[i].ID < spec.ops[j].ID })
	return spec, nil
}

// normalize turns map[any]any (non-string YAML keys such as 200) into map[string]any.
func normalize(v any) any {
	switch x := v.(type) {
	case map[string]any:
		for k, val := range x {
			x[k] = normalize(val)
		}
		return x
	case map[any]any:
		m := make(map[string]any, len(x))
		for k, val := range x {
			m[fmt.Sprint(k)] = normalize(val)
		}
		return m
	case []any:
		for i := range x {
			x[i] = normalize(x[i])
		}
		return x
	}
	return v
}

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func asSlice(v any) []any {
	s, _ := v.([]any)
	return s
}

func str(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	default:
		return fmt.Sprint(x)
	}
}

// --- search_api_docs ---

type searchInput struct {
	Query string `json:"query" jsonschema:"keywords, all must match (operation id, path, summary, tags, description), e.g. 'motion group state' or 'tcp'"`
	Tag   string `json:"tag,omitempty" jsonschema:"only operations with this tag, e.g. 'Controller' or 'Trajectory Planning'"`
	tool.PageInput
}

type operationSummary struct {
	OperationID  string   `json:"operation_id"`
	Method       string   `json:"method"`
	Path         string   `json:"path" jsonschema:"below /api/v2"`
	Summary      string   `json:"summary"`
	Tags         []string `json:"tags"`
	WebSocket    bool     `json:"websocket" jsonschema:"a WebSocket endpoint: use it from a script, see get_guide"`
	Experimental bool     `json:"experimental"`
	Deprecated   bool     `json:"deprecated,omitempty"`
}

type searchOutput struct {
	Operations []operationSummary `json:"operations"`
	tool.PageInfo
}

func addSearchAPIDocs(s *sdk.Server, cache *specCache) {
	const name = "search_api_docs"
	sdk.AddTool(s, &sdk.Tool{
		Name: name,
		Description: "Search the NOVA v2 API of this instance (its own API description, so it matches the installed " +
			"version) for operations by keywords. For anything no tool covers; describe_api_operation shows the details " +
			"of a hit. Paginated.",
		Annotations: tool.Read("Search API docs"),
	}, func(ctx context.Context, _ *sdk.CallToolRequest, in searchInput) (*sdk.CallToolResult, searchOutput, error) {
		if err := tool.Required(name, "query", in.Query); err != nil {
			return tool.Fail[searchOutput](err)
		}
		spec, apiErr := cache.get(ctx, name)
		if apiErr != nil {
			return tool.Fail[searchOutput](apiErr)
		}
		hits := search(spec.ops, in.Query, in.Tag)
		page, info := tool.Page(hits, in.PageInput)
		out := searchOutput{Operations: make([]operationSummary, 0, len(page)), PageInfo: info}
		for _, op := range page {
			out.Operations = append(out.Operations, summarize(op))
		}
		sentence := "operations found"
		if info.Total == 0 {
			sentence = "no operation matches; try fewer or other keywords"
		}
		return tool.OK(sentence, info.Fields(map[string]any{"query": in.Query}), out)
	})
}

func summarize(op operation) operationSummary {
	tags := op.Tags
	if tags == nil {
		tags = []string{}
	}
	return operationSummary{OperationID: op.ID, Method: op.Method, Path: op.Path, Summary: op.Summary,
		Tags: tags, WebSocket: op.WebSocket, Experimental: op.Experimental, Deprecated: op.Deprecated}
}

// search returns the operations matching every term of query, best match first: terms in
// the id or summary weigh most, then path and tags, then the description.
func search(ops []operation, query, tag string) []operation {
	terms := strings.Fields(strings.ToLower(query))
	type hit struct {
		op    operation
		score int
	}
	var hits []hit
	for _, op := range ops {
		if tag != "" && !containsFold(op.Tags, tag) {
			continue
		}
		fields := []struct {
			text   string
			weight int
		}{
			{strings.ToLower(op.ID), 4}, {strings.ToLower(op.Summary), 4},
			{strings.ToLower(op.Path), 2}, {strings.ToLower(strings.Join(op.Tags, " ")), 2},
			{strings.ToLower(op.Description), 1},
		}
		score := 0
		for _, term := range terms {
			best := 0
			for _, f := range fields {
				if f.weight > best && strings.Contains(f.text, term) {
					best = f.weight
				}
			}
			if best == 0 {
				score = 0
				break
			}
			score += best
		}
		if score > 0 {
			hits = append(hits, hit{op, score})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score > hits[j].score
		}
		return hits[i].op.ID < hits[j].op.ID
	})
	out := make([]operation, len(hits))
	for i, h := range hits {
		out[i] = h.op
	}
	return out
}

func containsFold(list []string, s string) bool {
	for _, v := range list {
		if strings.EqualFold(v, s) {
			return true
		}
	}
	return false
}

// --- describe_api_operation ---

// maxDescribeBytes caps the JSON size of describe_api_operation; deeper schemas are cut
// to $ref names until it fits.
const maxDescribeBytes = 24_000

type describeInput struct {
	OperationID string `json:"operation_id" jsonschema:"operation id from search_api_docs, e.g. getMotionGroupDescription"`
	Depth       int    `json:"depth,omitempty" jsonschema:"how many levels of referenced schemas to expand (default 3, max 6); deeper ones are named, call again with a higher depth or look at a smaller part"`
}

type parameter struct {
	Name        string `json:"name"`
	In          string `json:"in"`
	Required    bool   `json:"required"`
	Description string `json:"description,omitempty"`
	Schema      any    `json:"schema,omitempty"`
}

type content struct {
	ContentType string `json:"content_type"`
	Required    bool   `json:"required,omitempty"`
	Description string `json:"description,omitempty"`
	Schema      any    `json:"schema,omitempty"`
}

type response struct {
	Status      string `json:"status"`
	Description string `json:"description,omitempty"`
	ContentType string `json:"content_type,omitempty"`
	Schema      any    `json:"schema,omitempty"`
}

type describeOutput struct {
	operationSummary
	Description string      `json:"description"`
	Scope       string      `json:"scope,omitempty" jsonschema:"permission the caller needs"`
	Parameters  []parameter `json:"parameters"`
	RequestBody *content    `json:"request_body,omitempty"`
	Responses   []response  `json:"responses"`
	Depth       int         `json:"depth" jsonschema:"levels of referenced schemas expanded"`
	Truncated   bool        `json:"truncated" jsonschema:"true if schemas were cut to fit; named $ref schemas were not expanded"`
}

func addDescribeAPIOperation(s *sdk.Server, cache *specCache) {
	const name = "describe_api_operation"
	sdk.AddTool(s, &sdk.Tool{
		Name: name,
		Description: "Parameters, request body and response schemas of one NOVA v2 API operation of this instance, " +
			"by operation id (see search_api_docs). Referenced schemas are expanded to a bounded depth; larger ones " +
			"are named instead.",
		Annotations: tool.Read("Describe API operation"),
	}, func(ctx context.Context, _ *sdk.CallToolRequest, in describeInput) (*sdk.CallToolResult, describeOutput, error) {
		if err := tool.Required(name, "operation_id", in.OperationID); err != nil {
			return tool.Fail[describeOutput](err)
		}
		spec, apiErr := cache.get(ctx, name)
		if apiErr != nil {
			return tool.Fail[describeOutput](apiErr)
		}
		op, ok := spec.find(in.OperationID)
		if !ok {
			return tool.Fail[describeOutput](&novaapi.Error{Code: novaapi.CodeNotFound, Message: fmt.Sprintf(
				"%s: no operation '%s' on this instance, find the id with search_api_docs", name, in.OperationID)})
		}
		depth := in.Depth
		if depth <= 0 {
			depth = 3
		}
		depth = min(depth, 6)
		out := spec.describe(op, renderOptions{depth: depth, descriptions: true})
		for size(out) > maxDescribeBytes && out.Depth > 0 {
			out = spec.describe(op, renderOptions{depth: out.Depth - 1, descriptions: true})
			out.Truncated = true
		}
		if size(out) > maxDescribeBytes {
			out = spec.describe(op, renderOptions{depth: 0, descriptions: false})
			out.Truncated = true
		}
		return tool.OK("operation described", map[string]any{
			"operation_id": op.ID, "method": op.Method, "path": op.Path, "depth": out.Depth, "truncated": out.Truncated,
		}, out)
	})
}

func size(v any) int {
	raw, _ := json.Marshal(v)
	return len(raw)
}

func (s *apiSpec) find(id string) (operation, bool) {
	for _, op := range s.ops {
		if strings.EqualFold(op.ID, id) {
			return op, true
		}
	}
	return operation{}, false
}

type renderOptions struct {
	depth        int
	descriptions bool
}

func (s *apiSpec) describe(op operation, opts renderOptions) describeOutput {
	out := describeOutput{
		operationSummary: summarize(op),
		Description:      truncateText(op.Description, 3000),
		Scope:            op.Scope,
		Parameters:       []parameter{},
		Responses:        []response{},
		Depth:            opts.depth,
	}
	for _, raw := range append(append([]any{}, op.pathParams...), asSlice(op.node["parameters"])...) {
		p := s.resolve(asMap(raw))
		required, _ := p["required"].(bool)
		out.Parameters = append(out.Parameters, parameter{
			Name: str(p["name"]), In: str(p["in"]), Required: required,
			Description: s.text(p["description"], opts),
			Schema:      s.render(p["schema"], opts.depth, opts, nil),
		})
	}
	if body := s.resolve(asMap(op.node["requestBody"])); body != nil {
		ct, media := pickContent(asMap(body["content"]))
		required, _ := body["required"].(bool)
		out.RequestBody = &content{ContentType: ct, Required: required,
			Description: s.text(body["description"], opts),
			Schema:      s.render(media["schema"], opts.depth, opts, nil)}
	}
	responses := asMap(op.node["responses"])
	codes := make([]string, 0, len(responses))
	for code := range responses {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	for _, code := range codes {
		r := s.resolve(asMap(responses[code]))
		ct, media := pickContent(asMap(r["content"]))
		out.Responses = append(out.Responses, response{Status: code, Description: s.text(r["description"], opts),
			ContentType: ct, Schema: s.render(media["schema"], opts.depth, opts, nil)})
	}
	return out
}

// pickContent prefers JSON, otherwise the first content type in order.
func pickContent(content map[string]any) (string, map[string]any) {
	if len(content) == 0 {
		return "", nil
	}
	if media, ok := content["application/json"]; ok {
		return "application/json", asMap(media)
	}
	types := make([]string, 0, len(content))
	for ct := range content {
		types = append(types, ct)
	}
	sort.Strings(types)
	return types[0], asMap(content[types[0]])
}

// resolve follows a local $ref (#/components/<kind>/<name>), one level.
func (s *apiSpec) resolve(node map[string]any) map[string]any {
	for i := 0; node != nil && i < 5; i++ {
		ref := str(node["$ref"])
		if ref == "" {
			return node
		}
		node = s.lookup(ref)
	}
	return node
}

func (s *apiSpec) lookup(ref string) map[string]any {
	parts := strings.Split(strings.TrimPrefix(ref, "#/components/"), "/")
	if len(parts) != 2 {
		return nil
	}
	return asMap(asMap(s.components[parts[0]])[parts[1]])
}

func refName(ref string) string { return ref[strings.LastIndex(ref, "/")+1:] }

func (s *apiSpec) text(v any, opts renderOptions) string {
	if !opts.descriptions {
		return ""
	}
	return truncateText(str(v), 300)
}

// schemaKeys are the schema keywords handed on; everything else (examples, x-*
// extensions, prefixItems) is noise for an agent.
var schemaKeys = []string{"type", "format", "enum", "const", "default", "required", "minimum", "maximum",
	"minItems", "maxItems", "minLength", "maxLength", "nullable", "title"}

// render copies a schema, expanding $refs while depth lasts and naming them after that
// (or on a cycle).
func (s *apiSpec) render(v any, depth int, opts renderOptions, stack []string) any {
	node := asMap(v)
	if node == nil {
		return nil
	}
	if ref := str(node["$ref"]); ref != "" {
		name := refName(ref)
		target := s.lookup(ref)
		if target == nil || depth <= 0 || containsFold(stack, name) {
			return map[string]any{"$ref": name}
		}
		out := asMap(s.render(target, depth-1, opts, append(stack, name)))
		if out != nil {
			out["schema_name"] = name
		}
		return out
	}
	out := map[string]any{}
	for _, k := range schemaKeys {
		if val, ok := node[k]; ok {
			out[k] = val
		}
	}
	if d := s.text(node["description"], opts); d != "" {
		out["description"] = d
	}
	if disc := asMap(node["discriminator"]); disc != nil {
		out["discriminator"] = str(disc["propertyName"])
	}
	if props := asMap(node["properties"]); props != nil {
		rendered := map[string]any{}
		for k, p := range props {
			rendered[k] = s.render(p, depth, opts, stack)
		}
		out["properties"] = rendered
	}
	if items, ok := node["items"]; ok {
		out["items"] = s.render(items, depth, opts, stack)
	}
	if ap, ok := node["additionalProperties"].(map[string]any); ok {
		out["additionalProperties"] = s.render(ap, depth, opts, stack)
	}
	for _, k := range []string{"oneOf", "anyOf", "allOf"} {
		if list := asSlice(node[k]); list != nil {
			rendered := make([]any, 0, len(list))
			for _, item := range list {
				rendered = append(rendered, s.render(item, depth, opts, stack))
			}
			out[k] = rendered
		}
	}
	return out
}

func truncateText(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	cut := s[:n]
	// don't split a UTF-8 sequence
	for len(cut) > 0 && cut[len(cut)-1]&0xC0 == 0x80 {
		cut = cut[:len(cut)-1]
	}
	if len(cut) > 0 && cut[len(cut)-1]&0xC0 == 0xC0 {
		cut = cut[:len(cut)-1]
	}
	return cut + "…"
}
