package nucleus

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/wandelbotsgmbh/nova-cli/pkg/omniverse/nucleus/connlib"
	"github.com/wandelbotsgmbh/nova-cli/pkg/omniverse/nucleus/sows"
)

// Client is a Nucleus API client. Use [Dial] to create one.
//
// The API WebSocket can die mid-session (laptop sleep, network change,
// proxy idle timeout, server restart). Read-only operations (List, Stat,
// ReadFile) transparently re-dial and re-authenticate when that happens;
// mutating operations return an error wrapping [connlib.ErrClosed] instead,
// so the caller decides whether to repeat them.
type Client struct {
	cfg ClientConfig

	reconnectMu sync.Mutex // serializes reconnect attempts
	cmdMu       sync.Mutex // serializes connlib commands, see [Client.withCmd]
	mu          sync.Mutex // guards the fields below across reconnects
	conn        *connlib.Conn
	cancel      context.CancelFunc // cancels the read loop
	accessToken string             // JWT from auth service, needed for LFT
	connInfo    ConnectionInfo
	reconnects  int
}

// Dial connects to a Nucleus server, authenticates using the API token,
// and returns a ready-to-use Client.
func Dial(ctx context.Context, cfg ClientConfig) (*Client, error) {
	if cfg.UserAgent == "" {
		cfg.UserAgent = "nucleus/1.0"
	}
	if cfg.Host == "" {
		return nil, fmt.Errorf("nucleus: Host is required")
	}
	if cfg.APIToken == "" {
		return nil, fmt.Errorf("nucleus: APIToken is required")
	}

	c := &Client{cfg: cfg}
	if err := c.connect(ctx); err != nil {
		return nil, err
	}
	return c, nil
}

// connect performs the full handshake (SOWS auth, API WebSocket,
// authorize_token) and installs the resulting connection on c, closing any
// previous one.
func (c *Client) connect(ctx context.Context) error {
	cfg := c.cfg
	scheme := "wss"
	if !cfg.TLS {
		scheme = "ws"
	}

	// Step 1: Authenticate via the Auth service (SOWS protocol) to get an access token.
	accessToken, err := authenticate(ctx, scheme, cfg.Host, cfg.APIToken)
	if err != nil {
		return fmt.Errorf("nucleus: auth: %w", err)
	}

	// Step 2: Connect to the API service (connlib protocol).
	// Retry because the ingress health-check can briefly intercept the WS upgrade.
	apiURL := fmt.Sprintf("%s://%s/omni/api", scheme, cfg.Host)
	var apiWS *websocket.Conn
	for attempt := 0; attempt < 3; attempt++ {
		apiWS, _, err = websocket.Dial(ctx, apiURL, nil)
		if err == nil {
			break
		}
		if attempt < 2 {
			time.Sleep(time.Duration(attempt+1) * time.Second)
		}
	}
	if err != nil {
		return fmt.Errorf("nucleus: connect API: %w", err)
	}

	// Increase read limit for large directory listings.
	apiWS.SetReadLimit(32 * 1024 * 1024)

	loopCtx, loopCancel := context.WithCancel(context.Background())
	conn := connlib.NewConn(apiWS)

	// Start background reader (uses its own context, not the dial context).
	go conn.StartReadLoop(loopCtx)

	// Step 3: Authorize the access token on the API connection.
	info, err := authorizeToken(ctx, conn, accessToken, cfg.UserAgent)
	if err != nil {
		loopCancel()
		conn.Close()
		return fmt.Errorf("nucleus: authorize: %w", err)
	}

	c.mu.Lock()
	if c.conn != nil {
		c.cancel()
		c.conn.Close()
		c.reconnects++
	}
	c.conn = conn
	c.cancel = loopCancel
	c.accessToken = accessToken
	c.connInfo = info
	c.mu.Unlock()
	return nil
}

// reconnect re-establishes the API connection after it died. It is a no-op
// if another caller already replaced the dead connection.
func (c *Client) reconnect(ctx context.Context, dead *connlib.Conn) error {
	// Parallel callers all notice the same dead connection; only the first
	// one dials, the rest find a healthy replacement and return.
	c.reconnectMu.Lock()
	defer c.reconnectMu.Unlock()
	c.mu.Lock()
	current := c.conn
	c.mu.Unlock()
	if current != dead && !current.Closed() {
		return nil
	}
	return c.connect(ctx)
}

// withCmd runs one connlib command exchange (request + response) while
// holding the command lock.
//
// The API service rejects a command that arrives while another one on the
// same connection is still in progress with INVALID_COMMAND (measured live:
// even two overlapping reads trip it). Commands are short — a read only
// returns an LFT ticket, the transfer itself goes over HTTP outside the
// lock — so serializing them costs little even with many parallel workers.
func (c *Client) withCmd(fn func() error) error {
	c.cmdMu.Lock()
	defer c.cmdMu.Unlock()
	return fn()
}

// api returns the current API connection.
func (c *Client) api() *connlib.Conn {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn
}

// Close cleanly shuts down the connection.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cancel()
	return c.conn.Close()
}

// Info returns the connection info from the (most recent) authorization.
func (c *Client) Info() ConnectionInfo {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connInfo
}

// Reconnects returns how many times the client re-dialed after losing the
// API connection.
func (c *Client) Reconnects() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.reconnects
}

// Conn returns the underlying connlib connection for advanced use. It may
// change after a reconnect; don't cache it.
func (c *Client) Conn() *connlib.Conn {
	return c.api()
}

// clientCapabilities declares what commands/versions this client supports.
var clientCapabilities = map[string]int{
	"ping": 0, "auth": 5, "authorize_token": 4,
	"subscribe_server_notifications": 0,
	"stat2":                          3, "list": 4, "list2": 6,
	"subscribe_list": 3, "service_subscribe_list": 1,
	"service_resolve_acl": 1,
	"create":              3, "update": 1,
	"create_asset": 2, "update_asset": 1,
	"create_asset_with_hash": 2, "update_asset_with_hash": 1,
	"create_object": 2, "update_object": 1,
	"deep_copy_object_struct": 0,
	"read":                    1,
	"read_asset_version":      0, "read_asset_resolved": 0,
	"subscribe_read_asset": 1,
	"read_object_version":  0, "read_object_resolved": 0,
	"subscribe_read_object": 2,
	"rename":                2, "rename2": 1,
	"delete": 1, "delete2": 1,
	"copy": 2, "copy2": 2,
	"create_directory": 1,
	"get_acl":          0, "get_acl_resolved": 0, "get_acl_v2": 0,
	"set_acl_v2": 0, "change_acl": 0,
	"lock": 3, "unlock": 2,
	"mount": 0, "unmount": 0,
	"get_mount_info":  0,
	"get_checkpoints": 1, "checkpoint_version": 1,
	"replace_version": 1, "get_branches": 1,
	"set_path_options": 1, "set_path_options2": 0,
	"set_user_agent": 0, "get_transaction_id": 0,
	"get_users": 0, "get_groups": 0,
	"create_group": 0, "remove_group": 0, "rename_group": 0,
	"add_user_to_group": 0, "remove_user_from_group": 0,
	"get_user_groups": 0, "get_group_users": 0,
}

// authenticate performs SOWS auth against the Auth service to get an access token.
func authenticate(ctx context.Context, scheme, host, apiToken string) (string, error) {
	authURL := fmt.Sprintf("%s://%s/omni/auth", scheme, host)
	authConn, _, err := websocket.Dial(ctx, authURL, nil)
	if err != nil {
		return "", fmt.Errorf("connect auth: %w", err)
	}
	defer authConn.CloseNow()

	msg, err := sows.BuildRequest(1, "Tokens", "auth_with_api_token", map[string]any{
		"version":   1,
		"api_token": apiToken,
	})
	if err != nil {
		return "", err
	}

	if err := authConn.Write(ctx, websocket.MessageBinary, msg); err != nil {
		return "", fmt.Errorf("send auth: %w", err)
	}

	_, data, err := authConn.Read(ctx)
	if err != nil {
		return "", fmt.Errorf("read auth response: %w", err)
	}

	resp, err := sows.ParseResponse(data)
	if err != nil {
		return "", err
	}

	if resp.Type == sows.TypeError {
		return "", fmt.Errorf("auth error %d: %s", resp.ErrorCode, resp.ErrorMessage)
	}

	var result struct {
		Status      string `json:"status"`
		AccessToken string `json:"access_token"`
		Username    string `json:"username"`
	}
	if err := json.Unmarshal(resp.ParamsJSON, &result); err != nil {
		return "", fmt.Errorf("parse auth response: %w", err)
	}

	if result.Status != "OK" {
		return "", fmt.Errorf("auth failed: %s", result.Status)
	}

	authConn.Close(websocket.StatusNormalClosure, "")
	return result.AccessToken, nil
}

// authorizeToken sends the access token to the API service.
func authorizeToken(ctx context.Context, conn *connlib.Conn, accessToken, userAgent string) (ConnectionInfo, error) {
	var info ConnectionInfo
	resp, err := conn.Call(ctx, "authorize_token", map[string]any{
		"version":             "1.19",
		"client_capabilities": clientCapabilities,
		"token":               accessToken,
		"userAgent":           userAgent,
	})
	if err != nil {
		return info, err
	}

	if err := json.Unmarshal(resp, &info); err != nil {
		return info, fmt.Errorf("parse authorize response: %w", err)
	}

	if info.Status != "OK" {
		return info, fmt.Errorf("authorize failed: %s", info.Status)
	}

	return info, nil
}
