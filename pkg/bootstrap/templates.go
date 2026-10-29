package bootstrap

import (
	"embed"
	"fmt"
	"strings"
	"text/template"
)

// templatesFS holds the manifest templates rendered by the bootstrap package.
// Embedding keeps a single self-contained binary while the templates live as
// reviewable, lintable YAML files.
//
//go:embed templates
var templatesFS embed.FS

// manifestTemplates holds every default ({{ }}-delimited) manifest template.
// They are parsed as one set so partials can be shared if needed.
var manifestTemplates = template.Must(
	template.New("bootstrap").ParseFS(templatesFS, "templates/*.yaml.tmpl"))

// caPolicyTemplate is parsed separately with [[ ]] delimiters so Kyverno's own
// {{ }} variables (e.g. {{ element.name }}) survive verbatim in the output.
var caPolicyTemplate = template.Must(
	template.New("ca-policy").Delims("[[", "]]").
		ParseFS(templatesFS, "templates/ca-policy.kyverno.tmpl"))

// renderManifest executes a named manifest template (by file base name) into a
// string.
func renderManifest(name string, data any) (string, error) {
	var sb strings.Builder
	if err := manifestTemplates.ExecuteTemplate(&sb, name, data); err != nil {
		return "", fmt.Errorf("render %s: %w", name, err)
	}
	return sb.String(), nil
}
