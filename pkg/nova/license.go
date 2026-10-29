package nova

import (
	"context"
	"errors"
	"fmt"
	"io"

	apiv1 "github.com/wandelbotsgmbh/nova-api-client-go/v25/pkg/nova"
	"github.com/wandelbotsgmbh/nova-cli/pkg/oauth"
)

// license endpoints available from version 25.2.0
const licenseSinceVersion = "25.2.0"

var (
	errNoTokenFound          = errors.New("no token found. please login first with 'nova login'")
	errLicenseFeatureVersion = errors.New("license feature is only available with nova version starting from " + licenseSinceVersion)
)

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
		return errNoTokenFound
	}

	if token.OAuth2Token.RefreshToken == "" {
		return errNoTokenFound
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
