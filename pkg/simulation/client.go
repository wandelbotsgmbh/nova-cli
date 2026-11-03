package simulation

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/wandelbotsgmbh/nova-cli/pkg/httpx"
	"github.com/wandelbotsgmbh/nova-cli/pkg/version"
)

// Broker is a client for the streaming session broker of the logged-in
// environment.
type Broker struct {
	*ClientWithResponses
	Endpoints
	// Token also authorizes the APIs running inside a session.
	Token string
}

// NewBroker returns a client for the streaming session broker of the
// logged-in environment.
func NewBroker(ctx context.Context) (*Broker, error) {
	token, endpoints, err := AccessToken(ctx)
	if err != nil {
		return nil, err
	}
	auth := func(ctx context.Context, req *http.Request) error {
		req.Header.Set("Authorization", "Bearer "+token)
		return nil
	}
	client, err := NewClientWithResponses(endpoints.Broker,
		WithHTTPClient(httpx.Client()),
		WithRequestEditorFn(auth),
		WithRequestEditorFn(version.SetUserAgent))
	if err != nil {
		return nil, err
	}
	return &Broker{ClientWithResponses: client, Endpoints: endpoints, Token: token}, nil
}

// Response is implemented by every generated *Response type.
type Response interface {
	StatusCode() int
	GetBody() []byte
}

// Check turns a failed request or a non-2xx response into an error, using the
// broker's {"error":{"code","message"}} body when there is one.
func Check(resp Response, err error) error {
	if err != nil {
		return err
	}
	body := resp.GetBody()
	status := resp.StatusCode()
	if status >= 200 && status < 300 {
		return nil
	}
	var e struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &e) == nil && e.Error.Message != "" {
		if status == http.StatusUnauthorized {
			return fmt.Errorf("broker: %s (%s), try: nova simulation login", e.Error.Message, e.Error.Code)
		}
		return fmt.Errorf("broker: %s (%s)", e.Error.Message, e.Error.Code)
	}
	return fmt.Errorf("broker: HTTP %d: %s", status, string(body))
}
