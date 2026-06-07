package router

import (
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/RED-Collective/red-engine/internal/config"
	"github.com/RED-Collective/red-engine/internal/models"
	"github.com/RED-Collective/red-engine/internal/navigation"
	"github.com/RED-Collective/red-engine/internal/registry"
	"github.com/RED-Collective/red-engine/internal/store"
)

//go:embed templates static
var files embed.FS

type handler struct {
	store       *store.Store
	mainTmpl    *template.Template
	sub1Tmpl    *template.Template
	articleTmpl *template.Template
	adminTmpl   *template.Template
	nodesTmpl   *template.Template
	cfg         *config.Config
	navService  *navigation.Service
	devMode     bool
	// webFS is the active compiled-frontend source served at /, or nil to fall
	// back to the legacy Go templates. See frontend.go.
	webFS http.FileSystem
}

var tmplFuncs = template.FuncMap{
	// sortedSubs returns subsections sorted by name, skipping the internal "root" key.
	"sortedSubs": func(sub map[string]*models.Section) []*models.Section {
		keys := make([]string, 0, len(sub))
		for k := range sub {
			if k != "root" {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		out := make([]*models.Section, 0, len(keys))
		for _, k := range keys {
			out = append(out, sub[k])
		}
		return out
	},
	// humanize converts a raw folder name to a display string.
	"humanize": func(s string) string {
		s = strings.ReplaceAll(s, "-", " ")
		s = strings.ReplaceAll(s, "_", " ")
		if s == "" {
			return s
		}
		return strings.ToUpper(s[:1]) + s[1:]
	},
	// articleCount returns total article count for a section (direct + sub).
	"articleCount": func(sec *models.Section) int {
		count := len(sec.Articles)
		for _, sub := range sec.Sub {
			count += len(sub.Articles)
		}
		return count
	},
}

func New(s *store.Store, cfg *config.Config) http.Handler {
	var staticFS http.FileSystem

	devMode := os.Getenv("DEV_MODE") == "true"

	parse := func(name string) *template.Template {
		t := template.New("").Funcs(tmplFuncs)
		if devMode {
			return template.Must(t.ParseFiles("internal/router/templates/" + name))
		}
		return template.Must(t.ParseFS(files, "templates/"+name))
	}

	if devMode {
		staticFS = http.Dir("internal/router/static")
	} else {
		embedded, err := fs.Sub(files, "static")
		if err != nil {
			panic(err)
		}
		staticFS = http.FS(embedded)
	}

	h := &handler{
		store:       s,
		mainTmpl:    parse("main.html"),
		sub1Tmpl:    parse("sub1.html"),
		articleTmpl: parse("article.html"),
		adminTmpl:   parse("admin.html"),
		nodesTmpl:   parse("nodes.html"),
		cfg:         cfg,
		devMode:     devMode,
	}

	// Initialise navigation service if registry DB is available.
	if db := registry.GetDB(); db != nil {
		if err := navigation.InitNavSchema(db); err != nil {
			log.Printf("[Navigation] Schema init failed: %v", err)
		} else {
			h.navService = navigation.NewService(db, s.DataDir())
			go func() {
				if _, err := h.navService.ScanDataDirectories(); err != nil {
					log.Printf("[Navigation] Initial scan failed: %v", err)
				}
			}()
		}
	}

	// Resolve the active compiled-frontend source, in priority order:
	//   1. RED_WEB_DIR on the filesystem (drop in any build, no recompile);
	//   2. the binary's embedded static/dist;
	//   3. none — the legacy Go templates render instead.
	// The chosen source must contain an index.html to count as present.
	if cfg.WebDir != "" {
		if _, err := os.Stat(filepath.Join(cfg.WebDir, "index.html")); err == nil {
			h.webFS = http.Dir(cfg.WebDir)
			log.Printf("[Frontend] Serving UI from RED_WEB_DIR=%s", cfg.WebDir)
		} else {
			log.Printf("[Frontend] RED_WEB_DIR=%s has no index.html; ignoring", cfg.WebDir)
		}
	}
	if h.webFS == nil {
		if distFS, err := fs.Sub(files, "static/dist"); err == nil {
			if _, err := fs.Stat(distFS, "index.html"); err == nil {
				h.webFS = http.FS(distFS)
			}
		}
	}

	mux := http.NewServeMux()

	// When a compiled frontend is active it owns every non-API path (including
	// its own /assets, /static, etc.) via the catch-all. Only when there is no
	// frontend do we expose the embedded /static/ assets the legacy templates use.
	if h.webFS == nil {
		mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(staticFS)))
	}

	// Catch-all: serve the built UI (or legacy templates). Explicit client-side
	// routes resolve to the app shell so deep links / refreshes work.
	mux.HandleFunc("/", h.frontend)
	mux.HandleFunc("/-/health", h.health)
	mux.HandleFunc("/-/manifest", h.manifest)
	mux.HandleFunc("/-/search-index.json", h.searchIndex)
	mux.HandleFunc("/-/source/", h.source)
	mux.HandleFunc("/-/download/", h.download)
	mux.HandleFunc("/-/webhook/sync", h.webhookSync)
	mux.HandleFunc("/-/nodeinfo", h.nodeInfo)
	// /-/nodes is a client-side route; serve the app shell so direct visits and
	// refreshes render client-side. The JSON it consumes lives at /-/peers and
	// /-/nodeinfo (registered separately, untouched).
	mux.HandleFunc("/-/nodes", h.appShell)
	mux.HandleFunc("/-/peers", h.publicPeers)
	mux.HandleFunc("/-/announce/challenge", h.announceChallenge)
	mux.HandleFunc("/-/announce/confirm", h.announceConfirm)
	mux.HandleFunc("/-/sync/verify", h.syncVerify)
	mux.HandleFunc("/-/branch-meta/", h.branchMeta)
	mux.HandleFunc("/-/assets/", h.assetFile)
	contentFS := http.StripPrefix("/content/", http.FileServer(http.Dir(h.store.DataDir())))
	mux.Handle("/content/", h.contentWithManifest(contentFS))

	// /-/admin (exact path) is a client-side route; subpaths below remain the
	// real admin JSON API. ServeMux matches /-/admin exactly, so /-/admin/* are
	// unaffected.
	mux.HandleFunc("/-/admin", h.appShell)
	mux.HandleFunc("/-/admin/peers/health", h.adminOnly(h.checkPeerHealthHandler))
	mux.HandleFunc("/-/admin/peers", h.adminOnly(h.listPeers))
	mux.HandleFunc("/-/admin/peers/refresh", h.adminOnly(h.refreshPeer))
	mux.HandleFunc("/-/admin/peers/add", h.adminOnly(h.addPeer))
	mux.HandleFunc("/-/admin/peers/delete", h.adminOnly(h.deletePeer))
	mux.HandleFunc("/-/reload", h.adminOnly(h.reload))
	mux.HandleFunc("/-/import", h.adminOnly(h.importRemote))
	mux.HandleFunc("/-/admin/config", h.adminOnly(h.adminConfig))
	mux.HandleFunc("/-/admin/remove", h.adminOnly(h.adminRemove))
	// Contributor keyring: the recognized signer keys that make a valid signature
	// read as "verified" rather than "unverified".
	mux.HandleFunc("/-/admin/contributors", h.adminOnly(h.listContributors))
	mux.HandleFunc("/-/admin/contributors/add", h.adminOnly(h.addContributorToDB))
	mux.HandleFunc("/-/admin/contributors/delete", h.adminOnly(h.revokeContributor))
	// JSON API consumed by the frontend. GET /api is a self-describing catalog.
	mux.HandleFunc("/api", h.apiIndex)
	mux.HandleFunc("/api/navigation", h.navAPI)
	mux.HandleFunc("/api/tags", h.tagsAPI)
	mux.HandleFunc("/-/admin/navigation/rescan", h.adminOnly(h.navRescan))
	mux.HandleFunc("/-/admin/navigation/folder/description", h.adminOnly(h.navFolderDescription))

	mux.HandleFunc("/api/content", h.contentAPI)
	mux.HandleFunc("/api/recent-files", h.recentFiles)
	mux.HandleFunc("/-/admin/verify", h.adminOnly(h.adminVerify))

	// CORS wrapping lets a frontend on another origin (e.g. a Vite dev server)
	// call the API when RED_CORS_ORIGINS is set; otherwise it is a no-op.
	return corsMiddleware(cfg.CORSOrigins, mux)
}

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
	d := models.PageData{DevMode: h.devMode}
	if err := h.adminTmpl.ExecuteTemplate(w, "admin.html", d); err != nil {
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

// branchMeta serves .meta/ assets for hub card design.
// URL: /-/branch-meta/{path}/cover.jpg  or  /-/branch-meta/{path}/icon.svg
// Maps to: data/{path}/.meta/{file}
func (h *handler) branchMeta(w http.ResponseWriter, r *http.Request) {
	suffix := strings.TrimPrefix(r.URL.Path, "/-/branch-meta/")
	if strings.Contains(suffix, "..") {
		http.NotFound(w, r)
		return
	}
	parts := strings.Split(suffix, "/")
	if len(parts) < 2 {
		http.NotFound(w, r)
		return
	}
	fileName := parts[len(parts)-1]
	if fileName != "cover.jpg" && fileName != "icon.svg" {
		http.NotFound(w, r)
		return
	}
	dirParts := parts[:len(parts)-1]
	for _, p := range dirParts {
		if strings.HasPrefix(p, ".") || p == "" {
			http.NotFound(w, r)
			return
		}
	}
	filePath := filepath.Join(append([]string{h.store.DataDir()}, append(dirParts, ".meta", fileName)...)...)
	http.ServeFile(w, r, filePath)
}

func (h *handler) adminVerify(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Write([]byte(`{"status":"ok"}`))
}

// assetFile serves inline images embedded in markdown articles.
// URL: /-/assets/{dirPath}/{filename}
// Maps to: data/{dirPath}/.assets/{filename}
func (h *handler) assetFile(w http.ResponseWriter, r *http.Request) {
	suffix := strings.TrimPrefix(r.URL.Path, "/-/assets/")
	if strings.Contains(suffix, "..") {
		http.NotFound(w, r)
		return
	}
	parts := strings.Split(suffix, "/")
	if len(parts) < 2 {
		http.NotFound(w, r)
		return
	}
	fileName := parts[len(parts)-1]
	dirParts := parts[:len(parts)-1]
	for _, p := range parts {
		if strings.HasPrefix(p, ".") || p == "" {
			http.NotFound(w, r)
			return
		}
	}
	filePath := filepath.Join(append([]string{h.store.DataDir()}, append(dirParts, ".assets", fileName)...)...)
	http.ServeFile(w, r, filePath)
}
