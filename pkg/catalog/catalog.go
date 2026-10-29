package catalog

type NamedEntry struct {
	Name string
}

// CatalogSvc is used to interact with different catalog stores
// e.g. github, gitlab and github pages.
type CatalogSvc interface {
	// List all entries by their names
	ListEntries() ([]NamedEntry, error)
	// Gets the manifest of an entry and the raw entry by its name
	GetManifest(name string) (CatalogEntry, []byte, error)
}
