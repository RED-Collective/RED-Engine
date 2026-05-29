// RED ENGINE VERSION

package router

import (
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"html/template"
	"io/fs"
	"net/http"

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

func New(s *store.Store, cfg *config.Config, cfgPath string) http.Handler {
	tmpl := template.Must(template.ParseFS(files, "templates/base.html"))
	adminTmpl := template.Must(template.ParseFS(files, "templates/admin.html"))

	staticFS, err := fs.Sub(files, "static")
	if err != nil {
		panic(err)
	}

	h := &handler{
		store:     s,
		tmpl:      tmpl,
		adminTmpl: adminTmpl,
		cfg:       cfg,
		cfgPath:   cfgPath,
	}

	mux := http.NewServeMux()
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))

	// Public Routes
	mux.HandleFunc("/", h.serve)
	mux.HandleFunc("/-/health", h.health)
	mux.HandleFunc("/-/manifest", h.manifest)
	mux.HandleFunc("/-/search-index.json", h.searchIndex) // <--- ADD THIS LINE
	mux.HandleFunc("/-/source/", h.source)
	mux.HandleFunc("/-/download/", h.download)
	// NEW: Generic Webhook Endpoint (Security deferred)
	mux.HandleFunc("/-/webhook/sync", h.webhookSync)

	// The Admin UI
	mux.HandleFunc("/-/admin", h.adminUI)
	// SECURE ROUTES: Wrapped in the adminOnly middleware
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

		// 1. Check if token is empty
		if token == "" || h.cfg.AdminToken == "" {
			http.Error(w, "Unauthorized: Missing Token", http.StatusUnauthorized)
			return
		}

		// 2. Use ConstantTimeCompare on hashes to prevent length-based timing attacks
		expectedHash := sha256.Sum256([]byte(h.cfg.AdminToken))
		providedHash := sha256.Sum256([]byte(token))
		if subtle.ConstantTimeCompare(providedHash[:], expectedHash[:]) != 1 {
			http.Error(w, "Unauthorized: Invalid Token", http.StatusUnauthorized)
			return
		}

		// 3. Token is valid, proceed to the requested function
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
