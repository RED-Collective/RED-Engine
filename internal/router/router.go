package router

import (
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"html/template"
	"io/fs"
	"net/http"
	"os" // <-- for DEV_MODE check

	"github.com/RED-Collective/red-engine/internal/config"
	"github.com/RED-Collective/red-engine/internal/store"
)

//go:embed templates static
var files embed.FS

type handler struct {
	store     *store.Store
	tmpl      *template.Template
	adminTmpl *template.Template
	cfg       *config.Config
	cfgPath   string
}

func (h *handler) adminOnly(param any) func(http.ResponseWriter, *http.Request) {
	panic("unimplemented")
}

func (h *handler) adminOnly(param any) func(http.ResponseWriter, *http.Request) {
	panic("unimplemented")
}

func New(s *store.Store, cfg *config.Config, cfgPath string) http.Handler {
	var tmpl *template.Template
	var adminTmpl *template.Template
	var staticFS http.FileSystem

	devMode := os.Getenv("DEV_MODE") == "true"

	if devMode {
		// ---------- DEV MODE: read templates and static files from disk ----------
		// Templates: parse from disk each time (or cache with a watcher, but simple re-parse on every request is fine for dev)
		// To avoid parsing on every request, we'll wrap the template loading in a function that re-parses when files change.
		// For simplicity, we'll use a helper that re-parses on each request (acceptable for development).
		// But we can also parse once and rely on air/restart – your choice.
		// Here we parse once from disk (still requires restart on template change, but static files are live).
		// For true live template reload, we can implement a custom loader. Let's keep it simple: parse once from disk.
		// If you modify a template, restart the server (Ctrl+C, then go run). Air will automate that later.
		basePath := "internal/router/templates/base.html"
		adminPath := "internal/router/templates/admin.html"
		tmpl = template.Must(template.ParseFiles(basePath))
		adminTmpl = template.Must(template.ParseFiles(adminPath))

		// Static files from disk
		staticFS = http.Dir("internal/router/static")
	} else {
		// ---------- PRODUCTION MODE: use embedded files ----------
		tmpl = template.Must(template.ParseFS(files, "templates/base.html"))
		adminTmpl = template.Must(template.ParseFS(files, "templates/admin.html"))

		embedded, err := fs.Sub(files, "static")
		if err != nil {
			panic(err)
		}
		staticFS = http.FS(embedded)
	}

	h := &handler{
		store:     s,
		tmpl:      tmpl,
		adminTmpl: adminTmpl,
		cfg:       cfg,
		cfgPath:   cfgPath,
	}

	mux := http.NewServeMux()
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(staticFS)))

	// Public Routes
	mux.HandleFunc("/", h.serve)
	mux.HandleFunc("/-/health", h.health)
	mux.HandleFunc("/-/manifest", h.manifest)
	mux.HandleFunc("/-/search-index.json", h.searchIndex)
	mux.HandleFunc("/-/source/", h.source)
	mux.HandleFunc("/-/download/", h.download)
	mux.HandleFunc("/-/webhook/sync", h.webhookSync)
	// NEW: Node information and connections
	mux.HandleFunc("/-/nodeinfo", h.nodeInfo)

	// Admin UI
	mux.HandleFunc("/-/admin", h.adminUI)
	// Contributors management (admin only)
	mux.HandleFunc("/-/admin/contributors", h.adminOnly(h.listContributors))
	mux.HandleFunc("/-/admin/contributors/add", h.adminOnly(h.addContributor))
	mux.HandleFunc("/-/admin/contributors/delete", h.adminOnly(h.deleteContributor))
	mux.HandleFunc("/-/admin/peers", h.adminOnly(h.listPeers))
	mux.HandleFunc("/-/admin/peers/add", h.adminOnly(h.addPeer))
	mux.HandleFunc("/-/admin/peers/delete", h.adminOnly(h.deletePeer))
	// Secure routes
	mux.HandleFunc("/-/reload", h.adminOnly(h.reload))
	mux.HandleFunc("/-/import", h.adminOnly(h.importRemote))
	mux.HandleFunc("/-/admin/config", h.adminOnly(h.adminConfig))
	mux.HandleFunc("/-/admin/remove", h.adminOnly(h.adminRemove))

	return mux
}

// adminOnly is the security middleware that protects endpoints from unauthorized access
func (h *handler) adminOnly(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := r.Header.Get("X-Admin-Token")

		if token == "" || h.cfg.AdminToken == "" {
			http.Error(w, "Unauthorized: Missing Token", http.StatusUnauthorized)
			return
		}

		expectedHash := sha256.Sum256([]byte(h.cfg.AdminToken))
		providedHash := sha256.Sum256([]byte(token))
		if subtle.ConstantTimeCompare(providedHash[:], expectedHash[:]) != 1 {
			http.Error(w, "Unauthorized: Invalid Token", http.StatusUnauthorized)
			return
		}

		next(w, r)
	}
}

func (h *handler) adminUI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.adminTmpl.ExecuteTemplate(w, "admin.html", nil); err != nil {
		http.Error(w, "Admin template execution error: "+err.Error(), 500)
	}
}

func (h *handler) reload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := h.store.Reload(); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
