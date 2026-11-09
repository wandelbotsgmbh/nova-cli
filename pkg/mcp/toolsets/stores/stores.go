// Package stores serves the opt-in `stores` toolset: the collision world (colliders,
// collision tools, link chains, collision setups) and the object store of a cell.
//
// One set of tools covers all five kinds through a `kind` argument. Reads return keys
// and short summaries, bodies only up to a size cap; writes validate the body shape
// before sending. There is deliberately no clear-all.
package stores

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/google/jsonschema-go/jsonschema"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/novaapi"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/tool"
)

// Tools returns the tools of this package.
func Tools(env tool.Env) []tool.Registration {
	c := env.Client
	return []tool.Registration{listItems(c), getItem(c), putItem(c), deleteItem(c)}
}

// Store kinds.
const (
	KindCollider       = "collider"
	KindCollisionTool  = "collision_tool"
	KindLinkChain      = "link_chain"
	KindCollisionSetup = "collision_setup"
	KindObject         = "object"
)

var kinds = []string{KindCollider, KindCollisionTool, KindLinkChain, KindCollisionSetup, KindObject}

// collisionSegment is the path segment of a collision kind below /store/collision.
var collisionSegment = map[string]string{
	KindCollider:       "colliders",
	KindCollisionTool:  "tools",
	KindLinkChain:      "link-chains",
	KindCollisionSetup: "setups",
}

const (
	// maxBodyBytes caps a body returned by get_store_item (compact JSON or text).
	maxBodyBytes = 32 << 10
	// maxPutBytes caps a body sent by put_store_item.
	maxPutBytes = 1 << 20
)

// protectedPrefixes are object keys owned by other services: writing them directly
// would bypass their caches and change events.
var protectedPrefixes = map[string]string{
	"trees/":    "NovaFlow keeps its flows there; change flows through NovaFlow",
	"datasets/": "the datasets API keeps its revisions there; use the datasets tools",
}

func kindDescription() string {
	return "one of: " + strings.Join(kinds, ", ") +
		" (collider: one shape; collision_tool: colliders on the flange; link_chain: colliders per robot link; " +
		"collision_setup: colliders, link chain and tool checked together by the planner; object: any file in the cell's object store)"
}

func checkKind(toolName, kind string) *novaapi.Error {
	if !slices.Contains(kinds, kind) {
		return &novaapi.Error{Code: novaapi.CodeInvalidArgument, Message: fmt.Sprintf("%s: unknown kind '%s', %s", toolName, kind, "use one of: "+strings.Join(kinds, ", "))}
	}
	return nil
}

func storePath(cell string) string {
	return "/cells/" + url.PathEscape(cell) + "/store"
}

// objectPath keeps "/" in keys unescaped, as the object store expects (see
// pkg/novaflow).
func objectPath(cell, key string) string {
	segs := strings.Split(key, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	return storePath(cell) + "/objects/" + strings.Join(segs, "/")
}

func itemPath(cell, kind, key string) string {
	if kind == KindObject {
		return objectPath(cell, key)
	}
	return storePath(cell) + "/collision/" + collisionSegment[kind] + "/" + url.PathEscape(key)
}

func checkKey(toolName, kind, key string) *novaapi.Error {
	if err := tool.Required(toolName, "key", key); err != nil {
		return err
	}
	if kind == KindObject {
		if strings.HasPrefix(key, "/") || strings.HasSuffix(key, "/") || strings.Contains(key, "//") {
			return &novaapi.Error{Code: novaapi.CodeInvalidArgument, Message: fmt.Sprintf("%s: object key '%s' must not start or end with '/' or contain '//'", toolName, key)}
		}
		for _, s := range strings.Split(key, "/") {
			if s == "." || s == ".." {
				return &novaapi.Error{Code: novaapi.CodeInvalidArgument, Message: fmt.Sprintf("%s: object key '%s' must not contain '.' or '..' segments", toolName, key)}
			}
		}
		return nil
	}
	if strings.Contains(key, "/") {
		return &novaapi.Error{Code: novaapi.CodeInvalidArgument, Message: fmt.Sprintf("%s: %s key '%s' must not contain '/'", toolName, kind, key)}
	}
	return nil
}

func checkWritable(toolName, kind, key string) *novaapi.Error {
	if kind != KindObject {
		return nil
	}
	for prefix, why := range protectedPrefixes {
		if strings.HasPrefix(key, prefix) {
			return &novaapi.Error{Code: novaapi.CodeForbidden, Message: fmt.Sprintf("%s: object '%s' is not written through this tool: %s", toolName, key, why)}
		}
	}
	return nil
}

// --- summaries ---

// Summary is a short description of a stored item.
type Summary struct {
	Key     string `json:"key"`
	Summary string `json:"summary,omitempty" jsonschema:"shape types and counts; empty for objects (see get_store_item)"`
}

type apiCollider struct {
	Shape struct {
		ShapeType string `json:"shape_type"`
	} `json:"shape"`
	Margin *float64 `json:"margin"`
}

type apiCollisionSetup struct {
	Colliders              map[string]apiCollider   `json:"colliders"`
	LinkChain              []map[string]apiCollider `json:"link_chain"`
	Tool                   map[string]apiCollider   `json:"tool"`
	SelfCollisionDetection *bool                    `json:"self_collision_detection"`
}

func shapeCounts(colliders map[string]apiCollider) string {
	counts := map[string]int{}
	for _, c := range colliders {
		shape := c.Shape.ShapeType
		if shape == "" {
			shape = "unknown"
		}
		counts[shape]++
	}
	shapes := make([]string, 0, len(counts))
	for shape := range counts {
		shapes = append(shapes, shape)
	}
	sort.Strings(shapes)
	parts := make([]string, len(shapes))
	for i, shape := range shapes {
		parts[i] = fmt.Sprintf("%d %s", counts[shape], shape)
	}
	return strings.Join(parts, ", ")
}

func linkChainSummary(chain []map[string]apiCollider) string {
	colliders := 0
	for _, link := range chain {
		colliders += len(link)
	}
	return fmt.Sprintf("%d links, %d colliders", len(chain), colliders)
}

func summarize(kind string, raw json.RawMessage) string {
	switch kind {
	case KindCollider:
		var c apiCollider
		if json.Unmarshal(raw, &c) != nil {
			return ""
		}
		s := c.Shape.ShapeType
		if c.Margin != nil && *c.Margin != 0 {
			s += fmt.Sprintf(", margin %g mm", *c.Margin)
		}
		return s
	case KindCollisionTool:
		var t map[string]apiCollider
		if json.Unmarshal(raw, &t) != nil {
			return ""
		}
		return fmt.Sprintf("%d colliders (%s)", len(t), shapeCounts(t))
	case KindLinkChain:
		var chain []map[string]apiCollider
		if json.Unmarshal(raw, &chain) != nil {
			return ""
		}
		return linkChainSummary(chain)
	case KindCollisionSetup:
		var setup apiCollisionSetup
		if json.Unmarshal(raw, &setup) != nil {
			return ""
		}
		parts := []string{fmt.Sprintf("%d colliders", len(setup.Colliders))}
		if len(setup.LinkChain) > 0 {
			parts = append(parts, "link chain: "+linkChainSummary(setup.LinkChain))
		}
		if len(setup.Tool) > 0 {
			parts = append(parts, fmt.Sprintf("tool: %d colliders", len(setup.Tool)))
		}
		if setup.SelfCollisionDetection != nil && !*setup.SelfCollisionDetection {
			parts = append(parts, "self-collision off")
		}
		return strings.Join(parts, "; ")
	}
	return ""
}

// --- list_store_items ---

type listInput struct {
	Cell   string `json:"cell" jsonschema:"cell name (see list_cells)"`
	Kind   string `json:"kind" jsonschema:"store kind"`
	Prefix string `json:"prefix,omitempty" jsonschema:"only keys starting with this prefix"`
	tool.PageInput
}

type listOutput struct {
	Items []Summary `json:"items"`
	tool.PageInfo
}

func listItems(c *novaapi.Client) tool.Registration {
	const name = "list_store_items"
	add := func(s *sdk.Server) {
		sdk.AddTool(s, &sdk.Tool{
			Name: name,
			Description: "List the keys of a cell's collision world (colliders, collision tools, link chains, collision " +
				"setups) or object store, with a short summary for collision items. Sorted by key, paginated.",
			Annotations: tool.Read("List store items"),
			InputSchema: inputSchema[listInput](),
		}, func(ctx context.Context, _ *sdk.CallToolRequest, in listInput) (*sdk.CallToolResult, listOutput, error) {
			if err := tool.Required(name, "cell", in.Cell, "kind", in.Kind); err != nil {
				return tool.Fail[listOutput](err)
			}
			if err := checkKind(name, in.Kind); err != nil {
				return tool.Fail[listOutput](err)
			}
			items := []Summary{}
			if in.Kind == KindObject {
				var keys []string
				_, err := c.JSON(ctx, novaapi.Call{Tool: name, Method: http.MethodGet, Path: storePath(in.Cell) + "/objects"}, &keys)
				if err != nil && err.Code != novaapi.CodeNotFound { // the store is created on first write
					return tool.Fail[listOutput](err)
				}
				for _, key := range keys {
					items = append(items, Summary{Key: key})
				}
			} else {
				var all map[string]json.RawMessage
				if err := c.Get(ctx, name, storePath(in.Cell)+"/collision/"+collisionSegment[in.Kind],
					fmt.Sprintf("cell '%s' not found", in.Cell), &all); err != nil {
					return tool.Fail[listOutput](err)
				}
				for key, raw := range all {
					items = append(items, Summary{Key: key, Summary: summarize(in.Kind, raw)})
				}
			}
			filtered := items[:0]
			for _, item := range items {
				if strings.HasPrefix(item.Key, in.Prefix) {
					filtered = append(filtered, item)
				}
			}
			sort.Slice(filtered, func(i, j int) bool { return filtered[i].Key < filtered[j].Key })
			window, page := tool.Page(filtered, in.PageInput)
			return tool.OK("store items listed", page.Fields(map[string]any{"cell": in.Cell, "kind": in.Kind}),
				listOutput{Items: window, PageInfo: page})
		})
	}
	return tool.Registration{Toolset: tool.Stores, ReadOnly: true, Add: add}
}

// --- get_store_item ---

type itemInput struct {
	Cell string `json:"cell" jsonschema:"cell name (see list_cells)"`
	Kind string `json:"kind" jsonschema:"store kind"`
	Key  string `json:"key" jsonschema:"item key (see list_store_items); object keys may contain '/'"`
}

type getOutput struct {
	Key          string            `json:"key"`
	Kind         string            `json:"kind"`
	Summary      string            `json:"summary,omitempty"`
	Size         int               `json:"size" jsonschema:"bytes"`
	Body         any               `json:"body,omitempty" jsonschema:"collision item as JSON, or object content parsed as JSON; omitted when larger than 32 KiB"`
	Content      string            `json:"content,omitempty" jsonschema:"object content as text when it is UTF-8 but not JSON, up to 32 KiB"`
	ContentType  string            `json:"content_type,omitempty"`
	LastModified string            `json:"last_modified,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty" jsonschema:"object metadata"`
	BodyOmitted  string            `json:"body_omitted,omitempty" jsonschema:"why the body is not included"`
}

// parseMetadata reads the X-Metadata header (style simple, explode false:
// "k1,v1,k2,v2").
func parseMetadata(header string) map[string]string {
	if strings.TrimSpace(header) == "" {
		return nil
	}
	parts := strings.Split(header, ",")
	metadata := map[string]string{}
	for i := 0; i+1 < len(parts); i += 2 {
		// the store answers with form-encoded values ("a b" comes back as "a+b")
		value := strings.TrimSpace(parts[i+1])
		if decoded, err := url.QueryUnescape(value); err == nil {
			value = decoded
		}
		metadata[strings.TrimSpace(parts[i])] = value
	}
	return metadata
}

func getItem(c *novaapi.Client) tool.Registration {
	const name = "get_store_item"
	add := func(s *sdk.Server) {
		sdk.AddTool(s, &sdk.Tool{
			Name: name,
			Description: "Read one item of a cell's collision world or object store. Collision items come as JSON; objects " +
				"come with size, type and metadata, and their content only if it is text or JSON up to 32 KiB.",
			Annotations: tool.Read("Get store item"),
			InputSchema: inputSchema[itemInput](),
		}, func(ctx context.Context, _ *sdk.CallToolRequest, in itemInput) (*sdk.CallToolResult, getOutput, error) {
			if err := tool.Required(name, "cell", in.Cell, "kind", in.Kind); err != nil {
				return tool.Fail[getOutput](err)
			}
			if err := checkKind(name, in.Kind); err != nil {
				return tool.Fail[getOutput](err)
			}
			if err := checkKey(name, in.Kind, in.Key); err != nil {
				return tool.Fail[getOutput](err)
			}
			notFound := fmt.Sprintf("%s '%s' not found in cell '%s'", in.Kind, in.Key, in.Cell)
			out := getOutput{Key: in.Key, Kind: in.Kind}
			if in.Kind != KindObject {
				res, err := c.Do(ctx, novaapi.Call{Tool: name, Method: http.MethodGet, Path: itemPath(in.Cell, in.Kind, in.Key), NotFound: notFound})
				if err != nil {
					return tool.Fail[getOutput](err)
				}
				var compact bytes.Buffer
				if json.Compact(&compact, res.Body) != nil {
					return tool.Fail[getOutput](c.UnexpectedAnswer(ctx, name, res.Status, res.Body))
				}
				out.Size = compact.Len()
				out.Summary = summarize(in.Kind, compact.Bytes())
				if compact.Len() > maxBodyBytes {
					out.BodyOmitted = fmt.Sprintf("%d bytes is above the 32 KiB limit; the summary describes it", compact.Len())
				} else {
					out.Body = json.RawMessage(compact.Bytes())
				}
				return tool.OK("store item read", map[string]any{"kind": in.Kind, "key": in.Key, "size": out.Size}, out)
			}

			head, err := c.Do(ctx, novaapi.Call{Tool: name, Method: http.MethodHead, Path: itemPath(in.Cell, in.Kind, in.Key), NotFound: notFound})
			if err != nil {
				return tool.Fail[getOutput](err)
			}
			out.ContentType = head.Header.Get("Content-Type")
			out.LastModified = head.Header.Get("Last-Modified")
			out.Metadata = parseMetadata(head.Header.Get("X-Metadata"))
			size, sizeErr := strconv.Atoi(head.Header.Get("Content-Length"))
			if sizeErr != nil || size > maxBodyBytes {
				out.Size = size
				out.BodyOmitted = "the object is larger than 32 KiB (or of unknown size); only its metadata is returned"
				return tool.OK("object metadata read", map[string]any{"key": in.Key, "size": size}, out)
			}
			res, err := c.Do(ctx, novaapi.Call{Tool: name, Method: http.MethodGet, Path: itemPath(in.Cell, in.Kind, in.Key), NotFound: notFound})
			if err != nil {
				return tool.Fail[getOutput](err)
			}
			body := res.Body
			if len(body) > maxBodyBytes {
				body = nil
			}
			out.Size = len(res.Body)
			var compact bytes.Buffer
			switch {
			case body == nil:
				out.BodyOmitted = "the object is larger than 32 KiB; only its metadata is returned"
			case json.Valid(body) && json.Compact(&compact, body) == nil:
				out.Body = json.RawMessage(compact.Bytes())
			case utf8.Valid(body):
				out.Content = string(body)
			default:
				out.BodyOmitted = "binary content is not returned"
			}
			return tool.OK("object read", map[string]any{"key": in.Key, "size": out.Size}, out)
		})
	}
	return tool.Registration{Toolset: tool.Stores, ReadOnly: true, Add: add}
}

// shapeFields are the required fields per shape_type besides shape_type (26.8 spec).
var shapeFields = map[string][]string{
	"sphere":              {"radius"},
	"box":                 {"size_x", "size_y", "size_z", "box_type"},
	"rectangle":           {"size_x", "size_y"},
	"plane":               {},
	"cylinder":            {"radius", "height"},
	"capsule":             {"radius", "cylinder_height"},
	"rectangular_capsule": {"radius", "sphere_center_distance_x", "sphere_center_distance_y"},
	"convex_hull":         {"vertices"},
}

func shapeTypes() []string {
	types := make([]string, 0, len(shapeFields))
	for t := range shapeFields {
		types = append(types, t)
	}
	sort.Strings(types)
	return types
}

// --- put_store_item ---

type putInput struct {
	Cell     string            `json:"cell" jsonschema:"cell name (see list_cells)"`
	Kind     string            `json:"kind" jsonschema:"store kind"`
	Key      string            `json:"key" jsonschema:"item key; object keys may contain '/'"`
	Body     any               `json:"body,omitempty" jsonschema:"collision kinds: the item as JSON (collider and collision_setup: object, collision_tool: object of colliders, link_chain: array of objects of colliders). object: any JSON value to store as JSON"`
	Content  string            `json:"content,omitempty" jsonschema:"object only: text to store as is, instead of body"`
	Metadata map[string]string `json:"metadata,omitempty" jsonschema:"object only: metadata names and values (no commas)"`
}

type putOutput struct {
	Key  string `json:"key"`
	Kind string `json:"kind"`
	Size int    `json:"size" jsonschema:"bytes sent"`
}

func validateCollisionBody(name, kind string, raw []byte) *novaapi.Error {
	invalid := func(format string, args ...any) *novaapi.Error {
		return &novaapi.Error{Code: novaapi.CodeInvalidArgument, Message: name + ": " + fmt.Sprintf(format, args...)}
	}
	checkCollider := func(field string, c map[string]json.RawMessage) *novaapi.Error {
		var shape map[string]json.RawMessage
		var shapeType string
		if json.Unmarshal(c["shape"], &shape) != nil || json.Unmarshal(shape["shape_type"], &shapeType) != nil {
			return invalid("%s needs a 'shape' object with a 'shape_type' (%s)", field, strings.Join(shapeTypes(), ", "))
		}
		required, ok := shapeFields[shapeType]
		if !ok {
			return invalid("%s.shape: unknown shape_type '%s' (%s)", field, shapeType, strings.Join(shapeTypes(), ", "))
		}
		// the store accepts incomplete shapes, the planner fails on them later
		for _, f := range required {
			if _, ok := shape[f]; !ok {
				return invalid("%s.shape: a %s needs %s", field, shapeType, strings.Join(required, ", "))
			}
		}
		return nil
	}
	checkColliders := func(field string, raw json.RawMessage) *novaapi.Error {
		var colliders map[string]map[string]json.RawMessage
		if json.Unmarshal(raw, &colliders) != nil {
			return invalid("%s must be an object of colliders keyed by name", field)
		}
		for key, c := range colliders {
			if err := checkCollider(field+"."+key, c); err != nil {
				return err
			}
		}
		return nil
	}
	switch kind {
	case KindCollider:
		var c map[string]json.RawMessage
		if json.Unmarshal(raw, &c) != nil {
			return invalid("a collider body must be a JSON object")
		}
		return checkCollider("body", c)
	case KindCollisionTool:
		return checkColliders("body", raw)
	case KindLinkChain:
		var chain []json.RawMessage
		if json.Unmarshal(raw, &chain) != nil {
			return invalid("a link_chain body must be a JSON array with one object of colliders per link")
		}
		for i, link := range chain {
			if err := checkColliders(fmt.Sprintf("body[%d]", i), link); err != nil {
				return err
			}
		}
	case KindCollisionSetup:
		var setup map[string]json.RawMessage
		if json.Unmarshal(raw, &setup) != nil {
			return invalid("a collision_setup body must be a JSON object")
		}
		for field := range setup {
			if !slices.Contains([]string{"colliders", "link_chain", "tool", "self_collision_detection"}, field) {
				return invalid("unknown collision_setup field '%s' (colliders, link_chain, tool, self_collision_detection)", field)
			}
		}
		if raw, ok := setup["colliders"]; ok {
			if err := checkColliders("body.colliders", raw); err != nil {
				return err
			}
		}
		if raw, ok := setup["tool"]; ok {
			if err := checkColliders("body.tool", raw); err != nil {
				return err
			}
		}
		if raw, ok := setup["link_chain"]; ok {
			return validateCollisionBody(name, KindLinkChain, raw)
		}
	}
	return nil
}

func putItem(c *novaapi.Client) tool.Registration {
	const name = "put_store_item"
	add := func(s *sdk.Server) {
		sdk.AddTool(s, &sdk.Tool{
			Name: name,
			Description: "Store an item in a cell's collision world or object store, REPLACING any item with the same key " +
				"(check with get_store_item first and confirm with the user before overwriting). Up to 1 MiB.",
			Annotations: tool.Annotations("Put store item", false, true, true, false),
			InputSchema: inputSchema[putInput](),
		}, func(ctx context.Context, _ *sdk.CallToolRequest, in putInput) (*sdk.CallToolResult, putOutput, error) {
			if err := tool.Required(name, "cell", in.Cell, "kind", in.Kind); err != nil {
				return tool.Fail[putOutput](err)
			}
			if err := checkKind(name, in.Kind); err != nil {
				return tool.Fail[putOutput](err)
			}
			if err := checkKey(name, in.Kind, in.Key); err != nil {
				return tool.Fail[putOutput](err)
			}
			if err := checkWritable(name, in.Kind, in.Key); err != nil {
				return tool.Fail[putOutput](err)
			}
			out := putOutput{Key: in.Key, Kind: in.Kind}
			if in.Kind != KindObject {
				if in.Content != "" || len(in.Metadata) > 0 {
					return tool.Invalid[putOutput](name, "'content' and 'metadata' are for objects only; send collision items as 'body'")
				}
				if in.Body == nil {
					return tool.Invalid[putOutput](name, "'body' is required")
				}
				raw, err := json.Marshal(in.Body)
				if err != nil {
					return tool.Invalid[putOutput](name, "'body' is not valid JSON")
				}
				if len(raw) > maxPutBytes {
					return tool.Invalid[putOutput](name, "'body' is %d bytes, the limit is 1 MiB", len(raw))
				}
				if err := validateCollisionBody(name, in.Kind, raw); err != nil {
					return tool.Fail[putOutput](err)
				}
				if _, err := c.JSON(ctx, novaapi.Call{
					Tool: name, Method: http.MethodPut, Path: itemPath(in.Cell, in.Kind, in.Key), Body: json.RawMessage(raw),
					NotFound: fmt.Sprintf("cell '%s' not found", in.Cell),
				}, nil); err != nil {
					return tool.Fail[putOutput](err)
				}
				out.Size = len(raw)
				return tool.OK("store item saved", map[string]any{"cell": in.Cell, "kind": in.Kind, "key": in.Key}, out)
			}

			var data []byte
			switch {
			case in.Content != "" && in.Body != nil:
				return tool.Invalid[putOutput](name, "pass either 'content' (text) or 'body' (JSON), not both")
			case in.Content != "":
				data = []byte(in.Content)
			case in.Body != nil:
				raw, err := json.Marshal(in.Body)
				if err != nil {
					return tool.Invalid[putOutput](name, "'body' is not valid JSON")
				}
				data = raw
			default:
				return tool.Invalid[putOutput](name, "'content' or 'body' is required")
			}
			if len(data) > maxPutBytes {
				return tool.Invalid[putOutput](name, "the object is %d bytes, the limit is 1 MiB", len(data))
			}
			for k, v := range in.Metadata {
				if k == "" || strings.Contains(k, ",") || strings.Contains(v, ",") {
					return tool.Invalid[putOutput](name, "metadata names must be non-empty and names and values must not contain commas")
				}
			}
			if err := putObject(ctx, c, name, in.Cell, in.Key, data, in.Metadata); err != nil {
				return tool.Fail[putOutput](err)
			}
			out.Size = len(data)
			return tool.OK("object saved", map[string]any{"cell": in.Cell, "key": in.Key, "size": out.Size}, out)
		})
	}
	return tool.Registration{Toolset: tool.Stores, ReadOnly: false, Add: add}
}

// putObject sends the multipart upload the object store expects (field "any_value").
func putObject(ctx context.Context, c *novaapi.Client, name, cell, key string, data []byte, metadata map[string]string) *novaapi.Error {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	filename := key[strings.LastIndex(key, "/")+1:]
	part, err := w.CreateFormFile("any_value", filename)
	if err == nil {
		_, err = part.Write(data)
	}
	if err == nil {
		err = w.Close()
	}
	if err != nil {
		return &novaapi.Error{Code: novaapi.CodeInternal, Message: name + ": could not build the upload"}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.BaseURL()+objectPath(cell, key), &buf)
	if err != nil {
		return &novaapi.Error{Code: novaapi.CodeInvalidArgument, Message: fmt.Sprintf("%s: invalid object key '%s'", name, key)}
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Accept", "application/json")
	if len(metadata) > 0 {
		names := make([]string, 0, len(metadata))
		for k := range metadata {
			names = append(names, k)
		}
		sort.Strings(names)
		pairs := make([]string, 0, 2*len(names))
		for _, k := range names {
			pairs = append(pairs, k, metadata[k])
		}
		req.Header.Set("X-Metadata", strings.Join(pairs, ","))
	}
	if apiErr := c.Authorize(ctx, name, req); apiErr != nil {
		return apiErr
	}
	resp, err := c.HTTPClient().Do(req)
	if err != nil {
		c.Logger().ErrorContext(ctx, "NOVA API call failed", "tool", name, "error", err)
		return &novaapi.Error{Code: novaapi.CodeUnavailable, Message: fmt.Sprintf("%s: cannot reach the NOVA instance: %v", name, err)}
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return nil
	case resp.StatusCode == http.StatusBadRequest:
		return &novaapi.Error{Code: novaapi.CodeInvalidArgument, Message: name + ": " + apiMessage(body, "the NOVA API rejected the object")}
	case resp.StatusCode == http.StatusUnauthorized:
		return &novaapi.Error{Code: novaapi.CodeUnauthenticated, Message: name + ": the instance rejected the login, run 'nova login' (cloud) or check the basic auth of the instance"}
	case resp.StatusCode == http.StatusForbidden:
		return &novaapi.Error{Code: novaapi.CodeForbidden, Message: name + ": " + apiMessage(body, "this operation is not allowed")}
	case resp.StatusCode == http.StatusNotFound:
		return &novaapi.Error{Code: novaapi.CodeNotFound, Message: fmt.Sprintf("%s: cell '%s' not found", name, cell)}
	}
	return c.UnexpectedAnswer(ctx, name, resp.StatusCode, body)
}

func apiMessage(body []byte, fallback string) string {
	var apiErr struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &apiErr) != nil || apiErr.Message == "" {
		return fallback
	}
	return apiErr.Message
}

// --- delete_store_item ---

type deleteOutput struct {
	Key  string `json:"key"`
	Kind string `json:"kind"`
}

func deleteItem(c *novaapi.Client) tool.Registration {
	const name = "delete_store_item"
	add := func(s *sdk.Server) {
		sdk.AddTool(s, &sdk.Tool{
			Name: name,
			Description: "Delete one item of a cell's collision world or object store. DESTRUCTIVE and not undoable: " +
				"confirm with the user first, naming cell, kind and key. Other apps may rely on the item.",
			Annotations: tool.Destructive("Delete store item"),
			InputSchema: inputSchema[itemInput](),
		}, func(ctx context.Context, _ *sdk.CallToolRequest, in itemInput) (*sdk.CallToolResult, deleteOutput, error) {
			if err := tool.Required(name, "cell", in.Cell, "kind", in.Kind); err != nil {
				return tool.Fail[deleteOutput](err)
			}
			if err := checkKind(name, in.Kind); err != nil {
				return tool.Fail[deleteOutput](err)
			}
			if err := checkKey(name, in.Kind, in.Key); err != nil {
				return tool.Fail[deleteOutput](err)
			}
			if err := checkWritable(name, in.Kind, in.Key); err != nil {
				return tool.Fail[deleteOutput](err)
			}
			notFound := fmt.Sprintf("%s '%s' not found in cell '%s'", in.Kind, in.Key, in.Cell)
			// both stores answer 204 for unknown keys too (verified live), so check first
			check := http.MethodGet
			if in.Kind == KindObject {
				check = http.MethodHead
			}
			if _, err := c.Do(ctx, novaapi.Call{Tool: name, Method: check, Path: itemPath(in.Cell, in.Kind, in.Key), NotFound: notFound}); err != nil {
				return tool.Fail[deleteOutput](err)
			}
			if _, err := c.JSON(ctx, novaapi.Call{Tool: name, Method: http.MethodDelete, Path: itemPath(in.Cell, in.Kind, in.Key), NotFound: notFound}, nil); err != nil {
				return tool.Fail[deleteOutput](err)
			}
			return tool.OK("store item deleted", map[string]any{"cell": in.Cell, "kind": in.Kind, "key": in.Key},
				deleteOutput{Key: in.Key, Kind: in.Kind})
		})
	}
	return tool.Registration{Toolset: tool.Stores, ReadOnly: false, Add: add}
}

// inputSchema infers the input schema of T and restricts "kind" to the store kinds.
func inputSchema[T any]() *jsonschema.Schema {
	schema, err := jsonschema.For[T](nil)
	if err != nil {
		panic(fmt.Sprintf("stores: input schema: %v", err))
	}
	if kind, ok := schema.Properties["kind"]; ok {
		kind.Enum = make([]any, len(kinds))
		for i, k := range kinds {
			kind.Enum[i] = k
		}
		kind.Description = kindDescription()
	}
	return schema
}
