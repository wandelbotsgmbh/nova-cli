package bootstrap

// ApplyReflector applies the HelmChart CR that deploys Ember Stack Reflector.
// Reflector mirrors Secrets and ConfigMaps across namespaces based on
// annotations (reflector.v1.k8s.emberstack.com/reflection-allowed, etc.).
// It must be installed before pull secrets are applied so that they are
// automatically mirrored to all namespaces.
func ApplyReflector(cfg Config) error {
	manifest, err := renderManifest("reflector.yaml.tmpl", cfg)
	if err != nil {
		return err
	}
	return applyManifest("reflector", manifest)
}
