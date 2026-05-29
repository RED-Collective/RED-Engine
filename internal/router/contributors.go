package router

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"

	"github.com/RED-Collective/red-engine/internal/models"
)

const contributorsFile = "contributors.json"

// listContributors returns the current contributors list
func (h *handler) listContributors(w http.ResponseWriter, r *http.Request) {
	contributors, err := loadContributors()
	if err != nil && !os.IsNotExist(err) {
		http.Error(w, "Failed to load contributors", http.StatusInternalServerError)
		return
	}
	if contributors == nil {
		contributors = []models.Contributor{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(contributors)
}

// addContributor adds a new trusted contributor
func (h *handler) addContributor(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name      string `json:"name"`
		PublicKey string `json:"public_key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	if req.Name == "" || req.PublicKey == "" {
		http.Error(w, "Name and public_key are required", http.StatusBadRequest)
		return
	}
	// Basic validation: public key should be hex string of length 64 (Ed25519 key is 32 bytes = 64 hex chars)
	if len(req.PublicKey) != 64 {
		http.Error(w, "Public key must be a 64-character hex string", http.StatusBadRequest)
		return
	}

	contributors, err := loadContributors()
	if err != nil && !os.IsNotExist(err) {
		http.Error(w, "Failed to load contributors", http.StatusInternalServerError)
		return
	}
	if contributors == nil {
		contributors = []models.Contributor{}
	}

	// Check for duplicate public key (case-insensitive)
	for _, c := range contributors {
		if strings.EqualFold(c.PublicKey, req.PublicKey) {
			http.Error(w, "Public key already exists", http.StatusConflict)
			return
		}
	}

	contributors = append(contributors, models.Contributor{
		Name:      req.Name,
		PublicKey: req.PublicKey,
	})

	if err := saveContributors(contributors); err != nil {
		http.Error(w, "Failed to save contributors", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(req)
}

// deleteContributor removes a contributor by public key
func (h *handler) deleteContributor(w http.ResponseWriter, r *http.Request) {
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

	contributors, err := loadContributors()
	if err != nil {
		http.Error(w, "Failed to load contributors", http.StatusInternalServerError)
		return
	}

	newList := []models.Contributor{}
	found := false
	for _, c := range contributors {
		if strings.EqualFold(c.PublicKey, req.PublicKey) {
			found = true
			continue
		}
		newList = append(newList, c)
	}
	if !found {
		http.Error(w, "Public key not found", http.StatusNotFound)
		return
	}

	if err := saveContributors(newList); err != nil {
		http.Error(w, "Failed to save contributors", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("deleted"))
}

// Helper functions
func loadContributors() ([]models.Contributor, error) {
	data, err := os.ReadFile(contributorsFile)
	if err != nil {
		return nil, err
	}
	var contributors []models.Contributor
	if err := json.Unmarshal(data, &contributors); err != nil {
		return nil, err
	}
	return contributors, nil
}

func saveContributors(contributors []models.Contributor) error {
	data, err := json.MarshalIndent(contributors, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(contributorsFile, data, 0644)
}
