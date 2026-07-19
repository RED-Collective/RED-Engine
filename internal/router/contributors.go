package router

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/RED-Collective/red-engine/internal/models"
	"github.com/RED-Collective/red-engine/internal/registry"
)

// detectedSigner is one signer key observed across this node's content, with the
// self-asserted display name from the note frontmatter and whether the admin has
// already added it to the trusted keyring. It is the data behind the "add this
// contributor" shortcut: the pubkey is identity, the name is only a convenience
// label to pre-fill, and `trusted` reflects the keyring — the source of truth.
type detectedSigner struct {
	PublicKey  string `json:"public_key"`
	Name       string `json:"name"`        // self-asserted (red_author_name); may be empty
	NoteCount  int    `json:"note_count"`  // how many notes this key signed
	Trusted    bool   `json:"trusted"`     // already in the contributor keyring
	SamplePath string `json:"sample_path"` // one note path for context

	// The signer's most recently authored note, so a maintainer can judge an
	// unverified signer from their latest contribution without going to dig for it.
	// "Most recent" is by file modification time (the freshest sync/edit); empty
	// when none of the signer's notes could be stat'd.
	RecentPath     string `json:"recent_path"`
	RecentTitle    string `json:"recent_title"`
	RecentSignedAt string `json:"recent_signed_at"` // red_signed_at of that note (may be empty)
}

// detectedSigners serves GET /-/admin/contributors/detected — every signer key
// seen in this node's content (across local + pulled vaults), so the admin can add
// one to the trusted keyring with a click instead of hand-copying a hex key out of
// the database. Keys already in the keyring are flagged `trusted` so the UI can
// separate "add me" from "already trusted".
func (h *handler) detectedSigners(w http.ResponseWriter, r *http.Request) {
	recognized := registry.RecognizedContributorKeys()

	type agg struct {
		key, name, sample string
		count             int

		recentPath, recentTitle, recentSignedAt string
		recentMtime                             time.Time // zero until a note is stat'd
	}
	seen := map[string]*agg{}
	dataDir := h.store.DataDir()

	var walk func(sec *models.Section)
	walk = func(sec *models.Section) {
		for _, art := range sec.Articles {
			key := strings.ToLower(strings.TrimSpace(art.SignerKey))
			if key == "" {
				continue // unsigned notes have no signer to surface
			}
			a := seen[key]
			if a == nil {
				a = &agg{key: art.SignerKey, sample: art.Path}
				seen[key] = a
			}
			a.count++
			// Prefer the first non-empty self-asserted name we encounter.
			if a.name == "" && art.SignerName != "" {
				a.name = art.SignerName
			}
			// Track this signer's freshest note by file mtime. Same path→file
			// mapping the recent-activity feed uses (api.go): strip a leading '/'
			// and append ".md".
			filePath := filepath.Join(dataDir, strings.TrimPrefix(art.Path, "/")+".md")
			info, statErr := os.Stat(filePath)
			if statErr != nil {
				continue
			}
			if a.recentMtime.IsZero() || info.ModTime().After(a.recentMtime) {
				a.recentMtime = info.ModTime()
				a.recentPath = art.Path
				a.recentTitle = art.Title
				a.recentSignedAt = art.SignedAt
			}
		}
		for _, sub := range sec.Sub {
			walk(sub)
		}
	}
	for _, sec := range h.store.Root() {
		walk(sec)
	}

	out := make([]detectedSigner, 0, len(seen))
	for _, a := range seen {
		out = append(out, detectedSigner{
			PublicKey:      a.key,
			Name:           a.name,
			NoteCount:      a.count,
			Trusted:        recognized[strings.ToLower(a.key)],
			SamplePath:     a.sample,
			RecentPath:     a.recentPath,
			RecentTitle:    a.recentTitle,
			RecentSignedAt: a.recentSignedAt,
		})
	}
	// Untrusted first (the ones the admin likely wants to act on), then by note count.
	sort.Slice(out, func(i, j int) bool {
		if out[i].Trusted != out[j].Trusted {
			return !out[i].Trusted
		}
		return out[i].NoteCount > out[j].NoteCount
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

// listContributors returns this node's recognized contributor keyring (the
// non-revoked signer keys). A signature by one of these keys reads as "verified";
// any other valid signature is "unverified". `name` is an admin-facing label only,
// never shown on the public verification badge.
func (h *handler) listContributors(w http.ResponseWriter, r *http.Request) {
	db := registry.GetDB()
	if db == nil {
		http.Error(w, "Database not initialised", http.StatusInternalServerError)
		return
	}
	rows, err := db.Query(`SELECT public_key, name FROM contributors WHERE revoked = 0 ORDER BY added_at`)
	if err != nil {
		http.Error(w, "Failed to load contributors", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	contributors := []models.Contributor{}
	for rows.Next() {
		var c models.Contributor
		if err := rows.Scan(&c.PublicKey, &c.Name); err != nil {
			http.Error(w, "Failed to scan contributor", http.StatusInternalServerError)
			return
		}
		contributors = append(contributors, c)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(contributors)
}

// addContributorToDB recognizes a signer key on this node (marks it a verified
// contributor). It is an upsert that also clears any prior revocation.
func (h *handler) addContributorToDB(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name      string `json:"name"`
		PublicKey string `json:"public_key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	if req.PublicKey == "" {
		http.Error(w, "public_key is required", http.StatusBadRequest)
		return
	}
	if len(req.PublicKey) != 64 {
		http.Error(w, "Public key must be a 64-character hex string", http.StatusBadRequest)
		return
	}

	db := registry.GetDB()
	if db == nil {
		http.Error(w, "Database not initialised", http.StatusInternalServerError)
		return
	}
	if _, err := db.Exec(`
		INSERT INTO contributors (public_key, name, revoked, revoked_at)
		VALUES (?, ?, 0, NULL)
		ON CONFLICT(public_key) DO UPDATE SET
			name       = excluded.name,
			revoked    = 0,
			revoked_at = NULL
	`, req.PublicKey, req.Name); err != nil {
		http.Error(w, "Failed to save contributor: "+err.Error(), http.StatusInternalServerError)
		return
	}
	// Re-verify already-loaded notes against the updated keyring so notes signed by
	// this key flip to "verified" immediately — no restart or file touch needed.
	h.store.RevalidateTrust()
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(req)
}

// revokeContributor stops recognizing a signer key (soft delete). Content signed
// by it then reads as "unverified" instead of "verified".
func (h *handler) revokeContributor(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PublicKey string `json:"public_key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	if req.PublicKey == "" {
		http.Error(w, "public_key is required", http.StatusBadRequest)
		return
	}

	db := registry.GetDB()
	if db == nil {
		http.Error(w, "Database not initialised", http.StatusInternalServerError)
		return
	}
	result, err := db.Exec(`
		UPDATE contributors SET revoked = 1, revoked_at = CURRENT_TIMESTAMP
		WHERE public_key = ? AND revoked = 0
	`, req.PublicKey)
	if err != nil {
		http.Error(w, "Failed to revoke contributor: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		http.Error(w, "Public key not found or already revoked", http.StatusNotFound)
		return
	}
	// Re-verify already-loaded notes so anything signed by the revoked key drops
	// back to "unverified" immediately.
	h.store.RevalidateTrust()
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("revoked"))
}
