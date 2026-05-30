package router

import (
	"html/template"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/RED-Collective/red-engine/internal/models"
)

func capitalize(s string) string {
	s = strings.ReplaceAll(s, "-", " ")
	s = strings.ReplaceAll(s, "_", " ")
	if s == "" {
		return s
	}
	r, size := utf8.DecodeRuneInString(s)
	return string(unicode.ToUpper(r)) + s[size:]
}

func (h *handler) serve(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

	// Handle home page routing
	if path == "/" {
		path = "/root"
	}

	parts := strings.Split(strings.Trim(path, "/"), "/")
	topCat := ""
	if parts[0] != "" {
		topCat = parts[0]
	}

	d := models.PageData{
		Site:     h.cfg.SiteName,
		NodeName: h.cfg.NodeName,
		Nav:      h.store.Root(),
		Path:     path,
		TopCat:   topCat,
	}

	switch {
	case path == "/root":
		d.Body = template.HTML(`<div class="article"><h1>` + h.cfg.SiteName + `</h1><p>The free practical knowledge base. Choose a topic from the sidebar.</p></div>`)

	case len(parts) == 1 && topCat != "root":
		sec, ok := h.store.Root()[topCat]
		if !ok {
			http.NotFound(w, r)
			return
		}
		d.Title = capitalize(topCat)
		d.Crumb = []models.Crumb{{Label: capitalize(topCat), Path: "/" + topCat}}
		d.Body = template.HTML(sectionHTML(sec))

	default:
		// First, check if the requested path is a markdown Article
		art := h.store.Get(path)
		if art == nil {
			if strings.HasSuffix(path, ".md") {
				clean := strings.TrimSuffix(path, ".md")
				art = h.store.Get(clean)
			}
		}

		if art != nil {
			d.Title = capitalize(parts[len(parts)-1])

			// MERGED FROM PROJECT-RED: Remove .md from the UI title
			d.Title = strings.TrimSuffix(d.Title, ".md")

			d.Crumb = buildCrumbs(parts)
			d.Body = art.Body
			d.Verified = art.Verified
			d.Author = art.Author
			d.Hash = art.Hash
			d.VerificationError = art.VerificationError
		} else {
			// If it's not an article, check if it's a Sub-Directory
			sec := h.store.GetSection(path)
			if sec != nil {
				d.Title = capitalize(parts[len(parts)-1])
				d.Crumb = buildCrumbs(parts)
				d.Body = template.HTML(sectionHTML(sec))
			} else {
				// If neither file nor folder exists, return 404
				http.NotFound(w, r)
				return
			}
		}
	}

	if err := h.tmpl.ExecuteTemplate(w, "base.html", d); err != nil {
		http.Error(w, "template error: "+err.Error(), 500)
		return
	}
}

func buildCrumbs(parts []string) []models.Crumb {
	crumbs := make([]models.Crumb, 0, len(parts))
	path := ""
	for _, p := range parts {
		path += "/" + p
		crumbs = append(crumbs, models.Crumb{Label: capitalize(p), Path: path})
	}
	return crumbs
}

func sectionHTML(sec *models.Section) string {
	var b strings.Builder
	b.Grow(1024)

	b.WriteString(`<div class="section-index"><h1>`)
	b.WriteString(capitalize(sec.Name))
	b.WriteString(`</h1><ul>`)

	for _, a := range sec.Articles {
		b.WriteString(`<li><a href="`)
		b.WriteString(a.Path)
		b.WriteString(`">`)
		b.WriteString(a.Title)
		b.WriteString(`</a></li>`)
	}
	b.WriteString(`</ul>`)

	for _, sub := range sec.Sub {
		b.WriteString(`<h2>`)
		b.WriteString(capitalize(sub.Name))
		b.WriteString(`</h2><ul>`)
		for _, a := range sub.Articles {
			b.WriteString(`<li><a href="`)
			b.WriteString(a.Path)
			b.WriteString(`">`)
			b.WriteString(a.Title)
			b.WriteString(`</a></li>`)
		}
		b.WriteString(`</ul>`)
	}

	b.WriteString(`</div>`)
	return b.String()
}
