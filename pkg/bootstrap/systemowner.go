package bootstrap

import (
	"context"
	"fmt"

	"github.com/wandelbotsgmbh/nova-cli/pkg/oauth"
)

// WriteSystemOwner writes the system-owner Secret manifest containing the
// authenticated user's email and refresh token. The Service Manager Backend
// reads this secret at runtime for unattended portal API communication and
// license activation.
func WriteSystemOwner(ctx context.Context, cfg Config) error {
	env, err := loadEnvironment()
	if err != nil {
		return err
	}

	token, err := oauth.LoadToken()
	if err != nil {
		return fmt.Errorf("load token: %w (did you run 'nova login'?)", err)
	}

	if token.OAuth2Token.RefreshToken == "" {
		return fmt.Errorf("no refresh token available (did you run 'nova login'?)")
	}

	info, err := oauth.FetchUserInfo(ctx, env)
	if err != nil {
		return fmt.Errorf("fetch user info: %w", err)
	}

	if info.Email == "" {
		return fmt.Errorf("no email in user info response")
	}

	manifest, err := renderManifest("system-owner.yaml.tmpl", struct {
		Namespace    string
		Email        string
		RefreshToken string
	}{
		Namespace:    cfg.Namespace,
		Email:        info.Email,
		RefreshToken: token.OAuth2Token.RefreshToken,
	})
	if err != nil {
		return err
	}

	return applyManifest("system-owner", manifest)
}
