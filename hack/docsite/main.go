// Renders the cobra-generated markdown (nova gendoc) into a static HTML site
// styled with the NOVA design system tokens. See build.sh for the full build
// (fresh markdown + vendored design-system assets).
package main

import (
	"bytes"
	"embed"
	"flag"
	"fmt"
	"html/template"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

//go:embed templates/page.html.tmpl assets/*
var files embed.FS

const rootCommand = "nova"

type page struct {
	Name     string // command path, e.g. "nova app install"
	Short    string
	File     string // output file name
	Content  template.HTML
	Children []*page
	Parent   *page
}

type pageData struct {
	Page    *page
	Root    *page
	Crumbs  []*page
	Version string
	Repo    string
}

func main() {
	src := flag.String("src", "docs", "directory with the cobra-generated markdown files")
	out := flag.String("out", "_site", "output directory")
	version := flag.String("version", "dev", "CLI version shown in the header")
	repo := flag.String("repo", "https://github.com/wandelbotsgmbh/nova-cli", "repository link shown in the header")
	flag.Parse()

	if err := run(*src, *out, *version, *repo); err != nil {
		log.Fatal(err)
	}
}

func run(src, out, version, repo string) error {
	md := goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithParserOptions(
			parser.WithAutoHeadingID(),
			parser.WithASTTransformers(util.Prioritized(linkRewriter{}, 100)),
		),
		goldmark.WithRendererOptions(
			renderer.WithNodeRenderers(util.Prioritized(rawHTMLEscaper{}, 100)),
		),
	)

	mdFiles, err := filepath.Glob(filepath.Join(src, "*.md"))
	if err != nil {
		return err
	}
	if len(mdFiles) == 0 {
		return fmt.Errorf("no markdown files found in %s", src)
	}

	pages := map[string]*page{}
	for _, f := range mdFiles {
		p, err := renderPage(md, f)
		if err != nil {
			return fmt.Errorf("%s: %w", f, err)
		}
		pages[p.Name] = p
	}

	root, ok := pages[rootCommand]
	if !ok {
		return fmt.Errorf("root command page %q not found", rootCommand)
	}
	for _, p := range pages {
		if p == root {
			continue
		}
		parent := pages[p.Name[:strings.LastIndex(p.Name, " ")]]
		if parent == nil {
			return fmt.Errorf("no parent page for %q", p.Name)
		}
		p.Parent = parent
		parent.Children = append(parent.Children, p)
	}
	for _, p := range pages {
		sort.Slice(p.Children, func(i, j int) bool { return p.Children[i].Name < p.Children[j].Name })
	}

	tmpl, err := template.New("page.html.tmpl").Funcs(template.FuncMap{
		"leaf": func(name string) string { return name[strings.LastIndex(name, " ")+1:] },
		// args for the recursive "nav" template: the entry and the current page
		"dict": func(p, cur *page) map[string]*page { return map[string]*page{"p": p, "cur": cur} },
		"isAncestor": func(candidate, p *page) bool {
			for ; p != nil; p = p.Parent {
				if p == candidate {
					return true
				}
			}
			return false
		},
	}).ParseFS(files, "templates/page.html.tmpl")
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Join(out, "assets"), 0o755); err != nil {
		return err
	}
	for _, p := range pages {
		var crumbs []*page
		for c := p.Parent; c != nil; c = c.Parent {
			crumbs = append([]*page{c}, crumbs...)
		}
		var buf bytes.Buffer
		if err := tmpl.Execute(&buf, pageData{Page: p, Root: root, Crumbs: crumbs, Version: version, Repo: repo}); err != nil {
			return fmt.Errorf("%s: %w", p.Name, err)
		}
		if err := os.WriteFile(filepath.Join(out, p.File), buf.Bytes(), 0o644); err != nil {
			return err
		}
	}

	assets, err := files.ReadDir("assets")
	if err != nil {
		return err
	}
	for _, a := range assets {
		b, err := files.ReadFile("assets/" + a.Name())
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(out, "assets", a.Name()), b, 0o644); err != nil {
			return err
		}
	}
	// GitHub Pages would otherwise run the site through Jekyll.
	if err := os.WriteFile(filepath.Join(out, ".nojekyll"), nil, 0o644); err != nil {
		return err
	}

	log.Printf("rendered %d pages to %s", len(pages), out)
	return nil
}

// renderPage converts one cobra markdown file. Cobra pages start with
// "## <command path>" followed by the short description; the title becomes the
// page heading in the template, so it's dropped from the body.
func renderPage(md goldmark.Markdown, path string) (*page, error) {
	source, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	doc := md.Parser().Parse(text.NewReader(source))

	title, ok := doc.FirstChild().(*ast.Heading)
	if !ok {
		return nil, fmt.Errorf("expected the page to start with a heading")
	}
	p := &page{
		Name: string(title.Lines().Value(source)),
		File: htmlName(filepath.Base(path)),
	}
	if para, ok := title.NextSibling().(*ast.Paragraph); ok {
		p.Short = string(para.Lines().Value(source))
	}
	doc.RemoveChild(doc, title)

	var buf bytes.Buffer
	if err := md.Renderer().Render(&buf, source, doc); err != nil {
		return nil, err
	}
	p.Content = template.HTML(buf.String())
	return p, nil
}

// htmlName maps a cobra markdown file name to its page; the root command
// becomes the landing page.
func htmlName(mdName string) string {
	if mdName == rootCommand+".md" {
		return "index.html"
	}
	return strings.TrimSuffix(mdName, ".md") + ".html"
}

// linkRewriter points relative links between the markdown files to the
// rendered pages.
type linkRewriter struct{}

func (linkRewriter) Transform(doc *ast.Document, _ text.Reader, _ parser.Context) {
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if l, ok := n.(*ast.Link); ok && entering {
			dest := string(l.Destination)
			if strings.HasSuffix(dest, ".md") && !strings.Contains(dest, "://") && !strings.Contains(dest, "/") {
				l.Destination = []byte(htmlName(dest))
			}
		}
		return ast.WalkContinue, nil
	})
}

// rawHTMLEscaper shows inline/block HTML as text instead of dropping it: cobra
// help texts use placeholders like <your-registry>, which markdown would
// otherwise treat as (unsafe) HTML tags and omit.
type rawHTMLEscaper struct{}

func (rawHTMLEscaper) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(ast.KindRawHTML, func(w util.BufWriter, source []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering {
			segs := n.(*ast.RawHTML).Segments
			for i := 0; i < segs.Len(); i++ {
				seg := segs.At(i)
				_, _ = w.WriteString(template.HTMLEscapeString(string(seg.Value(source))))
			}
		}
		return ast.WalkSkipChildren, nil
	})
	reg.Register(ast.KindHTMLBlock, func(w util.BufWriter, source []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering {
			b := n.(*ast.HTMLBlock)
			_, _ = w.WriteString("<p>")
			lines := b.Lines()
			for i := 0; i < lines.Len(); i++ {
				seg := lines.At(i)
				_, _ = w.WriteString(template.HTMLEscapeString(string(seg.Value(source))))
			}
			if b.HasClosure() {
				_, _ = w.WriteString(template.HTMLEscapeString(string(b.ClosureLine.Value(source))))
			}
			_, _ = w.WriteString("</p>\n")
		}
		return ast.WalkSkipChildren, nil
	})
}
