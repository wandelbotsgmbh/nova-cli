package nova

import (
	"fmt"
	"io"
	"net/http"

	"github.com/wandelbotsgmbh/nova-cli/pkg/httpx"
	"github.com/wandelbotsgmbh/nova-cli/pkg/oauth"
)

// GetOpenAPISpec returns the OpenAPI specification for the NOVA API.
// TODO auth for cloud instances
func GetOpenAPISpec(host string) ([]byte, error) {
	specURL := SpecURL(host)
	client := httpx.Client()

	req, err := http.NewRequest("GET", specURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request for spec URL %s: %w", specURL, err)
	}

	oauth.SetAuthForRequest(req)
	req.Header.Set("Accept", "application/yaml, application/x-yaml, text/yaml, */*")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch spec from URL %s: %w", specURL, err)
	}
	defer resp.Body.Close()

	if !StatusSuccessfull(resp.StatusCode) {
		// Attempt to read body for more details on error
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed to fetch spec from URL %s: %s\nResponse Body: %s", specURL, resp.Status, string(bodyBytes))
	}

	specContent, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read spec response body from %s: %w", specURL, err)
	}
	return specContent, nil
}

func SpecURL(host string) string {
	host = HostWithProtocol(host)
	return fmt.Sprintf("%s/api/v1/ui/public.openapi.yaml", host)
}
