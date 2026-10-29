package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// UserInfo holds the fields returned by the Auth0 /userinfo endpoint.
type UserInfo struct {
	Email string `json:"email"`
}

// FetchUserInfo calls the Auth0 /userinfo endpoint using the current access
// token and returns the authenticated user's profile information.
func FetchUserInfo(ctx context.Context, env AuthEnvironment) (UserInfo, error) {
	token, err := LoadToken()
	if err != nil {
		return UserInfo{}, fmt.Errorf("load token: %w", err)
	}

	if token.OAuth2Token.AccessToken == "" {
		return UserInfo{}, fmt.Errorf("no access token available")
	}

	url := Auth0Domain(env) + "/userinfo"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return UserInfo{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token.OAuth2Token.AccessToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return UserInfo{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return UserInfo{}, fmt.Errorf("userinfo returned %d: %s", resp.StatusCode, string(body))
	}

	var info UserInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return UserInfo{}, err
	}
	return info, nil
}
