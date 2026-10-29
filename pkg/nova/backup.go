package nova

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"

	openapi_types "github.com/oapi-codegen/runtime/types"

	apiv2 "github.com/wandelbotsgmbh/nova-api-client-go/v25/pkg/nova/v2"
	"github.com/wandelbotsgmbh/nova-cli/pkg/utils"
)

// the version of Nova API where backup functionality was introduced
const backupSinceVersion = "25.6.0"

var errBackupFeatureVersion = errors.New("backup feature is only available with nova version starting from " + backupSinceVersion)

// BackupManifest represents the structure of the "manifest.json" file in a backup.
// There are additional meta fields we currently do not care about.
type BackupManifest struct {
	Resources   []string `json:"Resources"`
	NovaVersion string   `json:"NovaVersion"`
}

// DownloadStatus represents the current status of a backup download
type DownloadStatus struct {
	BytesDownloaded int
	TotalBytes      int
	Message         string
	Error           error
}

// ProgressWriter wraps an io.Writer to track download progress
type ProgressWriter struct {
	total    int
	written  int
	progress chan<- DownloadStatus
	writer   io.Writer
}

func (pw *ProgressWriter) Write(p []byte) (int, error) {
	n, err := pw.writer.Write(p)
	if err != nil {
		return n, err
	}

	pw.written += n

	pw.progress <- DownloadStatus{
		BytesDownloaded: pw.written,
		TotalBytes:      pw.total,
		Message:         "downloading",
	}

	return n, nil
}

// DownloadBackupWithProgress downloads a backup with progress reporting via channel
func DownloadBackupWithProgress(host, filename string, progress chan<- DownloadStatus) {
	defer close(progress)

	if isLess, err := IsCurrentVersionLessThan(host, backupSinceVersion); err != nil {
		progress <- DownloadStatus{Error: err}
		return
	} else if isLess {
		progress <- DownloadStatus{Error: errBackupFeatureVersion}
		return
	}

	progress <- DownloadStatus{Message: "starting backup download"}

	client, err := NewV2ClientWithToken(host)
	if err != nil {
		progress <- DownloadStatus{Error: err}
		return
	}

	progress <- DownloadStatus{Message: "listing configuration resources"}

	resourceResp, err := client.ListConfigurationResourcesWithResponse(context.Background())
	if err != nil {
		progress <- DownloadStatus{Error: fmt.Errorf("failed to list configuration resources: %w", err)}
		return
	}

	if resourceResp.JSON200 == nil {
		progress <- DownloadStatus{Error: fmt.Errorf("no configuration resources found: %s %s", resourceResp.Status(), string(resourceResp.Body))}
		return
	}

	progress <- DownloadStatus{Message: "creating backup"}

	// Resource IDs are provided via tree structure, but later provided as flat list, thus we need to flatten them.
	params := apiv2.BackupConfigurationParams{}
	resources := flattenResources(resourceResp.JSON200)
	params.Resources = &resources

	resp, err := client.BackupConfiguration(context.Background(), &params)
	if err != nil {
		progress <- DownloadStatus{Error: err}
		return
	}

	backupId := resp.Header.Get("X-Backup-Id")
	// Casting should be fine as we are not running/compiling for 32-bit systems
	// Backup is also generated on the fly, thus the final size is probably not known at this point.
	// Thus it is expected this is just -1 in most (all) cases
	contentLength := int(resp.ContentLength)

	progress <- DownloadStatus{Message: fmt.Sprintf("Backup created with ID: %s", backupId), TotalBytes: contentLength}

	var backupUuid openapi_types.UUID
	err = backupUuid.UnmarshalText([]byte(backupId))
	if err != nil {
		progress <- DownloadStatus{Error: fmt.Errorf("failed to parse backup ID: %w", err)}
		return
	}

	// Create file and write with progress
	file, err := os.Create(filename)
	if err != nil {
		progress <- DownloadStatus{Error: err}
		return
	}
	defer file.Close()

	progressWriter := &ProgressWriter{
		total:    contentLength,
		writer:   file,
		progress: progress,
	}

	_, err = io.Copy(progressWriter, resp.Body)
	if err != nil {
		progress <- DownloadStatus{Error: err}
		return
	}

	progress <- DownloadStatus{
		Message:         "backup written to " + filename,
		BytesDownloaded: progressWriter.written,
	}
}

func RestoreBackup(host, filename string) error {
	client, err := NewV2ClientWithToken(host)
	if err != nil {
		return err
	}

	if isLess, err := IsCurrentVersionLessThan(host, backupSinceVersion); err != nil {
		return err
	} else if isLess {
		return errBackupFeatureVersion
	}

	data, err := os.ReadFile(filename)
	if err != nil {
		return fmt.Errorf("failed to read backup file: %w", err)
	}

	if len(data) == 0 {
		return fmt.Errorf("backup file is empty")
	}

	manifest, err := ReadBackupManifest(filename)
	if err != nil {
		return err
	}

	if isEqual, _ := IsCurrentVersionEqual(host, manifest.NovaVersion); !isEqual {
		fmt.Println("Warning: You are restoring a backup to a different NOVA version than it was created with.")
	}

	reader := bytes.NewReader(data)
	params := apiv2.RestoreConfigurationParams{}

	// restore everything from the backup
	params.Resources = append(params.Resources, manifest.Resources...)

	resp, err := client.RestoreConfigurationWithBodyWithResponse(context.Background(), &params, "application/gzip", reader)
	if err != nil {
		return err
	}

	if resp.HTTPResponse.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to restore backup: %s %s", resp.Status(), string(resp.Body))
	}

	return nil
}

func ReadBackupManifest(filename string) (*BackupManifest, error) {
	manifestRaw, err := utils.ReadFileFromTarGz(filename, "manifest.json")
	if err != nil {
		return nil, fmt.Errorf("failed to read manifest from backup file: %w", err)
	}

	var manifest BackupManifest
	if err := json.Unmarshal(manifestRaw, &manifest); err != nil {
		return nil, fmt.Errorf("failed to unmarshal manifest: %w", err)
	}

	return &manifest, nil
}

func flattenResources(resources *apiv2.ConfigurationResourceArray) []string {
	var result []string
	if resources == nil {
		return result
	}

	for _, r := range *resources {
		result = append(result, r.Id)
		result = append(result, flattenResources(r.Children)...)
	}
	return result
}
