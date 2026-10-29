package catalog

import (
	"testing"
)

// Compile-time assertions to ensure that CatalogSvc is implemented by different providers
// even though some of them are not used (yet).
func TestImplementsCatalogService(t *testing.T) {
	var _ CatalogSvc = (*WebHosted)(nil)
}
