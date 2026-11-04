package nova

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	apiv1 "github.com/wandelbotsgmbh/nova-api-client-go/v25/pkg/nova"
	"github.com/wandelbotsgmbh/nova-cli/pkg/oauth"
)

// license endpoints available from version 25.2.0
const licenseSinceVersion = "25.2.0"

var (
	errLicenseFeatureVersion = errors.New("license feature is only available with nova version starting from " + licenseSinceVersion)
)

// isLicenseNotInstalled reports whether a request failed because the host has no
// license: it then answers every API request, the version too, with
// 403 {"code":403,"message":"license not installed"}.
func isLicenseNotInstalled(err error) bool {
	return err != nil && strings.Contains(err.Error(), "403") && strings.Contains(err.Error(), "license not installed")
}

// errNoLogin names the environment whose login the license activation needs (the
// active one, see --env / NOVA_ENV).
func errNoLogin() error {
	env := oauth.ActiveEnvironment()
	return fmt.Errorf("not logged in to the %s environment, the license activation sends that login to the instance as its owner. please login with 'nova login --env %s' or pick another environment with --env", env, env)
}

func activateLicenseV1Impl(host string) error {
	client, err := NewClientWithToken(host)
	if err != nil {
		return err
	}

	licenseNotSupported, err := IsCurrentVersionLessThan(host, licenseSinceVersion)
	if err != nil {
		return err
	}

	if licenseNotSupported {
		return errLicenseFeatureVersion
	}

	token, err := oauth.LoadToken()
	if err != nil {
		return errNoLogin()
	}

	if token.OAuth2Token.RefreshToken == "" {
		return errNoLogin()
	}

	body := apiv1.ActivateLicenseJSONRequestBody{
		OwnerRefreshToken: token.OAuth2Token.RefreshToken,
	}

	resp, err := client.ActivateLicense(context.TODO(), body)
	if err != nil {
		return err
	}

	if !StatusSuccessfull(resp.StatusCode) {
		return fmt.Errorf("failed to activate license: %s", resp.Status)
	}
	return nil
}

func getLicenseV1Impl(host string) ([]byte, error) {
	client, err := NewClientWithToken(host)
	if err != nil {
		return nil, err
	}

	licenseNotSupported, err := IsCurrentVersionLessThan(host, licenseSinceVersion)
	if err != nil {
		return nil, err
	}

	if licenseNotSupported {
		return nil, errLicenseFeatureVersion
	}

	resp, err := client.GetLicense(context.TODO())
	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	if !StatusSuccessfull(resp.StatusCode) {
		return nil, fmt.Errorf("failed to get license: %s", resp.Status)
	}

	licenseAsJson, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	return ConvertJsonToYamlString(licenseAsJson)
}
