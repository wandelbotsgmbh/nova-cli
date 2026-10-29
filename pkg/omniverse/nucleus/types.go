// Package nucleus provides a pure-Go client for NVIDIA Omniverse Nucleus servers.
//
// It implements the two wire protocols Nucleus uses (SOWS for auth/discovery,
// connlib/JSON-over-WebSocket for the content API) so it works on any platform
// including macOS — no C++ library required.
package nucleus

import "time"

// Entry represents a file or directory in Nucleus.
type Entry struct {
	Path              string   `json:"path"`
	Branch            string   `json:"branch,omitempty"`
	ACL               []string `json:"acl,omitempty"`
	CreatedTimestamp  int64    `json:"created_timestamp,omitempty"`
	ModifiedTimestamp int64    `json:"modified_timestamp,omitempty"`
	PathType          string   `json:"path_type"` // "file", "folder", "mount"
	Size              int64    `json:"size,omitempty"`
	Hash              string   `json:"hash,omitempty"`
	Empty             bool     `json:"empty,omitempty"`
	Mounted           bool     `json:"mounted,omitempty"`
	CreatedBy         string   `json:"created_by,omitempty"`
	ModifiedBy        string   `json:"modified_by,omitempty"`
	Etag              string   `json:"etag,omitempty"`
	Comment           string   `json:"comment,omitempty"`
	Version           int      `json:"version,omitempty"`
	DeletedBy         string   `json:"deleted_by,omitempty"`
	ChangeID          int64    `json:"change_id,omitempty"`
}

// CreatedTime returns the creation time as time.Time.
func (e Entry) CreatedTime() time.Time {
	return time.Unix(e.CreatedTimestamp, 0)
}

// ModifiedTime returns the modification time as time.Time.
func (e Entry) ModifiedTime() time.Time {
	return time.Unix(e.ModifiedTimestamp, 0)
}

// IsDir returns true if this entry is a directory or mount point.
func (e Entry) IsDir() bool {
	return e.PathType == "folder" || e.PathType == "mount"
}

// ConnectionInfo is returned after successful authentication with the API.
type ConnectionInfo struct {
	Status             string         `json:"status"`
	Username           string         `json:"username"`
	Token              string         `json:"token"`
	ConnectionID       string         `json:"connection_id"`
	MaxChunkSize       int64          `json:"max_chunk_size"`
	LFTAddress         string         `json:"lft_address,omitempty"`
	LFTThreshold       int64          `json:"lft_threshold,omitempty"`
	ServerCapabilities map[string]int `json:"server_capabilities,omitempty"`
	Version            string         `json:"version,omitempty"`
	SuperUser          bool           `json:"super_user,omitempty"`
}

// ClientConfig holds configuration for connecting to a Nucleus server.
type ClientConfig struct {
	// Host is the Nucleus server hostname (e.g. "nucleus.dev.wandelbots.io").
	Host string

	// APIToken is the long-lived API token for authentication.
	// Obtain via Navigator UI → Account → API Tokens.
	APIToken string

	// TLS controls whether to use wss:// (default true).
	TLS bool

	// UserAgent identifies this client to the server.
	UserAgent string
}
