package navigation

import "database/sql"

// DBTX is satisfied by both *sql.DB and *sql.Tx, so mutating methods can run
// inside or outside a transaction without code duplication.
type DBTX interface {
	Exec(query string, args ...any) (sql.Result, error)
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
}

// compile-time interface checks
var _ DBTX = (*sql.DB)(nil)
var _ DBTX = (*sql.Tx)(nil)

// NavNode represents a folder in the navigation tree.
type NavNode struct {
	ID             int64     `json:"id"`
	Path           string    `json:"path"`
	DisplayName    string    `json:"display_name"`
	Description    string    `json:"description,omitempty"`
	DescriptionSrc string    `json:"description_source,omitempty"`
	IsLeaf         bool      `json:"is_leaf"`
	IsGuide        bool      `json:"is_guide,omitempty"`
	ChildCount     int       `json:"child_count,omitempty"`
	GuideCount     int       `json:"guide_count,omitempty"`
	ContentType    string    `json:"content_type,omitempty"`
	Children       []NavNode `json:"children,omitempty"`
}

// TagCount is a tag and how many guides carry it.
type TagCount struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// ScanResult captures statistics from a filesystem scan.
type ScanResult struct {
	FoldersScanned int      `json:"folders_scanned"`
	GuidesIndexed  int      `json:"guides_indexed"`
	Duration       string   `json:"duration"`
	Errors         []string `json:"errors,omitempty"`
	StartTime      string   `json:"start_time"`
	EndTime        string   `json:"end_time"`
}

// Service owns a reference to the shared registry DB and the data directory.
type Service struct {
	db      *sql.DB
	dataDir string
}

// NewService creates a Service backed by db, scanning the given dataDir.
func NewService(db *sql.DB, dataDir string) *Service {
	return &Service{db: db, dataDir: dataDir}
}
