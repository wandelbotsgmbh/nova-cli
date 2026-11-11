package virtualcontrollers

import (
	"context"
	"encoding/json"
	"net/url"
	"sort"
	"strconv"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/novaapi"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/tool"
)

type ioDescription struct {
	IO        string `json:"io"`
	Name      string `json:"name"`
	Group     string `json:"group"`
	Direction string `json:"direction"`
	ValueType string `json:"value_type"`
}

type ioEntry struct {
	IO        string `json:"io" jsonschema:"I/O id, used by set_virtual_io_values"`
	Name      string `json:"name,omitempty"`
	Group     string `json:"group,omitempty"`
	Direction string `json:"direction" jsonschema:"input or output"`
	ValueType string `json:"value_type" jsonschema:"boolean, integer or float"`
	Value     any    `json:"value,omitempty" jsonschema:"current value"`
}

var valueTypes = map[string]string{
	"IO_VALUE_BOOLEAN":        "boolean",
	"IO_VALUE_ANALOG_INTEGER": "integer",
	"IO_VALUE_ANALOG_FLOAT":   "float",
}

func direction(apiDirection string) string {
	if apiDirection == "IO_TYPE_OUTPUT" {
		return "output"
	}
	return "input"
}

func (c ioListContext) describe(ctx context.Context, ios []string) ([]ioDescription, *novaapi.Error) {
	query := url.Values{}
	for _, io := range ios {
		query.Add("ios", io)
	}
	descriptions := []ioDescription{}
	_, err := c.client.JSON(ctx, novaapi.Call{
		Tool: c.tool, Method: "GET", Path: basePath(c.cell, c.controller) + "/ios/description", Query: query,
		NotFound: controllerNotFound(c.cell, c.controller),
	}, &descriptions)
	return descriptions, err
}

type ioListContext struct {
	client                 *novaapi.Client
	tool, cell, controller string
}

type listIOsInput struct {
	Cell       string `json:"cell" jsonschema:"cell name (see list_cells)"`
	Controller string `json:"controller" jsonschema:"virtual controller name (see list_controllers)"`
	Direction  string `json:"direction,omitempty" jsonschema:"input or output; empty lists both"`
	Search     string `json:"search,omitempty" jsonschema:"only I/Os whose id, name or group contains this text (case-insensitive)"`
	tool.PageInput
}

func (in listIOsInput) target() (string, string) { return in.Cell, in.Controller }

type listIOsOutput struct {
	IOs []ioEntry `json:"ios"`
	tool.PageInfo
}

func listIOsTool(c *novaapi.Client) tool.Registration {
	t := &sdk.Tool{
		Name: "list_virtual_ios",
		Description: "List the simulated I/Os of a virtual controller with their current values, paginated; " +
			"narrow with direction and search. Refused for physical controllers.",
		Annotations: tool.Read("List virtual I/Os"),
	}
	return add(c, true, t, func(ctx context.Context, _ *sdk.CallToolRequest, in listIOsInput) (*sdk.CallToolResult, listIOsOutput, error) {
		if in.Direction != "" && in.Direction != "input" && in.Direction != "output" {
			return tool.Invalid[listIOsOutput](t.Name, "'direction' must be input or output")
		}
		lc := ioListContext{client: c, tool: t.Name, cell: in.Cell, controller: in.Controller}
		descriptions, err := lc.describe(ctx, nil)
		if err != nil {
			return tool.Fail[listIOsOutput](err)
		}
		search := strings.ToLower(in.Search)
		var matching []ioEntry
		for _, d := range descriptions {
			e := ioEntry{IO: d.IO, Name: d.Name, Group: d.Group, Direction: direction(d.Direction), ValueType: valueTypes[d.ValueType]}
			if in.Direction != "" && e.Direction != in.Direction {
				continue
			}
			if search != "" && !strings.Contains(strings.ToLower(d.IO+" "+d.Name+" "+d.Group), search) {
				continue
			}
			matching = append(matching, e)
		}
		sort.Slice(matching, func(i, j int) bool { return matching[i].IO < matching[j].IO })
		page, info := tool.Page(matching, in.PageInput)
		if len(page) > 0 {
			query := url.Values{}
			for _, e := range page {
				query.Add("ios", e.IO)
			}
			var values []struct {
				IO        string          `json:"io"`
				Value     json.RawMessage `json:"value"`
				ValueType string          `json:"value_type"`
			}
			if _, err := c.JSON(ctx, novaapi.Call{
				Tool: t.Name, Method: "GET", Path: basePath(in.Cell, in.Controller) + "/ios/values", Query: query,
				NotFound: controllerNotFound(in.Cell, in.Controller),
			}, &values); err != nil {
				return tool.Fail[listIOsOutput](err)
			}
			byIO := map[string]any{}
			for _, v := range values {
				var value any
				if json.Unmarshal(v.Value, &value) == nil {
					byIO[v.IO] = value
				}
			}
			for i := range page {
				page[i].Value = byIO[page[i].IO]
			}
		}
		return tool.OK("virtual I/Os listed", info.Fields(map[string]any{"controller": in.Controller}), listIOsOutput{IOs: page, PageInfo: info})
	})
}

type ioValueInput struct {
	IO    string `json:"io" jsonschema:"I/O id (see list_virtual_ios)"`
	Value string `json:"value" jsonschema:"true/false for boolean I/Os, an integer or a number otherwise"`
}

type setIOValuesInput struct {
	Cell       string         `json:"cell" jsonschema:"cell name (see list_cells)"`
	Controller string         `json:"controller" jsonschema:"virtual controller name (see list_controllers)"`
	Values     []ioValueInput `json:"values" jsonschema:"I/Os to set; the value type is looked up per I/O"`
}

func (in setIOValuesInput) target() (string, string) { return in.Cell, in.Controller }

func setIOValuesTool(c *novaapi.Client) tool.Registration {
	t := &sdk.Tool{
		Name: "set_virtual_io_values",
		Description: "Set simulated I/O values (inputs or outputs) of a virtual controller, e.g. to simulate a " +
			"sensor signal. Refused for physical controllers.",
		Annotations: tool.Write("Set virtual I/O values", true),
	}
	return add(c, false, t, func(ctx context.Context, _ *sdk.CallToolRequest, in setIOValuesInput) (*sdk.CallToolResult, writeOutput, error) {
		if len(in.Values) == 0 {
			return tool.Invalid[writeOutput](t.Name, "'values' is required")
		}
		ios := make([]string, 0, len(in.Values))
		for _, v := range in.Values {
			if err := tool.Required(t.Name, "io", v.IO); err != nil {
				return tool.Fail[writeOutput](err)
			}
			ios = append(ios, v.IO)
		}
		lc := ioListContext{client: c, tool: t.Name, cell: in.Cell, controller: in.Controller}
		descriptions, err := lc.describe(ctx, ios)
		if err != nil {
			return tool.Fail[writeOutput](err)
		}
		types := map[string]string{}
		for _, d := range descriptions {
			types[d.IO] = valueTypes[d.ValueType]
		}
		body := make([]map[string]any, 0, len(in.Values))
		for _, v := range in.Values {
			valueType, ok := types[v.IO]
			if !ok {
				return tool.Invalid[writeOutput](t.Name, "I/O '%s' not found on virtual controller '%s', see list_virtual_ios", v.IO, in.Controller)
			}
			value, parseErr := parseIOValue(valueType, v.Value)
			if parseErr != "" {
				return tool.Invalid[writeOutput](t.Name, "I/O '%s': %s", v.IO, parseErr)
			}
			body = append(body, map[string]any{"io": v.IO, "value": value, "value_type": valueType})
		}
		status, apiErr := c.JSON(ctx, novaapi.Call{
			Tool: t.Name, Method: "PUT", Path: basePath(in.Cell, in.Controller) + "/ios/values", Body: body,
			NotFound: controllerNotFound(in.Cell, in.Controller),
		}, nil)
		if apiErr != nil {
			return tool.Fail[writeOutput](apiErr)
		}
		out := writeOutput{Controller: in.Controller, Status: novaapi.DeployStatus(status)}
		return tool.OK("virtual I/O values set", map[string]any{"controller": in.Controller, "count": len(body), "status": out.Status}, out)
	})
}

// parseIOValue converts the user's value to the API's representation (integers are
// strings there); it returns a message if the value doesn't fit the type.
func parseIOValue(valueType, raw string) (any, string) {
	raw = strings.TrimSpace(raw)
	switch valueType {
	case "boolean":
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return nil, "expects true or false"
		}
		return b, ""
	case "integer":
		if _, err := strconv.ParseInt(raw, 10, 64); err != nil {
			return nil, "expects an integer"
		}
		return raw, ""
	case "float":
		f, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return nil, "expects a number"
		}
		return f, ""
	}
	return nil, "has an unknown value type"
}
