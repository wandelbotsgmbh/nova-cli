package renderer

import (
	"embed"
	"html/template"
	"io"

	"github.com/labstack/echo/v4"
)

//go:embed views/*.html
var views embed.FS

type Renderer struct {
	templates *template.Template
}

func (t *Renderer) Render(w io.Writer, name string, data any, c echo.Context) error {
	return t.templates.ExecuteTemplate(w, name, data)
}

func NewRenderer() *Renderer {
	return &Renderer{
		templates: template.Must(template.ParseFS(views, "views/*.html")),
	}
}
