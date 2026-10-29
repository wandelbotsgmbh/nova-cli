package bootstrap

// ApplyNamespace creates the target namespace if it doesn't already exist.
func ApplyNamespace(cfg Config) error {
	manifest, err := renderManifest("namespace.yaml.tmpl", cfg)
	if err != nil {
		return err
	}
	return applyManifest("namespace", manifest)
}
