package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/pb33f/libopenapi"
	v3 "github.com/pb33f/libopenapi/datamodel/high/v3"
	"github.com/wandelbotsgmbh/nova-cli/pkg/httpx"
	"github.com/wandelbotsgmbh/nova-cli/pkg/nova"
	"github.com/wandelbotsgmbh/nova-cli/pkg/oauth"
)

// Define a struct to hold operation context for the handler
type operationHandler struct {
	Method      string
	Path        string
	BaseURL     string
	OperationID string // Can be empty
}

// runOpenAPIMCPServer loads an OpenAPI specification and starts an MCP server.
func RunOpenAPIMCPServer(host string) error {
	specContent, err := nova.GetOpenAPISpec(host)
	if err != nil {
		return fmt.Errorf("failed to read spec response body from %s: %w", host, err)
	}

	// Parse the OpenAPI specification
	document, err := libopenapi.NewDocument(specContent)
	if err != nil {
		// Print spec content to stderr on error, not stdout
		fmt.Fprintf(os.Stderr, "\n--- Spec Content Start ---\n%s\n--- Spec Content End ---\n\n", string(specContent))
		return fmt.Errorf("failed to parse OpenAPI document from %s: %w", host, err)
	}

	// Build the model
	docModel, err := document.BuildV3Model()
	if err != nil {
		return fmt.Errorf("errors building OpenAPI model from %s: %w", host, err)
	}
	if docModel == nil {
		return fmt.Errorf("failed to build OpenAPI model from %s, but no errors reported", host)
	}

	serverName := "OpenAPI MCP Server"
	serverVersion := "1.0.0"
	if docModel.Model.Info != nil {
		if docModel.Model.Info.Title != "" {
			serverName = docModel.Model.Info.Title
		}
		if docModel.Model.Info.Version != "" {
			serverVersion = docModel.Model.Info.Version
		}
	}
	s := server.NewMCPServer(
		serverName,
		serverVersion,
	)

	// Determine base URL robustly
	defaultBaseURLStr := nova.HostWithProtocol(host)
	finalBaseURLStr := defaultBaseURLStr // Start with the default

	defaultBaseURL, err := url.Parse(defaultBaseURLStr)
	if err != nil {
		// This should not happen with the constructed default URL
		fmt.Fprintf(os.Stderr, "Warning: Failed to parse default base URL %s: %v\n", defaultBaseURLStr, err)
	}

	if len(docModel.Model.Servers) > 0 && docModel.Model.Servers[0].URL != "" {
		specServerURLStr := docModel.Model.Servers[0].URL
		specServerURL, err := url.Parse(specServerURLStr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: Failed to parse server URL from spec ('%s'): %v. Using default base URL.\n", specServerURLStr, err)
		} else {
			if specServerURL.Scheme != "" && specServerURL.Host != "" {
				// If spec URL is absolute, use it directly
				finalBaseURLStr = strings.TrimSuffix(specServerURL.String(), "/") // Ensure no trailing slash initially
				// fmt.Fprintf(os.Stderr, "Using absolute base URL from OpenAPI spec: %s\n", finalBaseURLStr) // Removed
			} else if defaultBaseURL != nil {
				// If spec URL is relative, resolve it against the default base URL
				resolvedURL := defaultBaseURL.ResolveReference(specServerURL)
				finalBaseURLStr = strings.TrimSuffix(resolvedURL.String(), "/") // Ensure no trailing slash
				// fmt.Fprintf(os.Stderr, "Resolved relative server URL ('%s') from spec against default base URL. Using: %s\n", specServerURLStr, finalBaseURLStr) // Removed
			} else {
				fmt.Fprintf(os.Stderr, "Warning: Cannot resolve relative server URL ('%s') because default base URL failed to parse. Using default base URL string.\n", specServerURLStr)
				finalBaseURLStr = strings.TrimSuffix(defaultBaseURLStr, "/") // Fallback, ensure no trailing slash
			}
		}
	} else {
		finalBaseURLStr = strings.TrimSuffix(defaultBaseURLStr, "/") // Ensure no trailing slash
		// fmt.Fprintf(os.Stderr, "No servers found in OpenAPI spec, or first server URL is empty. Using default base URL: %s\n", finalBaseURLStr) // Removed
	}

	// Generate tools from paths, passing the finalized absolute base URL
	err = generateToolsFromPaths(s, &docModel.Model, finalBaseURLStr)
	if err != nil {
		return fmt.Errorf("failed to generate tools from spec %s: %w", host, err)
	}

	// fmt.Printf("Starting %s v%s based on OpenAPI spec from %s...\n", serverName, serverVersion, host) // Removed: Interferes with MCP stdio
	// fmt.Println("Press Ctrl+C to stop") // Removed: Interferes with MCP stdio
	return server.ServeStdio(s)
}

// generateToolsFromPaths creates MCP tools from OpenAPI paths.
func generateToolsFromPaths(s *server.MCPServer, docModel *v3.Document, baseURL string) error {
	if docModel.Paths == nil || docModel.Paths.PathItems == nil {
		fmt.Println("Warning: No paths found in OpenAPI spec")
		return nil
	}

	// Iterate through paths using First/Next
	for pathPair := docModel.Paths.PathItems.First(); pathPair != nil; pathPair = pathPair.Next() {
		pathName := pathPair.Key()
		pathItem := pathPair.Value()

		// Local function to add tool and handler
		addOperation := func(method string, op *v3.Operation) {
			if op == nil {
				return
			}
			tool, err := createToolForOperation(method, pathName, op)
			if err != nil {
				fmt.Printf("Error creating tool for %s %s: %v\n", method, pathName, err)
				return
			}

			// Create handler instance with context
			handler := operationHandler{
				Method:      method,
				Path:        pathName,
				BaseURL:     baseURL,
				OperationID: op.OperationId, // May be empty
			}

			s.AddTool(*tool, handler.handleToolCall) // Pass method value
		}

		// Process operations
		addOperation("GET", pathItem.Get)
		addOperation("POST", pathItem.Post)
		addOperation("PUT", pathItem.Put)
		addOperation("DELETE", pathItem.Delete)
		addOperation("PATCH", pathItem.Patch)
		addOperation("OPTIONS", pathItem.Options) // Consider adding other methods if needed
		addOperation("HEAD", pathItem.Head)
		addOperation("TRACE", pathItem.Trace)
	}
	return nil
}

// createToolForOperation creates an MCP tool for a specific operation.
// Removed baseURL from parameters as it's now handled by the handler struct.
func createToolForOperation(method string, path string, operation *v3.Operation) (*mcp.Tool, error) {
	toolName := method
	if operation.OperationId != "" {
		toolName = operation.OperationId
	} else {
		segments := strings.Split(strings.Trim(path, "/"), "/")
		for _, segment := range segments {
			if segment != "" {
				if strings.HasPrefix(segment, "{") && strings.HasSuffix(segment, "}") {
					// Represent path parameters like /users/{userId} -> GetUsersByUserId
					paramName := strings.Trim(segment, "{}")
					toolName += "By" + strings.ToUpper(paramName[:1]) + paramName[1:]
				} else {
					toolName += strings.ToUpper(segment[:1]) + segment[1:]
				}
			}
		}
	}

	description := operation.Summary
	if description == "" && operation.Description != "" {
		description = operation.Description
	}
	if description == "" {
		description = fmt.Sprintf("Execute %s request on %s", method, path)
	}

	inputSchema := map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{
			// Parameters and request body properties added here
		},
		"required": []string{},
	}
	properties := inputSchema["properties"].(map[string]interface{})
	required := inputSchema["required"].([]string)

	// Add parameters (path, query, header, cookie) to the input schema
	if operation.Parameters != nil {
		for _, param := range operation.Parameters {
			paramName := param.Name
			paramSchemaMap := map[string]interface{}{ // Renamed to avoid conflict
				"type":        "string", // Default type
				"description": param.Description,
			}

			if param.Schema != nil && param.Schema.Schema() != nil {
				paramSchema := param.Schema.Schema()
				if len(paramSchema.Type) > 0 {
					paramSchemaMap["type"] = paramSchema.Type[0]
				}
				if paramSchema.Description != "" {
					// Prefer parameter description if schema description is missing
					if paramSchemaMap["description"] == "" {
						paramSchemaMap["description"] = paramSchema.Description
					}
				}
				// TODO: Consider adding other schema properties like format, enum, min/max, etc.
			}

			properties[paramName] = paramSchemaMap
			if param.Required != nil && *param.Required {
				required = append(required, paramName)
			}
		}
	}

	// Add request body to the input schema if it exists
	if operation.RequestBody != nil && operation.RequestBody.Content != nil {
		if jsonContent, ok := operation.RequestBody.Content.Get("application/json"); ok && jsonContent != nil && jsonContent.Schema != nil && jsonContent.Schema.Schema() != nil {
			bodySchema := jsonContent.Schema.Schema()
			bodyProp := map[string]interface{}{
				"description": "Request body (application/json)",
			}

			if len(bodySchema.Type) > 0 {
				bodyProp["type"] = bodySchema.Type[0]
			}

			if len(bodySchema.Type) > 0 && bodySchema.Type[0] == "object" && bodySchema.Properties != nil {
				bodyProperties := make(map[string]interface{})
				// Iterate properties using First/Next
				for propPair := bodySchema.Properties.First(); propPair != nil; propPair = propPair.Next() {
					key := propPair.Key()
					value := propPair.Value()
					if value != nil && value.Schema() != nil {
						propSchemaVal := value.Schema()
						propSchemaMap := map[string]interface{}{} // Renamed
						if len(propSchemaVal.Type) > 0 {
							propSchemaMap["type"] = propSchemaVal.Type[0]
						}
						if propSchemaVal.Description != "" {
							propSchemaMap["description"] = propSchemaVal.Description
						}
						bodyProperties[key] = propSchemaMap
					}
				}

				if len(bodyProperties) > 0 {
					bodyProp["properties"] = bodyProperties
				}
			}

			properties["body"] = bodyProp
			if operation.RequestBody.Required != nil && *operation.RequestBody.Required {
				required = append(required, "body")
			}
		} else {
			// Handle other content types or no schema? Maybe add a generic 'body' string param?
			// For now, we only support application/json with schema.
		}
	}

	inputSchema["required"] = required

	rawSchema, err := json.Marshal(inputSchema)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal input schema for tool %s: %w", toolName, err)
	}

	// Create the tool
	tool := mcp.NewToolWithRawSchema(toolName, description, rawSchema)

	// No need to set annotations here, they are part of the handler context

	return &tool, nil
}

// HTTP Client to reuse for requests
var httpClient = httpx.Client

// handleToolCall is now a method on operationHandler
func (h *operationHandler) handleToolCall(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	// Context (method, path, baseURL) comes from the handler 'h'

	// Build the full URL using arguments from the request
	fullURL, err := buildURLFromRequest(h.BaseURL, h.Path, req.GetArguments())
	if err != nil {
		return nil, fmt.Errorf("failed to build URL for %s %s: %w", h.Method, h.Path, err)
	}

	// Prepare request body if applicable
	var requestBody io.Reader
	bodyContent, hasBody := req.GetArguments()["body"]
	if hasBody && (h.Method == http.MethodPost || h.Method == http.MethodPut || h.Method == http.MethodPatch) {
		// Assume body is JSON marshalable
		bodyBytes, err := json.Marshal(bodyContent)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body for %s %s: %w", h.Method, h.Path, err)
		}
		requestBody = bytes.NewBuffer(bodyBytes)
	} else {
		requestBody = nil
	}

	// Create the HTTP request
	httpReq, err := http.NewRequestWithContext(ctx, h.Method, fullURL, requestBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request for %s %s: %w", h.Method, h.Path, err)
	}

	// Set headers
	oauth.SetAuthForRequest(httpReq)
	httpReq.Header.Set("Accept", "application/json, */*") // Accept JSON or anything
	httpReq.Header.Set("User-Agent", "wos-cli-mcp/1.0")
	if hasBody && requestBody != nil {
		httpReq.Header.Set("Content-Type", "application/json")
	}

	// Execute the request
	// fmt.Printf("Executing: %s %s\n", h.Method, fullURL) // Removed: Interferes with MCP stdio
	httpResp, err := httpClient().Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed for %s %s: %w", h.Method, h.Path, err)
	}
	defer httpResp.Body.Close()

	// Read response body
	responseBodyBytes, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body for %s %s: %w", h.Method, h.Path, err)
	}

	// Format the result
	resultString := fmt.Sprintf("Status: %s\n", httpResp.Status)

	contentType := httpResp.Header.Get("Content-Type")
	if strings.Contains(contentType, "application/json") && len(responseBodyBytes) > 0 {
		// Try to pretty-print JSON
		var prettyJSON bytes.Buffer
		if err := json.Indent(&prettyJSON, responseBodyBytes, "", "  "); err == nil {
			resultString += fmt.Sprintf("Body (JSON):\n%s", prettyJSON.String())
		} else {
			// Fallback to raw string if JSON indent fails
			resultString += fmt.Sprintf("Body (Unformatted JSON):\n%s", string(responseBodyBytes))
		}
	} else if len(responseBodyBytes) > 0 {
		resultString += fmt.Sprintf("Body (Content-Type: %s):\n%s", contentType, string(responseBodyBytes))
	} else {
		resultString += "Body: (empty)"
	}

	// Return the result
	return mcp.NewToolResultText(resultString), nil
}

// buildURLFromRequest constructs a URL from the base URL, path template, and request arguments.
func buildURLFromRequest(baseURLStr, pathTemplate string, args map[string]interface{}) (string, error) {
	// Base URL should already be absolute and without a trailing slash from runOpenAPIMCPServer
	processedPath := pathTemplate               // Path template from OpenAPI (e.g., /cells/{cellId})
	queryParams := make(map[string]interface{}) // Store query params separately

	// Substitute path parameters and collect query parameters
	for key, value := range args {
		placeholder := fmt.Sprintf("{%s}", key)
		if strings.Contains(processedPath, placeholder) {
			// Consider url.PathEscape if values can contain special chars like '/'
			processedPath = strings.ReplaceAll(processedPath, placeholder, fmt.Sprintf("%v", value))
		} else if key != "body" { // Collect query parameters
			queryParams[key] = value
		}
	}

	// Ensure the path starts with a single slash
	processedPath = "/" + strings.TrimPrefix(processedPath, "/")

	// Combine base URL and the processed path
	// Assuming baseURLStr has no trailing slash and processedPath starts with a slash
	finalURLStr := baseURLStr + processedPath

	// Parse the combined URL to add query parameters safely
	finalURL, err := url.Parse(finalURLStr)
	if err != nil {
		// Provide more context in the error message
		return "", fmt.Errorf("failed to parse combined URL ('%s') from base ('%s') and path ('%s'): %w", finalURLStr, baseURLStr, processedPath, err)
	}

	// Add collected query parameters
	query := finalURL.Query() // Start with any query params already in the path
	for key, value := range queryParams {
		// TODO: Handle array query parameters if needed
		query.Add(key, fmt.Sprintf("%v", value))
	}
	finalURL.RawQuery = query.Encode()

	return finalURL.String(), nil
}
