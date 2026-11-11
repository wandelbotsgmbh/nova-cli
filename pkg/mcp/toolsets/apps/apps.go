// Package apps serves the `apps` toolset: the containerized web apps of a cell.
//
// Hardening: environment values and registry credentials never reach the model.
// describe_app returns environment variable names only; add_app and update_app take no
// registry credentials (pull secrets that exist on the instance are referenced by
// name), and update_app changes only the fields it is given, sending the rest of the
// configuration back as read without ever handing it on.
package apps

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/novaapi"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/tool"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/toolsets/system"
)

// Tools returns the tools of this package.
func Tools(env tool.Env) []tool.Registration {
	c := env.Client
	return []tool.Registration{
		listApps(c),
		describeApp(c),
		addApp(c),
		updateApp(c),
		deleteApp(c),
	}
}

// appNamePattern is the app name pattern of the API.
var appNamePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,61}[a-z0-9]$`)

func appsPath(cell string) string { return "/cells/" + url.PathEscape(cell) + "/apps" }

func appPath(cell, app string) string { return appsPath(cell) + "/" + url.PathEscape(app) }

func appNotFound(cell, app string) string {
	return fmt.Sprintf("app '%s' not found in cell '%s'", app, cell)
}

// EnvVar is an environment variable to set (write only, values are never returned).
type EnvVar struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type deployOutput struct {
	Cell   string `json:"cell"`
	App    string `json:"app"`
	Status string `json:"status" jsonschema:"'completed' or 'pending': verify with describe_app when pending"`
}

// --- list_apps ---

type listAppsInput struct {
	Cell string `json:"cell" jsonschema:"cell name (see list_cells)"`
	tool.PageInput
}

type listAppsOutput struct {
	Apps []string `json:"apps"`
	tool.PageInfo
}

func listApps(c *novaapi.Client) tool.Registration {
	const name = "list_apps"
	add := func(s *sdk.Server) {
		sdk.AddTool(s, &sdk.Tool{
			Name:        name,
			Description: "List the apps installed in a cell.",
			Annotations: tool.Read("List apps"),
		}, func(ctx context.Context, _ *sdk.CallToolRequest, in listAppsInput) (*sdk.CallToolResult, listAppsOutput, error) {
			if err := tool.Required(name, "cell", in.Cell); err != nil {
				return tool.Fail[listAppsOutput](err)
			}
			var apps []string
			if err := c.Get(ctx, name, appsPath(in.Cell), fmt.Sprintf("cell '%s' not found", in.Cell), &apps); err != nil {
				return tool.Fail[listAppsOutput](err)
			}
			sort.Strings(apps)
			window, page := tool.Page(apps, in.PageInput)
			return tool.OK("apps listed", page.Fields(map[string]any{"cell": in.Cell}), listAppsOutput{Apps: window, PageInfo: page})
		})
	}
	return tool.Registration{Toolset: tool.Apps, ReadOnly: true, Add: add}
}

// --- describe_app ---

type appInput struct {
	Cell string `json:"cell" jsonschema:"cell name (see list_cells)"`
	App  string `json:"app" jsonschema:"app name (see list_apps)"`
}

// rawApp declares only what describe_app hands on: no environment values, no
// credentials.
type rawApp struct {
	Name           string `json:"name"`
	AppIcon        string `json:"app_icon"`
	ContainerImage struct {
		Image   string `json:"image"`
		Secrets []struct {
			Name string `json:"name"`
		} `json:"secrets"`
	} `json:"container_image"`
	Port        int `json:"port"`
	Environment []struct {
		Name string `json:"name"`
	} `json:"environment"`
	Storage *struct {
		MountPath string `json:"mount_path"`
		Capacity  string `json:"capacity"`
	} `json:"storage"`
	Resources *struct {
		IntelGPU    int    `json:"intel_gpu"`
		MemoryLimit string `json:"memory_limit"`
	} `json:"resources"`
	HealthPath    string `json:"health_path"`
	DiagnosisPath string `json:"diagnosis_path"`
}

type describeAppOutput struct {
	Name             string   `json:"name"`
	Image            string   `json:"image"`
	Port             int      `json:"port"`
	Path             string   `json:"path" jsonschema:"URL path of the app on the instance"`
	AppIcon          string   `json:"app_icon,omitempty"`
	HealthPath       string   `json:"health_path,omitempty"`
	DiagnosisPath    string   `json:"diagnosis_path,omitempty"`
	EnvironmentNames []string `json:"environment_names" jsonschema:"names of the environment variables; values are never returned"`
	PullSecrets      []string `json:"pull_secrets,omitempty" jsonschema:"names of the registry pull secrets used"`
	StorageMountPath string   `json:"storage_mount_path,omitempty"`
	StorageCapacity  string   `json:"storage_capacity,omitempty"`
	MemoryLimit      string   `json:"memory_limit,omitempty"`
	IntelGPU         int      `json:"intel_gpu,omitempty"`
	State            string   `json:"state,omitempty" jsonschema:"phase from the cell status, e.g. Running or ImagePullBackOff"`
	Severity         string   `json:"severity,omitempty"`
}

func projectApp(cell string, raw rawApp) describeAppOutput {
	out := describeAppOutput{
		Name: raw.Name, Image: raw.ContainerImage.Image, Port: raw.Port, Path: "/" + cell + "/" + raw.Name,
		AppIcon: raw.AppIcon, HealthPath: raw.HealthPath, DiagnosisPath: raw.DiagnosisPath, EnvironmentNames: []string{},
	}
	for _, e := range raw.Environment {
		out.EnvironmentNames = append(out.EnvironmentNames, e.Name)
	}
	sort.Strings(out.EnvironmentNames)
	for _, s := range raw.ContainerImage.Secrets {
		out.PullSecrets = append(out.PullSecrets, s.Name)
	}
	if raw.Storage != nil {
		out.StorageMountPath, out.StorageCapacity = raw.Storage.MountPath, raw.Storage.Capacity
	}
	if raw.Resources != nil {
		out.MemoryLimit, out.IntelGPU = raw.Resources.MemoryLimit, raw.Resources.IntelGPU
	}
	return out
}

func describeApp(c *novaapi.Client) tool.Registration {
	const name = "describe_app"
	add := func(s *sdk.Server) {
		sdk.AddTool(s, &sdk.Tool{
			Name: name,
			Description: "An app's configuration (image, port, paths, storage, resources, environment variable names) and its state. " +
				"Environment values and registry credentials are never returned.",
			Annotations: tool.Read("Describe app"),
		}, func(ctx context.Context, _ *sdk.CallToolRequest, in appInput) (*sdk.CallToolResult, describeAppOutput, error) {
			if err := tool.Required(name, "cell", in.Cell, "app", in.App); err != nil {
				return tool.Fail[describeAppOutput](err)
			}
			var raw rawApp
			if err := c.Get(ctx, name, appPath(in.Cell, in.App), appNotFound(in.Cell, in.App), &raw); err != nil {
				return tool.Fail[describeAppOutput](err)
			}
			out := projectApp(in.Cell, raw)
			// the state is a bonus: a failing status call doesn't fail the tool
			if status, err := system.FetchCellStatus(ctx, c, name, in.Cell); err == nil {
				for _, svc := range status.Services {
					if svc.Group == "App" && svc.Service == raw.Name {
						out.State, out.Severity = svc.Code, svc.Severity
					}
				}
			}
			return tool.OK("app described", map[string]any{"cell": in.Cell, "app": out.Name, "image": out.Image, "state": out.State}, out)
		})
	}
	return tool.Registration{Toolset: tool.Apps, ReadOnly: true, Add: add}
}

// --- add_app ---

type addAppInput struct {
	Cell          string   `json:"cell" jsonschema:"cell name (see list_cells)"`
	Name          string   `json:"name" jsonschema:"app name, unique in the cell: lowercase letters, digits and '-', starting with a letter"`
	Image         string   `json:"image" jsonschema:"container image <registry>/<image>:<tag>; the instance must be able to pull it"`
	Port          int      `json:"port,omitempty" jsonschema:"port the app's webserver listens on (default 8080)"`
	AppIcon       string   `json:"app_icon,omitempty" jsonschema:"path of the app icon below the app (default app_icon.png)"`
	Environment   []EnvVar `json:"environment,omitempty" jsonschema:"environment variables; NOVA_API, NATS_BROKER, BASE_PATH and CELL_NAME are set by NOVA. Never put secrets here."`
	PullSecrets   []string `json:"pull_secrets,omitempty" jsonschema:"names of existing registry pull secrets on the instance (see describe_app of another app)"`
	HealthPath    string   `json:"health_path,omitempty"`
	DiagnosisPath string   `json:"diagnosis_path,omitempty"`
	MemoryLimit   string   `json:"memory_limit,omitempty" jsonschema:"e.g. 1000Mi"`
	StorageMount  string   `json:"storage_mount_path,omitempty" jsonschema:"path of a persistent volume in the container"`
	StorageSize   string   `json:"storage_capacity,omitempty" jsonschema:"size of the persistent volume, at most 300Mi; can never be reduced"`
}

func addApp(c *novaapi.Client) tool.Registration {
	const name = "add_app"
	add := func(s *sdk.Server) {
		sdk.AddTool(s, &sdk.Tool{
			Name: name,
			Description: "Install a containerized web app into a cell. Registry credentials are not accepted: the image must be " +
				"pullable with the instance's existing pull secrets. Returns 'pending' while it rolls out: verify with describe_app.",
			Annotations: tool.Write("Add app", false),
		}, func(ctx context.Context, _ *sdk.CallToolRequest, in addAppInput) (*sdk.CallToolResult, deployOutput, error) {
			if err := tool.Required(name, "cell", in.Cell, "name", in.Name, "image", in.Image); err != nil {
				return tool.Fail[deployOutput](err)
			}
			if !appNamePattern.MatchString(in.Name) {
				return tool.Invalid[deployOutput](name, "app name '%s' is invalid: lowercase letters, digits and '-', starting with a letter", in.Name)
			}
			if (in.StorageMount == "") != (in.StorageSize == "") {
				return tool.Invalid[deployOutput](name, "storage_mount_path and storage_capacity go together")
			}
			if err := validEnv(name, in.Environment); err != nil {
				return tool.Fail[deployOutput](err)
			}
			body := map[string]any{"name": in.Name, "app_icon": "app_icon.png", "port": 8080}
			if in.AppIcon != "" {
				body["app_icon"] = in.AppIcon
			}
			if in.Port != 0 {
				body["port"] = in.Port
			}
			image := map[string]any{"image": in.Image}
			if len(in.PullSecrets) > 0 {
				image["secrets"] = secretRefs(in.PullSecrets)
			}
			body["container_image"] = image
			if len(in.Environment) > 0 {
				body["environment"] = in.Environment
			}
			if in.HealthPath != "" {
				body["health_path"] = in.HealthPath
			}
			if in.DiagnosisPath != "" {
				body["diagnosis_path"] = in.DiagnosisPath
			}
			if in.MemoryLimit != "" {
				body["resources"] = map[string]any{"memory_limit": in.MemoryLimit}
			}
			if in.StorageMount != "" {
				body["storage"] = map[string]any{"mount_path": in.StorageMount, "capacity": in.StorageSize}
			}
			status, err := c.JSON(ctx, novaapi.Call{Tool: name, Method: http.MethodPost, Path: appsPath(in.Cell), Body: body,
				NotFound: fmt.Sprintf("cell '%s' not found", in.Cell)}, nil)
			if err != nil {
				if err.Code == novaapi.CodeAlreadyExists {
					err.Message = fmt.Sprintf("%s: an app named '%s' already exists in cell '%s'; use update_app", name, in.Name, in.Cell)
				}
				return tool.Fail[deployOutput](err)
			}
			out := deployOutput{Cell: in.Cell, App: in.Name, Status: novaapi.DeployStatus(status)}
			return tool.OK("app installation "+out.Status, map[string]any{"cell": in.Cell, "app": in.Name, "status": out.Status}, out)
		})
	}
	return tool.Registration{Toolset: tool.Apps, ReadOnly: false, Add: add}
}

func secretRefs(names []string) []map[string]string {
	refs := make([]map[string]string, 0, len(names))
	for _, n := range names {
		refs = append(refs, map[string]string{"name": n})
	}
	return refs
}

func validEnv(toolName string, env []EnvVar) *novaapi.Error {
	for _, e := range env {
		if e.Name == "" {
			return &novaapi.Error{Code: novaapi.CodeInvalidArgument, Message: toolName + ": every environment variable needs a name"}
		}
	}
	return nil
}

// --- update_app ---

type updateAppInput struct {
	Cell              string   `json:"cell" jsonschema:"cell name (see list_cells)"`
	App               string   `json:"app" jsonschema:"app name (see list_apps)"`
	Image             string   `json:"image,omitempty" jsonschema:"new container image"`
	Port              int      `json:"port,omitempty"`
	SetEnvironment    []EnvVar `json:"set_environment,omitempty" jsonschema:"environment variables to add or overwrite; others are kept. Never put secrets here."`
	RemoveEnvironment []string `json:"remove_environment,omitempty" jsonschema:"names of environment variables to remove"`
	HealthPath        string   `json:"health_path,omitempty"`
	DiagnosisPath     string   `json:"diagnosis_path,omitempty"`
	MemoryLimit       string   `json:"memory_limit,omitempty"`
}

func updateApp(c *novaapi.Client) tool.Registration {
	const name = "update_app"
	add := func(s *sdk.Server) {
		sdk.AddTool(s, &sdk.Tool{
			Name: name,
			Description: "Change an app's image, port, environment, paths or memory limit. Only the given fields change, " +
				"everything else is kept. The app restarts. Returns 'pending' while it rolls out: verify with describe_app.",
			Annotations: tool.Write("Update app", true),
		}, func(ctx context.Context, _ *sdk.CallToolRequest, in updateAppInput) (*sdk.CallToolResult, deployOutput, error) {
			if err := tool.Required(name, "cell", in.Cell, "app", in.App); err != nil {
				return tool.Fail[deployOutput](err)
			}
			if in.Image == "" && in.Port == 0 && len(in.SetEnvironment) == 0 && len(in.RemoveEnvironment) == 0 &&
				in.HealthPath == "" && in.DiagnosisPath == "" && in.MemoryLimit == "" {
				return tool.Invalid[deployOutput](name, "nothing to change: pass at least one field to update")
			}
			if err := validEnv(name, in.SetEnvironment); err != nil {
				return tool.Fail[deployOutput](err)
			}
			res, err := c.Do(ctx, novaapi.Call{Tool: name, Method: http.MethodGet, Path: appPath(in.Cell, in.App), NotFound: appNotFound(in.Cell, in.App)})
			if err != nil {
				return tool.Fail[deployOutput](err)
			}
			// the full configuration stays in this process: it may carry credentials
			var config map[string]any
			if json.Unmarshal(res.Body, &config) != nil || config == nil {
				return tool.Fail[deployOutput](c.UnexpectedAnswer(ctx, name, res.Status, res.Body))
			}
			applyUpdate(config, in)
			status, err := c.JSON(ctx, novaapi.Call{Tool: name, Method: http.MethodPut, Path: appPath(in.Cell, in.App), Body: config,
				NotFound: appNotFound(in.Cell, in.App)}, nil)
			if err != nil {
				return tool.Fail[deployOutput](err)
			}
			out := deployOutput{Cell: in.Cell, App: in.App, Status: novaapi.DeployStatus(status)}
			return tool.OK("app update "+out.Status, map[string]any{"cell": in.Cell, "app": in.App, "status": out.Status}, out)
		})
	}
	return tool.Registration{Toolset: tool.Apps, ReadOnly: false, Add: add}
}

// applyUpdate changes config (an app as read from the API) in place.
func applyUpdate(config map[string]any, in updateAppInput) {
	image, _ := config["container_image"].(map[string]any)
	if image == nil {
		image = map[string]any{}
		config["container_image"] = image
	}
	// the API reads credentials back with empty values; sending those back could
	// replace working ones, so empty credentials are dropped
	if creds, ok := image["credentials"].(map[string]any); ok {
		empty := true
		for _, v := range creds {
			if s, _ := v.(string); s != "" {
				empty = false
			}
		}
		if empty {
			delete(image, "credentials")
		}
	}
	if in.Image != "" {
		image["image"] = in.Image
	}
	if in.Port != 0 {
		config["port"] = in.Port
	}
	if in.HealthPath != "" {
		config["health_path"] = in.HealthPath
	}
	if in.DiagnosisPath != "" {
		config["diagnosis_path"] = in.DiagnosisPath
	}
	if in.MemoryLimit != "" {
		resources, _ := config["resources"].(map[string]any)
		if resources == nil {
			resources = map[string]any{}
		}
		resources["memory_limit"] = in.MemoryLimit
		config["resources"] = resources
	}
	if len(in.SetEnvironment) > 0 || len(in.RemoveEnvironment) > 0 {
		remove := map[string]bool{}
		for _, n := range in.RemoveEnvironment {
			remove[n] = true
		}
		set := map[string]string{}
		for _, e := range in.SetEnvironment {
			set[e.Name] = e.Value
		}
		var env []any
		existing, _ := config["environment"].([]any)
		for _, item := range existing {
			entry, _ := item.(map[string]any)
			n, _ := entry["name"].(string)
			if remove[n] {
				continue
			}
			if v, ok := set[n]; ok {
				entry["value"] = v
				delete(set, n)
			}
			env = append(env, entry)
		}
		for _, e := range in.SetEnvironment {
			if _, ok := set[e.Name]; ok {
				env = append(env, map[string]any{"name": e.Name, "value": e.Value})
				delete(set, e.Name)
			}
		}
		if env == nil {
			env = []any{}
		}
		config["environment"] = env
	}
}

// --- delete_app ---

func deleteApp(c *novaapi.Client) tool.Registration {
	const name = "delete_app"
	add := func(s *sdk.Server) {
		sdk.AddTool(s, &sdk.Tool{
			Name: name,
			Description: "Remove an app from a cell, including its persistent storage. DESTRUCTIVE and not undoable: " +
				"confirm with the user first, naming the exact cell and app.",
			Annotations: tool.Destructive("Delete app"),
		}, func(ctx context.Context, _ *sdk.CallToolRequest, in appInput) (*sdk.CallToolResult, deployOutput, error) {
			if err := tool.Required(name, "cell", in.Cell, "app", in.App); err != nil {
				return tool.Fail[deployOutput](err)
			}
			status, err := c.JSON(ctx, novaapi.Call{Tool: name, Method: http.MethodDelete, Path: appPath(in.Cell, in.App),
				NotFound: appNotFound(in.Cell, in.App)}, nil)
			if err != nil {
				return tool.Fail[deployOutput](err)
			}
			out := deployOutput{Cell: in.Cell, App: in.App, Status: novaapi.DeployStatus(status)}
			return tool.OK("app deletion "+out.Status, map[string]any{"cell": in.Cell, "app": in.App, "status": out.Status}, out)
		})
	}
	return tool.Registration{Toolset: tool.Apps, ReadOnly: false, Add: add}
}
