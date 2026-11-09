// Package maintenance is the opt-in `maintenance` toolset: configuration backups and the
// NOVA update. Backups are saved locally and only their path is handed on (they can
// hold controller credentials and app settings); restore and update need the human's
// approval per call.
package maintenance

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/novaapi"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/tool"
)

// Tools returns the tools of the maintenance toolset.
func Tools(env tool.Env) []tool.Registration {
	m := &maintenance{client: env.Client, downloadDir: env.DownloadDir}
	return []tool.Registration{
		{Toolset: tool.Maintenance, ReadOnly: true, Add: m.addListBackupResources},
		{Toolset: tool.Maintenance, ReadOnly: true, Add: m.addGetBackupStatus},
		{Toolset: tool.Maintenance, ReadOnly: false, Add: m.addCreateBackup},
		{Toolset: tool.Maintenance, ReadOnly: false, Add: m.addRestoreBackup},
		{Toolset: tool.Maintenance, ReadOnly: false, Add: m.addUpdateNovaVersion},
	}
}

type maintenance struct {
	client      *novaapi.Client
	downloadDir string
}

// host names the instance in approval messages.
func (m *maintenance) host() string {
	if u, err := url.Parse(m.client.BaseURL()); err == nil && u.Host != "" {
		return u.Host
	}
	return m.client.BaseURL()
}

// --- list_backup_resources ---

type resource struct {
	ID     string `json:"id" jsonschema:"resource id for create_backup and restore_backup"`
	Name   string `json:"name"`
	Parent string `json:"parent,omitempty" jsonschema:"id of the enclosing resource"`
}

type listResourcesOutput struct {
	Resources []resource `json:"resources"`
	tool.PageInfo
}

type apiResource struct {
	ID       string        `json:"id"`
	Name     string        `json:"name"`
	Children []apiResource `json:"children"`
}

func flatten(nodes []apiResource, parent string, out *[]resource) {
	for _, n := range nodes {
		*out = append(*out, resource{ID: n.ID, Name: n.Name, Parent: parent})
		flatten(n.Children, n.ID, out)
	}
}

func (m *maintenance) addListBackupResources(s *sdk.Server) {
	const name = "list_backup_resources"
	sdk.AddTool(s, &sdk.Tool{
		Name: name,
		Description: "List the configuration resources of this instance a backup can contain (cells, controllers, " +
			"motion groups, apps, stored objects, …), flattened with their parent. Paginated.",
		Annotations: tool.Read("List backup resources"),
	}, func(ctx context.Context, _ *sdk.CallToolRequest, in tool.PageInput) (*sdk.CallToolResult, listResourcesOutput, error) {
		var tree []apiResource
		if err := m.client.Get(ctx, name, "/system/configuration/resources", "", &tree); err != nil {
			return tool.Fail[listResourcesOutput](err)
		}
		all := []resource{}
		flatten(tree, "", &all)
		page, info := tool.Page(all, in)
		return tool.OK("backup resources listed", info.Fields(nil), listResourcesOutput{Resources: page, PageInfo: info})
	})
}

// --- get_backup_status ---

type backupStatusInput struct {
	BackupID string `json:"backup_id" jsonschema:"backup id returned by create_backup"`
}

type backupStatusOutput struct {
	Status   string   `json:"status" jsonschema:"creating, success or error"`
	Progress *float64 `json:"progress,omitempty" jsonschema:"completion ratio 0–1 while creating"`
	Message  string   `json:"message,omitempty" jsonschema:"reason of an error"`
}

func (m *maintenance) addGetBackupStatus(s *sdk.Server) {
	const name = "get_backup_status"
	sdk.AddTool(s, &sdk.Tool{
		Name:        name,
		Description: "Status of a configuration backup by the backup id create_backup returned: creating (with progress), success or error.",
		Annotations: tool.Read("Get backup status"),
	}, func(ctx context.Context, _ *sdk.CallToolRequest, in backupStatusInput) (*sdk.CallToolResult, backupStatusOutput, error) {
		if err := tool.Required(name, "backup_id", in.BackupID); err != nil {
			return tool.Fail[backupStatusOutput](err)
		}
		var out backupStatusOutput
		if _, err := m.client.JSON(ctx, novaapi.Call{Tool: name, Method: http.MethodGet, Path: "/system/configuration/status",
			Query: url.Values{"operation_id": {in.BackupID}}, NotFound: fmt.Sprintf("backup '%s' not found", in.BackupID)}, &out); err != nil {
			return tool.Fail[backupStatusOutput](err)
		}
		fields := map[string]any{"backup_id": in.BackupID, "status": out.Status}
		if out.Progress != nil {
			fields["progress"] = *out.Progress
		}
		return tool.OK("backup status", fields, out)
	})
}

// --- create_backup ---

type createBackupInput struct {
	Resources []string `json:"resources,omitempty" jsonschema:"resource ids from list_backup_resources; each id is included on its own (a cell id doesn't include its controllers); empty means everything"`
}

type createBackupOutput struct {
	Path      string `json:"path" jsonschema:"absolute path of the saved backup archive (tar.gz) on this machine"`
	SizeBytes int    `json:"size_bytes"`
	BackupID  string `json:"backup_id,omitempty"`
}

func (m *maintenance) addCreateBackup(s *sdk.Server) {
	const name = "create_backup"
	sdk.AddTool(s, &sdk.Tool{
		Name: name,
		Description: "Back up the configuration of this instance (everything, or the given resource ids) and save the " +
			"archive on this machine. Returns the file path, never the content: backups can hold controller " +
			"credentials and app settings, don't read or share the file.",
		Annotations: tool.Write("Create backup", false),
	}, func(ctx context.Context, _ *sdk.CallToolRequest, in createBackupInput) (*sdk.CallToolResult, createBackupOutput, error) {
		query := url.Values{}
		if resources := cleanList(in.Resources); len(resources) > 0 {
			query.Set("resources", strings.Join(resources, ","))
		}
		res, apiErr := m.client.Do(ctx, novaapi.Call{Tool: name, Method: http.MethodGet, Path: "/system/configuration", Query: query})
		if apiErr != nil {
			return tool.Fail[createBackupOutput](apiErr)
		}
		if !isGzip(res.Body) {
			return tool.Fail[createBackupOutput](m.client.UnexpectedAnswer(ctx, name, res.Status, res.Body))
		}
		path, err := m.save(res.Body)
		if err != nil {
			m.client.Logger().ErrorContext(ctx, "saving backup failed", "error", err)
			return tool.Fail[createBackupOutput](&novaapi.Error{Code: novaapi.CodeInternal,
				Message: fmt.Sprintf("%s: could not save the backup: %v", name, err)})
		}
		out := createBackupOutput{Path: path, SizeBytes: len(res.Body), BackupID: res.Header.Get("X-Backup-Id")}
		return tool.OK("backup saved", map[string]any{"path": path, "size_bytes": out.SizeBytes, "backup_id": out.BackupID}, out)
	})
}

// save writes a backup with owner-only permissions; it can hold secrets.
func (m *maintenance) save(data []byte) (string, error) {
	dir := m.downloadDir
	if dir == "" {
		dir = filepath.Join(os.TempDir(), "nova-mcp")
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	host := strings.NewReplacer(":", "_", "/", "_").Replace(m.host())
	f, err := os.CreateTemp(dir, fmt.Sprintf("nova-backup-%s-%s-*.tar.gz", host, time.Now().UTC().Format("20060102-150405")))
	if err != nil {
		return "", err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), f.Close()
}

func isGzip(b []byte) bool { return len(b) >= 2 && b[0] == 0x1f && b[1] == 0x8b }

func cleanList(items []string) []string {
	var out []string
	for _, item := range items {
		if item = strings.TrimSpace(item); item != "" && !slices.Contains(out, item) {
			out = append(out, item)
		}
	}
	return out
}

// --- restore_backup ---

type restoreBackupInput struct {
	Path      string   `json:"path" jsonschema:"path of a backup archive on this machine, e.g. from create_backup"`
	Resources []string `json:"resources,omitempty" jsonschema:"resource ids to restore; empty restores everything in the backup"`
}

type restoreBackupOutput struct {
	Path      string   `json:"path"`
	Resources []string `json:"resources" jsonschema:"restored resource ids; empty means everything in the backup"`
}

// maxRestoreBytes bounds what restore_backup reads into memory.
const maxRestoreBytes = 512 << 20

func (m *maintenance) addRestoreBackup(s *sdk.Server) {
	const name = "restore_backup"
	sdk.AddTool(s, &sdk.Tool{
		Name: name,
		Description: "Restore the configuration of this instance from a local backup archive (everything in it, or the " +
			"given resource ids). Overwrites the current configuration. Asks the user for approval through the MCP " +
			"client on every call; clients that can't ask get APPROVAL_REQUIRED.",
		Annotations: tool.Destructive("Restore backup"),
	}, func(ctx context.Context, req *sdk.CallToolRequest, in restoreBackupInput) (*sdk.CallToolResult, restoreBackupOutput, error) {
		if err := tool.Required(name, "path", in.Path); err != nil {
			return tool.Fail[restoreBackupOutput](err)
		}
		path, err := filepath.Abs(in.Path)
		if err != nil {
			return tool.Invalid[restoreBackupOutput](name, "invalid path '%s'", in.Path)
		}
		info, err := os.Stat(path)
		switch {
		case err != nil:
			return tool.Invalid[restoreBackupOutput](name, "cannot read '%s': %v", path, err)
		case !info.Mode().IsRegular():
			return tool.Invalid[restoreBackupOutput](name, "'%s' is not a file", path)
		case info.Size() == 0 || info.Size() > maxRestoreBytes:
			return tool.Invalid[restoreBackupOutput](name, "'%s' has %d bytes, not a backup archive", path, info.Size())
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return tool.Invalid[restoreBackupOutput](name, "cannot read '%s': %v", path, err)
		}
		if !isGzip(data) {
			return tool.Invalid[restoreBackupOutput](name, "'%s' is not a backup archive (tar.gz)", path)
		}
		resources := cleanList(in.Resources)
		scope := "everything in the backup"
		if len(resources) > 0 {
			scope = strings.Join(resources, ", ")
		}
		message := fmt.Sprintf("Restore the configuration of NOVA instance %s from %s (%d bytes)? Restores: %s. "+
			"This overwrites the current configuration of the instance.", m.host(), path, len(data), scope)
		if res := tool.Approval(req, name, message); res != nil {
			return res, restoreBackupOutput{}, nil
		}

		query := url.Values{}
		if len(resources) > 0 {
			query.Set("resources", strings.Join(resources, ","))
		}
		if apiErr := m.send(ctx, name, http.MethodPost, "/system/configuration", query, "application/gzip", data, nil); apiErr != nil {
			return tool.Fail[restoreBackupOutput](apiErr)
		}
		if resources == nil {
			resources = []string{}
		}
		return tool.OK("configuration restored", map[string]any{"path": path, "resources": len(resources)},
			restoreBackupOutput{Path: path, Resources: resources})
	})
}

// --- update_nova_version ---

type updateInput struct {
	Channel     string `json:"channel" jsonschema:"stable: newest patch of the current version; next: the latest version"`
	UpdateCells *bool  `json:"update_cells,omitempty" jsonschema:"also update cells without a pinned version (default true)"`
}

type updateOutput struct {
	CurrentVersion string `json:"current_version"`
	TargetVersion  string `json:"target_version,omitempty"`
	Status         string `json:"status" jsonschema:"'pending' while the update runs (verify with get_context), 'up_to_date' if there is nothing to update"`
}

func (m *maintenance) addUpdateNovaVersion(s *sdk.Server) {
	const name = "update_nova_version"
	sdk.AddTool(s, &sdk.Tool{
		Name: name,
		Description: "Update NOVA on this instance to the newest version of a release channel. Asks the user for " +
			"approval through the MCP client, naming current and target version; clients that can't ask get " +
			"APPROVAL_REQUIRED. Services restart during the update; check the version afterwards with get_context.",
		Annotations: tool.Annotations("Update NOVA version", false, false, true, false),
	}, func(ctx context.Context, req *sdk.CallToolRequest, in updateInput) (*sdk.CallToolResult, updateOutput, error) {
		channel := strings.ToLower(strings.TrimSpace(in.Channel))
		if channel != "stable" && channel != "next" {
			return tool.Invalid[updateOutput](name, "channel must be 'stable' or 'next', got '%s'", in.Channel)
		}
		current, apiErr := m.text(ctx, name, "/system/version", nil)
		if apiErr != nil {
			return tool.Fail[updateOutput](apiErr)
		}
		target, apiErr := m.text(ctx, name, "/system/update", url.Values{"channel": {channel}})
		if apiErr != nil {
			return tool.Fail[updateOutput](apiErr)
		}
		if target == "" {
			return tool.OK("no update available", map[string]any{"channel": channel, "current_version": current},
				updateOutput{CurrentVersion: current, Status: "up_to_date"})
		}
		updateCells := in.UpdateCells == nil || *in.UpdateCells
		cells := "cells without a pinned version are updated too"
		if !updateCells {
			cells = "cells keep their version"
		}
		message := fmt.Sprintf("Update NOVA on instance %s from %s to %s (channel %s, %s)? Services and cells restart during the update.",
			m.host(), current, target, channel, cells)
		if res := tool.Approval(req, name, message); res != nil {
			return res, updateOutput{}, nil
		}

		body, _ := json.Marshal(map[string]any{"channel": channel, "update_cells": updateCells})
		var status int
		if apiErr := m.send(ctx, name, http.MethodPut, "/system/update", nil, "application/json", body, &status); apiErr != nil {
			return tool.Fail[updateOutput](apiErr)
		}
		out := updateOutput{CurrentVersion: current, TargetVersion: target, Status: novaapi.StatusPending}
		if status == http.StatusNoContent {
			out = updateOutput{CurrentVersion: current, Status: "up_to_date"}
			return tool.OK("no update available", map[string]any{"channel": channel, "current_version": current}, out)
		}
		return tool.OK("update started", map[string]any{"channel": channel, "current_version": current,
			"target_version": target, "status": out.Status}, out)
	})
}

// text GETs a text/plain answer (a version); 204 is "".
func (m *maintenance) text(ctx context.Context, toolName, path string, query url.Values) (string, *novaapi.Error) {
	res, apiErr := m.client.Do(ctx, novaapi.Call{Tool: toolName, Method: http.MethodGet, Path: path, Query: query})
	if apiErr != nil {
		return "", apiErr
	}
	if res.Status == http.StatusNoContent {
		return "", nil
	}
	var s string
	if json.Unmarshal(res.Body, &s) == nil {
		return strings.TrimSpace(s), nil
	}
	return strings.TrimSpace(string(res.Body)), nil
}

// send makes a request with a non-JSON or pre-encoded body, which novaapi.Call can't
// carry, and maps failures like novaapi.Client.Do.
func (m *maintenance) send(ctx context.Context, toolName, method, path string, query url.Values, contentType string, body []byte, status *int) *novaapi.Error {
	u := m.client.BaseURL() + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, bytes.NewReader(body))
	if err != nil {
		return &novaapi.Error{Code: novaapi.CodeInternal, Message: toolName + ": " + err.Error()}
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Accept", "application/json, */*")
	if apiErr := m.client.Authorize(ctx, toolName, req); apiErr != nil {
		return apiErr
	}
	resp, err := m.client.HTTPClient().Do(req)
	if err != nil {
		m.client.Logger().ErrorContext(ctx, "NOVA API call failed", "tool", toolName, "error", err)
		return &novaapi.Error{Code: novaapi.CodeUnavailable, Message: fmt.Sprintf("%s: cannot reach the NOVA instance: %v", toolName, err)}
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if status != nil {
		*status = resp.StatusCode
	}
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode <= 299:
		return nil
	case resp.StatusCode == http.StatusBadRequest:
		return &novaapi.Error{Code: novaapi.CodeInvalidArgument, Message: toolName + ": " + apiMessage(raw, "the NOVA API rejected the request")}
	case resp.StatusCode == http.StatusUnauthorized:
		return &novaapi.Error{Code: novaapi.CodeUnauthenticated, Message: toolName + ": the instance rejected the login, run 'nova login' (cloud) or check the basic auth of the instance"}
	case resp.StatusCode == http.StatusForbidden:
		return &novaapi.Error{Code: novaapi.CodeForbidden, Message: toolName + ": " + apiMessage(raw, "this operation is not allowed")}
	case resp.StatusCode == http.StatusNotImplemented:
		return &novaapi.Error{Code: novaapi.CodeForbidden, Message: toolName + ": this NOVA instance can't do this through the API (HTTP 501)"}
	}
	return m.client.UnexpectedAnswer(ctx, toolName, resp.StatusCode, raw)
}

func apiMessage(body []byte, fallback string) string {
	var e struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &e) != nil || e.Message == "" {
		return fallback
	}
	return e.Message
}
