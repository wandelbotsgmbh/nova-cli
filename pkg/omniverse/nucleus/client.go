package nucleus

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/coder/websocket"

	"github.com/wandelbotsgmbh/nova-cli/pkg/omniverse/nucleus/connlib"
	"github.com/wandelbotsgmbh/nova-cli/pkg/omniverse/nucleus/sows"
)

// Client is a Nucleus API client. Use [Dial] to create one.
type Client struct {
	cfg         ClientConfig
	conn        *connlib.Conn
	cancel      context.CancelFunc // cancels the read loop
	accessToken string             // JWT from auth service, needed for LFT

	connInfo ConnectionInfo
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

	scheme := "wss"
	if !cfg.TLS {
		scheme = "ws"
	}

	// Step 1: Authenticate via the Auth service (SOWS protocol) to get an access token.
	accessToken, err := authenticate(ctx, scheme, cfg.Host, cfg.APIToken)
	if err != nil {
		return nil, fmt.Errorf("nucleus: auth: %w", err)
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
		return nil, fmt.Errorf("nucleus: connect API: %w", err)
	}

	// Increase read limit for large directory listings.
	apiWS.SetReadLimit(32 * 1024 * 1024)

	loopCtx, loopCancel := context.WithCancel(context.Background())

	c := &Client{
		cfg:         cfg,
		conn:        connlib.NewConn(apiWS),
		cancel:      loopCancel,
		accessToken: accessToken,
	}

	// Start background reader (uses its own context, not the dial context).
	go c.conn.StartReadLoop(loopCtx)

	// Step 3: Authorize the access token on the API connection.
	if err := c.authorizeToken(ctx, accessToken); err != nil {
		c.conn.Close()
		return nil, fmt.Errorf("nucleus: authorize: %w", err)
	}

	return c, nil
}

// Close cleanly shuts down the connection.
func (c *Client) Close() error {
	c.cancel()
	return c.conn.Close()
}

// Info returns the connection info from the initial authorization.
func (c *Client) Info() ConnectionInfo {
	return c.connInfo
}

// Conn returns the underlying connlib connection for advanced use.
func (c *Client) Conn() *connlib.Conn {
	return c.conn
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
func (c *Client) authorizeToken(ctx context.Context, accessToken string) error {
	resp, err := c.conn.Call(ctx, "authorize_token", map[string]any{
		"version":             "1.19",
		"client_capabilities": clientCapabilities,
		"token":               accessToken,
		"userAgent":           c.cfg.UserAgent,
	})
	if err != nil {
		return err
	}

	if err := json.Unmarshal(resp, &c.connInfo); err != nil {
		return fmt.Errorf("parse authorize response: %w", err)
	}

	if c.connInfo.Status != "OK" {
		return fmt.Errorf("authorize failed: %s", c.connInfo.Status)
	}

	return nil
}
