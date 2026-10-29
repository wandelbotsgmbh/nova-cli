package catalog

import (
	"fmt"
	"io"
	"net/http"

	"github.com/wandelbotsgmbh/nova-cli/pkg/httpx"
	"gopkg.in/yaml.v3"
)

const (
	// the url for the current official catalog
	catalogUrl        = "https://catalog.wandelbots.io/"
	catalogUrlPreview = "https://catalog.wandelbots.io/_preview/"
)

var (
	ErrCatalogNotFound = fmt.Errorf("catalog not found")
)

// WebHosted implements the CatalogSvc interface for a web-hosted catalog.
// Hosting can be done on GitHub Pages, GitLab Pages, or any other static file hosting service.
type WebHosted struct {
	client *http.Client
	// where the catalog is hosted
	baseUrl string
	// for identifying the catalog when mutliple are hosted
	prefix string
}

type CatalogListing struct {
	Name    string `yaml:"name"`
	Version string `yaml:"version"`
}

type CatalogManifest struct {
	SchemaVersion string           `yaml:"schemaVersion"`
	Version       string           `yaml:"version"`
	Entries       []CatalogListing `yaml:"entries"`
}

// Creates a catalog service based on a url (currently the default)
func NewDefault(usePreview bool) (*WebHosted, error) {
	baseUrl := catalogUrl
	if usePreview {
		baseUrl = catalogUrlPreview
	}
	httpClient := httpx.Client()
	return &WebHosted{
		client:  httpClient,
		baseUrl: baseUrl,
		// the default catalog needs no prefix
		prefix: "",
	}, nil
}

// Creates a catalog service based on a custom url
func NewWebHosted(prefix, baseUrl string) (*WebHosted, error) {
	httpClient := httpx.Client()
	return &WebHosted{
		client:  httpClient,
		baseUrl: baseUrl,
		prefix:  prefix,
	}, nil
}

// Loads the catalog from the config file
func NewFromConfig(name string) (*WebHosted, error) {
	c, err := ReadConfig()
	if err != nil {
		return nil, err
	}
	for _, entry := range c.Entries {
		if entry.Name == name {
			return NewWebHosted(entry.Name, entry.Url)
		}
	}
	return nil, ErrCatalogNotFound
}

func (web *WebHosted) ListEntries() ([]NamedEntry, error) {
	resp, err := web.client.Get(web.baseUrl + "catalog.yaml")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("failed to fetch catalog: %s", resp.Status)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var catalog CatalogManifest
	err = yaml.Unmarshal(data, &catalog)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal catalog.yaml: %w", err)
	}

	entries := make([]NamedEntry, 0, len(catalog.Entries))
	for _, entry := range catalog.Entries {
		entries = append(entries, NamedEntry{Name: entry.Name})
	}

	return entries, err
}

func (web *WebHosted) GetManifest(name string) (CatalogEntry, []byte, error) {
	uri := web.baseUrl + name + "/manifest.yaml"
	resp, err := web.client.Get(uri)
	if err != nil {
		return CatalogEntry{}, nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return CatalogEntry{}, nil, fmt.Errorf("failed to fetch catalog entry '%s': %s", name, resp.Status)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return CatalogEntry{}, nil, err
	}

	var entry CatalogEntry
	err = yaml.Unmarshal(data, &entry)
	if err != nil {
		return CatalogEntry{}, data, fmt.Errorf("failed to unmarshal %s/manifest.yaml: %w", name, err)
	}

	return entry, data, nil

}
