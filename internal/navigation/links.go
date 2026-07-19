package navigation

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/RED-Collective/red-engine/internal/fetch"
)

// pendingLink is one raw wikilink occurrence collected during a scan, before
// resolution: the guide it came from, the target as written (alias/heading
// already stripped), and whether it was an embed (![[…]]) or a plain link.
type pendingLink struct {
	sourceID int64
	target   string
	embed    bool
}

// rebuildLinks regenerates the nav_links table from the wikilink targets
// collected during a scan. It must run inside the scan transaction, after
// pruneStale, so resolution only ever sees guides that still exist on disk.
//
// Target resolution mirrors store.linkResolver: the key is the lowercased
// basename with any .md suffix trimmed, and on duplicate basenames the first
// occurrence in file_path order wins. (The store walks with filepath.WalkDir;
// ORDER BY file_path reproduces that lexical order except when a path component
// contains a character sorting before '/', which is negligible in practice.)
func (s *Service) rebuildLinks(dbtx DBTX, links []pendingLink) error {
	if _, err := dbtx.Exec(`DELETE FROM nav_links`); err != nil {
		return fmt.Errorf("clear nav_links: %w", err)
	}

	// Basename → guide id map from the rows this scan just wrote.
	rows, err := dbtx.Query(`SELECT id, file_name FROM nav_guides ORDER BY file_path`)
	if err != nil {
		return fmt.Errorf("load guide names: %w", err)
	}
	byBase := map[string]int64{}
	for rows.Next() {
		var id int64
		var fileName string
		if err := rows.Scan(&id, &fileName); err != nil {
			rows.Close()
			return fmt.Errorf("scan guide name: %w", err)
		}
		key := strings.ToLower(strings.TrimSuffix(fileName, filepath.Ext(fileName)))
		if _, taken := byBase[key]; !taken {
			byBase[key] = id // first occurrence wins, matching store.buildLinkIndex
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate guide names: %w", err)
	}

	inserted := map[string]bool{}
	for _, l := range links {
		name := strings.TrimSpace(l.target)
		if name == "" || fetch.IsSyncableAsset(name) {
			continue // image embeds/links are assets, not note edges
		}
		key := strings.TrimSuffix(strings.ToLower(filepath.Base(name)), ".md")
		if key == "" {
			continue
		}
		kind := "link"
		if l.embed {
			kind = "embed"
		}
		// Collapse repeats of the same target from the same note ([[Note]] +
		// [[note|alias]]); a link and an embed of one target stay distinct.
		dedup := fmt.Sprintf("%d|%s|%s", l.sourceID, key, kind)
		if inserted[dedup] {
			continue
		}
		inserted[dedup] = true

		var targetID interface{}
		if id, ok := byBase[key]; ok {
			targetID = id
		}
		if _, err := dbtx.Exec(`
			INSERT INTO nav_links (source_id, target_path, target_id, kind)
			VALUES (?, ?, ?, ?)`, l.sourceID, name, targetID, kind); err != nil {
			return fmt.Errorf("insert link %q: %w", name, err)
		}
	}
	return nil
}

// BacklinkRef is one note that links to (or embeds) the requested note.
type BacklinkRef struct {
	FilePath string `json:"file_path"` // clean path, no .md
	Title    string `json:"title"`
	Kind     string `json:"kind"` // "link" | "embed"
}

// Backlinks returns the notes whose wikilinks point at the note at path.
// path is a clean note path (no .md); a leading slash or .md suffix is tolerated.
func (s *Service) Backlinks(path string) ([]BacklinkRef, error) {
	path = strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(path), "/"), ".md")
	if path == "" {
		return []BacklinkRef{}, nil
	}
	// COLLATE NOCASE covers notes saved with a .MD extension, which the scanner
	// indexes; the table is small enough that bypassing the index is irrelevant.
	rows, err := s.db.Query(`
		SELECT src.file_path, COALESCE(src.title,''), l.kind
		FROM nav_links l
		JOIN nav_guides src ON src.id = l.source_id
		JOIN nav_guides tgt ON tgt.id = l.target_id
		WHERE tgt.file_path = ? COLLATE NOCASE
		ORDER BY src.title, src.file_path`, path+".md")
	if err != nil {
		return nil, fmt.Errorf("backlinks query: %w", err)
	}
	defer rows.Close()

	out := []BacklinkRef{}
	for rows.Next() {
		var r BacklinkRef
		if err := rows.Scan(&r.FilePath, &r.Title, &r.Kind); err != nil {
			return nil, fmt.Errorf("scan backlink: %w", err)
		}
		r.FilePath = strings.TrimSuffix(r.FilePath, ".md")
		out = append(out, r)
	}
	return out, rows.Err()
}

// GraphNode is one note in the link graph.
type GraphNode struct {
	ID       int64  `json:"id"`
	FilePath string `json:"file_path"` // clean path, no .md
	Title    string `json:"title"`
	Vault    string `json:"vault,omitempty"` // top-level content_type, for grouping/coloring
}

// GraphEdge is one resolved wikilink between two notes.
type GraphEdge struct {
	SourceID int64  `json:"source_id"`
	TargetID int64  `json:"target_id"`
	Kind     string `json:"kind"` // "link" | "embed"
}

// Graph is the full note link graph: every indexed note plus every resolved
// wikilink edge. Broken links are excluded (their target is not a node); they
// are reported separately by BrokenLinks.
type Graph struct {
	Nodes []GraphNode `json:"nodes"`
	Edges []GraphEdge `json:"edges"`
}

// GraphDump returns the whole link graph in one response.
func (s *Service) GraphDump() (*Graph, error) {
	g := &Graph{Nodes: []GraphNode{}, Edges: []GraphEdge{}}

	rows, err := s.db.Query(`
		SELECT g.id, g.file_path, COALESCE(g.title,''), COALESCE(f.content_type,'')
		FROM nav_guides g
		JOIN nav_folders f ON f.id = g.folder_id
		ORDER BY g.file_path`)
	if err != nil {
		return nil, fmt.Errorf("graph nodes query: %w", err)
	}
	for rows.Next() {
		var n GraphNode
		if err := rows.Scan(&n.ID, &n.FilePath, &n.Title, &n.Vault); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan graph node: %w", err)
		}
		n.FilePath = strings.TrimSuffix(n.FilePath, ".md")
		g.Nodes = append(g.Nodes, n)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	rows, err = s.db.Query(`
		SELECT source_id, target_id, kind FROM nav_links
		WHERE target_id IS NOT NULL
		ORDER BY source_id, target_id`)
	if err != nil {
		return nil, fmt.Errorf("graph edges query: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var e GraphEdge
		if err := rows.Scan(&e.SourceID, &e.TargetID, &e.Kind); err != nil {
			return nil, fmt.Errorf("scan graph edge: %w", err)
		}
		g.Edges = append(g.Edges, e)
	}
	return g, rows.Err()
}

// BrokenLink is one wikilink target that resolves to no local note, with every
// note that references it. These are the vault's gaps — notes that are linked
// but missing (possibly available on a federation peer).
type BrokenLink struct {
	TargetPath string   `json:"target_path"` // first-seen raw spelling
	Count      int      `json:"count"`
	Sources    []string `json:"sources"` // clean file_paths of referencing notes
}

// BrokenLinks returns unresolved wikilink targets grouped case-insensitively,
// biggest gaps first.
func (s *Service) BrokenLinks() ([]BrokenLink, error) {
	rows, err := s.db.Query(`
		SELECT l.target_path, src.file_path
		FROM nav_links l
		JOIN nav_guides src ON src.id = l.source_id
		WHERE l.target_id IS NULL
		ORDER BY LOWER(l.target_path), src.file_path`)
	if err != nil {
		return nil, fmt.Errorf("broken links query: %w", err)
	}
	defer rows.Close()

	groups := map[string]*BrokenLink{}
	var order []string
	for rows.Next() {
		var target, source string
		if err := rows.Scan(&target, &source); err != nil {
			return nil, fmt.Errorf("scan broken link: %w", err)
		}
		key := strings.ToLower(target)
		gl, ok := groups[key]
		if !ok {
			gl = &BrokenLink{TargetPath: target} // keep the first raw spelling
			groups[key] = gl
			order = append(order, key)
		}
		gl.Count++
		gl.Sources = append(gl.Sources, strings.TrimSuffix(source, ".md"))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]BrokenLink, 0, len(order))
	for _, key := range order {
		out = append(out, *groups[key])
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].TargetPath < out[j].TargetPath
	})
	return out, nil
}
