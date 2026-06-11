package router

import (
	"encoding/json"
	"log"
	"net/http"
	"path/filepath"

	"github.com/RED-Collective/red-engine/internal/backup"
)

// backupDirPath is where snapshot zips live: a "backups" subfolder of the node's
// private state dir, kept out of data/ so a snapshot never appears as published
// content (and a restore-by-unzip into data/ never recurses into old backups).
func (h *handler) backupDirPath() string {
	return filepath.Join(h.cfg.ResolvedStateDir(), "backups")
}

// backupCreate makes an on-demand zip snapshot of data/ and prunes old ones.
//
//	POST /-/admin/backup → {name,path,size_bytes,created}
func (h *handler) backupCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	dir := h.backupDirPath()
	info, err := backup.CreateDataBackup(h.store.DataDir(), dir)
	if err != nil {
		http.Error(w, "backup failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := backup.PruneBackups(dir, 10); err != nil {
		log.Printf("backup: prune failed: %v", err)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(info)
}

// backupList returns existing snapshots, newest first.
//
//	GET /-/admin/backups → [{name,path,size_bytes,created}]
func (h *handler) backupList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	list, err := backup.ListBackups(h.backupDirPath())
	if err != nil {
		http.Error(w, "list failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if list == nil {
		list = []backup.BackupInfo{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(list)
}
