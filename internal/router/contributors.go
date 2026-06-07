package router

import (
	"encoding/json"
	"net/http"

	"github.com/RED-Collective/red-engine/internal/models"
	"github.com/RED-Collective/red-engine/internal/registry"
)

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
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("revoked"))
}
