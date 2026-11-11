package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/olekukonko/tablewriter"
	"github.com/olekukonko/tablewriter/renderer"
	"github.com/olekukonko/tablewriter/tw"
	"github.com/spf13/cobra"
	apiv1 "github.com/wandelbotsgmbh/nova-api-client-go/v25/pkg/nova"
	apiv2 "github.com/wandelbotsgmbh/nova-api-client-go/v25/pkg/nova/v2"
	"github.com/wandelbotsgmbh/nova-cli/pkg/catalog"
	"github.com/wandelbotsgmbh/nova-cli/pkg/nova"
)

var (
	// if set list also internal entries
	catalogListAll bool
	// if set use the preview catalog
	catalogUsePreview bool
)

var (
	errCatalogEntryNotSupportedInCloud = errors.New("catalog entry does not support cloud deployment")
)

var catalogCmd = &cobra.Command{
	Use:   "catalog",
	Short: "Manage catalog entries",
	Long:  `Manage catalog entries`,
}

var catalogListCmd = &cobra.Command{
	Use:   "list [name]",
	Short: "List catalog entries",
	Long:  `List catalog entries and optionally all entries from additional catalogs`,
	Example: `
# list all entries from the default catalog
nova catalog list

# list all entries from the default catalog and all entries from additional catalogs
nova catalog list --all
nova catalog list -a

# list all entries from a specific catalog
nova catalog list mycatalog
`,
	Args:              cobra.MaximumNArgs(1),
	ValidArgsFunction: completeExternalCatalog,
	RunE: func(cmd *cobra.Command, args []string) error {
		var entries []catalogListItem
		add := func(c *catalog.WebHosted, name string) error {
			e, err := c.ListEntries()
			if err != nil {
				return err
			}
			for _, entry := range e {
				entries = append(entries, catalogListItem{Name: entry.Name, Catalog: name})
			}
			return nil
		}

		// list entries from a specific catalog
		if len(args) > 0 {
			c, err := catalog.NewFromConfig(args[0])
			if err != nil {
				return err
			}
			if err := add(c, args[0]); err != nil {
				return err
			}
			return writeCatalogList(cmd, entries, false)
		}

		// list entries from the default catalog
		c, err := catalog.NewDefault(catalogUsePreview)
		if err != nil {
			return err
		}
		if err := add(c, ""); err != nil {
			return err
		}

		// list entries from all catalogs
		if catalogListAll {
			c, _ := catalog.ReadConfig()
			for _, entry := range c.Entries {
				cat, err := catalog.NewWebHosted(entry.Name, entry.Url)
				if err != nil {
					return err
				}
				if err := add(cat, entry.Name); err != nil {
					return err
				}
			}
		}

		return writeCatalogList(cmd, entries, true)
	},
}

// catalogListItem is an entry of 'catalog list -o json'.
type catalogListItem struct {
	Name string `json:"name"`
	// the additional catalog the entry comes from; "" for the default catalog
	Catalog string `json:"catalog"`
}

// writeCatalogList prints the entries one per line; with qualify, entries of
// additional catalogs as <catalog>/<name>, as 'catalog install' takes them.
func writeCatalogList(cmd *cobra.Command, entries []catalogListItem, qualify bool) error {
	if *catalogListOutput == outputJSON {
		if entries == nil {
			entries = []catalogListItem{}
		}
		return writeJSON(cmd, entries)
	}
	for _, e := range entries {
		if qualify && e.Catalog != "" {
			fmt.Fprintln(cmd.OutOrStdout(), e.Catalog+"/"+e.Name)
		} else {
			fmt.Fprintln(cmd.OutOrStdout(), e.Name)
		}
	}
	return nil
}

// -o/--output of 'catalog list'
var catalogListOutput *outputFormat

var catalogInstallCmd = &cobra.Command{
	Use:               "install <name>",
	Short:             "Install from catalog",
	Long:              `Install from catalog`,
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: completeCatalog,
	RunE: func(cmd *cobra.Command, args []string) error {
		host := currentHost()
		cell := currentCell()

		c, err := catalog.NewDefault(catalogUsePreview)
		if err != nil {
			return err
		}

		catalogName, entryName := splitEntryName(args[0])

		if catalogName != "" {
			c, err = catalog.NewFromConfig(catalogName)
			if err != nil {
				return err
			}
		}

		m, _, err := c.GetManifest(entryName)
		if err != nil {
			return err
		}

		if m.SupportsCloud != nil && !*m.SupportsCloud && strings.Contains(host, nova.WABO_CLOUD_HOST) {
			return fmt.Errorf("%w: %s", errCatalogEntryNotSupportedInCloud, entryName)
		}

		appName := m.App.Name
		if m.App.Name == "" {
			fmt.Fprintln(os.Stderr, "WARN: no app name found in app manifest, using entry name as app name")
			appName = entryName
		}

		err = installFromCatalog(host, cell, appName, m)
		if err != nil {
			return err
		}

		return nil
	},
}

var catalogInfoCmd = &cobra.Command{
	Use:               "info <name>",
	Short:             "Info for specific catalog entry",
	Long:              `Info for specific catalog entry`,
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: completeCatalog,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := catalog.NewDefault(catalogUsePreview)
		if err != nil {
			return err
		}

		catalogName, entryName := splitEntryName(args[0])

		if catalogName != "" {
			c, err = catalog.NewFromConfig(catalogName)
			if err != nil {
				return err
			}
		}

		m, _, err := c.GetManifest(entryName)

		if err != nil {
			return err
		}

		if *catalogInfoOutput == outputJSON {
			deps := make([]namedItem, 0, len(m.Dependencies))
			for _, d := range m.Dependencies {
				deps = append(deps, namedItem{Name: d.Name})
			}
			return writeJSON(cmd, catalogInfo{
				Name:          entryName,
				Catalog:       catalogName,
				Version:       m.Version,
				Description:   m.Description,
				Homepage:      m.Homepage,
				Image:         m.App.ContainerImage.Image,
				SupportsCloud: m.SupportsCloud,
				Dependencies:  deps,
			})
		}
		fmt.Fprintln(cmd.OutOrStdout(), m.Description)
		return nil
	},
}

// catalogInfo is the JSON result of 'catalog info -o json'.
type catalogInfo struct {
	Name string `json:"name"`
	// the additional catalog the entry comes from; "" for the default catalog
	Catalog     string `json:"catalog"`
	Version     string `json:"version"`
	Description string `json:"description"`
	Homepage    string `json:"homepage"`
	// container image of the app
	Image string `json:"image"`
	// null when the manifest doesn't say
	SupportsCloud *bool `json:"supports_cloud"`
	// catalog entries installed along with this one
	Dependencies []namedItem `json:"dependencies"`
}

var catalogAddCmd = &cobra.Command{
	Use:   "add <name> <url>",
	Short: "Add a catalog",
	Long:  `Add a catalog e.g. internal hosted catalog which is only reachable within the company VPN.`,
	Args:  cobra.ExactArgs(2),
	Example: `
# add a catalog
nova catalog add mycatalog https://backend.pages.company.gitlab.run/catalog/

# apps from this catalog can now be installed with
nova catalog install mycatalog/myapp
`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return catalog.AddCatalog(args[0], args[1])
	},
}

var catalogRemoveCmd = &cobra.Command{
	Use:               "remove <name>",
	Short:             "Remove a catalog",
	Long:              `Remove a catalog.`,
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: completeExternalCatalog,
	Example: `
# removes a catalog
nova catalog remove mycatalog
`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return catalog.RemoveCatalog(args[0])
	},
}

var catalogHostsCmd = &cobra.Command{
	Use:   "hosts",
	Short: "List all catalog hosts with their names",
	Long:  `List all catalog hosts with their names`,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := catalog.ReadConfig()
		if err != nil {
			return err
		}

		if *catalogHostsOutput == outputJSON {
			hosts := make([]catalogHost, 0, len(c.Entries))
			for _, entry := range c.Entries {
				hosts = append(hosts, catalogHost{Name: entry.Name, URL: entry.Url})
			}
			return writeJSON(cmd, hosts)
		}

		// keep the ASCII borders of tablewriter v0
		table := tablewriter.NewTable(cmd.OutOrStdout(),
			tablewriter.WithRenderer(renderer.NewBlueprint(tw.Rendition{Symbols: tw.NewSymbols(tw.StyleASCII)})))
		table.Header([]string{"Name", "URL"})

		for _, entry := range c.Entries {
			if err := table.Append([]string{entry.Name, entry.Url}); err != nil {
				return err
			}
		}
		return table.Render()
	},
}

// catalogHost is an entry of 'catalog hosts -o json'.
type catalogHost struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// -o/--output of 'catalog info' and 'catalog hosts'
var catalogInfoOutput, catalogHostsOutput *outputFormat

func completeExternalCatalog(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	var comp []string
	c, _ := catalog.ReadConfig()
	for _, entry := range c.Entries {
		if toComplete == "" || strings.HasPrefix(entry.Name, toComplete) {
			return []string{entry.Name}, cobra.ShellCompDirectiveNoFileComp
		}
	}
	return comp, cobra.ShellCompDirectiveNoFileComp
}

func completeCatalog(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	c, err := catalog.NewDefault(catalogUsePreview)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	entries, err := c.ListEntries()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	var comp []string
	for _, entry := range entries {
		if toComplete == "" || strings.HasPrefix(entry.Name, toComplete) {
			comp = append(comp, entry.Name)
		}
	}

	return comp, cobra.ShellCompDirectiveNoFileComp
}

func installFromCatalog(host, cell, appName string, entry catalog.CatalogEntry) error {
	_, err := nova.CreateCellIfNotExists(host, cell)
	if err != nil {
		return err
	}

	updateApp, err := appAllreadyInstalled(host, cell, appName)
	if err != nil {
		return err
	}

	// Use V2 app config (wrapper functions will automatically detect V1 or V2 API)
	appConfigRaw, err := mapCatalogEntryToApiV2App(entry.App)
	if err != nil {
		return err
	}

	if updateApp {
		fmt.Fprintf(os.Stderr, "App '%s' already installed. Updating it...\n", appName)
		return nova.UpdateAppConfigurationFromJson(host, cell, appName, appConfigRaw)
	} else {
		fmt.Fprintf(os.Stderr, "Installing app '%s'...\n", appName)
		return nova.AddAppFromJson(host, cell, appName, appConfigRaw)
	}
}

// converts a catalog.CatalogEntryApp to apiv2.App configuration
func mapCatalogEntryToApiV2App(catalogApp catalog.CatalogEntryApp) ([]byte, error) {
	appConfig := apiv2.App{
		Name:    catalogApp.Name,
		Port:    &catalogApp.Port,
		AppIcon: catalogApp.AppIcon,
		ContainerImage: apiv2.ContainerImage{
			Image:   catalogApp.ContainerImage.Image,
			Secrets: mapCatalogSecretsToApiV2Secrets(catalogApp.ContainerImage.Secrets),
		},
	}

	// Handle Resources
	if catalogApp.Resources != nil {
		appConfig.Resources = &apiv2.ContainerResources{}

		if catalogApp.Resources.IntelGpu != nil {
			appConfig.Resources.IntelGpu = catalogApp.Resources.IntelGpu
		}

		if catalogApp.Resources.MemoryLimit != nil {
			appConfig.Resources.MemoryLimit = catalogApp.Resources.MemoryLimit
		}
	}

	// Handle Storage
	if catalogApp.Storage != nil {
		appConfig.Storage = &apiv2.ContainerStorage{
			Capacity:  catalogApp.Storage.Capacity,
			MountPath: catalogApp.Storage.MountPath,
		}
	}

	return json.Marshal(appConfig)
}

// converts a catalog.CatalogEntryApp to apiv1.App configuration
func mapCatalogEntryToApiV1App(catalogApp catalog.CatalogEntryApp) ([]byte, error) {
	appConfig := apiv1.App{
		Name:    catalogApp.Name,
		Port:    &catalogApp.Port,
		AppIcon: catalogApp.AppIcon,
		ContainerImage: apiv1.ContainerImage{
			Image: catalogApp.ContainerImage.Image,
		},
	}

	// Handle Resources
	if catalogApp.Resources != nil {
		if catalogApp.Resources.IntelGpu != nil {
			appConfig.Resources.IntelGpu = catalogApp.Resources.IntelGpu
		}

		if catalogApp.Resources.MemoryLimit != nil {
			fmt.Fprintf(os.Stderr, "WARN: MemoryLimit is not supported in NOVA API v1, "+
				"skipping this setting. If you need this feature, consider upgrade to nova >=%s\n", appUseNovaV2SinceVersion)
		}
	}

	// Handle Storage
	if catalogApp.Storage != nil {
		appConfig.Storage = &apiv1.ContainerStorage{
			Capacity:  catalogApp.Storage.Capacity,
			MountPath: catalogApp.Storage.MountPath,
		}
	}

	// Handle Environment - initialize empty for v1
	appConfig.Environment = &[]struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	}{}

	return json.Marshal(appConfig)
}

// returns the prefix and the name of the entry
func splitEntryName(entryName string) (string, string) {
	parts := strings.Split(entryName, "/")
	if len(parts) == 1 {
		return "", parts[0]
	}
	return parts[0], strings.Join(parts[1:], "/")
}

// converts catalog secrets to Nova API v2 format
func mapCatalogSecretsToApiV2Secrets(catalogSecrets []catalog.CatalogEntryAppContainerImageSecretsElem) *[]struct {
	Name string `json:"name"`
} {
	if len(catalogSecrets) == 0 {
		return nil
	}

	secrets := make([]struct {
		Name string `json:"name"`
	}, len(catalogSecrets))

	for i, secret := range catalogSecrets {
		secrets[i] = struct {
			Name string `json:"name"`
		}{
			Name: secret.Name,
		}
	}

	return &secrets
}

func init() {
	catalogListOutput = addOutputFlag(catalogListCmd)
	catalogInfoOutput = addOutputFlag(catalogInfoCmd)
	catalogHostsOutput = addOutputFlag(catalogHostsCmd)
	catalogCmd.AddCommand(catalogListCmd)
	catalogCmd.AddCommand(catalogInstallCmd)
	catalogCmd.AddCommand(catalogInfoCmd)
	catalogCmd.AddCommand(catalogAddCmd)
	catalogCmd.AddCommand(catalogRemoveCmd)
	catalogCmd.AddCommand(catalogHostsCmd)
	rootCmd.AddCommand(catalogCmd)

	catalogListCmd.Flags().BoolVarP(&catalogListAll, "all", "a", false, "list all entries including internal ones")
	catalogCmd.PersistentFlags().BoolVar(&catalogUsePreview, "preview", false, "use the preview catalog")
}
