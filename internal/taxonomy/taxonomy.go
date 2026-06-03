// Package taxonomy holds the fixed knowledge taxonomy and the flat list of
// manual tags. The data ships embedded at build time from data/*.json so the
// same canonical tree is available to the engine and (separately) the signer.
//
// IDs are permanent and never reused: adding a branch means a new ID, renaming
// keeps the ID and changes the name string only.
package taxonomy

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

//go:embed data/library-tree.json
var libraryTreeJSON []byte

//go:embed data/manual-tags.json
var manualTagsJSON []byte

// Node is one entry in the library taxonomy tree.
type Node struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	Slug     string `json:"slug"`
	Children []Node `json:"children"`
}

// Tag is one entry in the flat manual-tag list.
type Tag struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type treeFile struct {
	Version string `json:"version"`
	Tree    []Node `json:"tree"`
}

type tagsFile struct {
	Version string `json:"version"`
	Tags    []Tag  `json:"tags"`
}

// Tree returns the parsed library taxonomy (top-level nodes with children).
func Tree() ([]Node, error) {
	var f treeFile
	if err := json.Unmarshal(libraryTreeJSON, &f); err != nil {
		return nil, err
	}
	return f.Tree, nil
}

// Tags returns the parsed flat list of manual tags.
func Tags() ([]Tag, error) {
	var f tagsFile
	if err := json.Unmarshal(manualTagsJSON, &f); err != nil {
		return nil, err
	}
	return f.Tags, nil
}

// CoreUID returns the stable federation UID for a curated taxonomy node. It is
// derived from the permanent integer id, so it never changes even when the node
// is renamed.
func CoreUID(id int) string {
	return fmt.Sprintf("c%d", id)
}

// CommunityUID returns the deterministic UID for a user-created branch. Because
// it is derived from (parent, slug), the same branch created independently on
// two nodes resolves to the same UID and they merge on sync instead of
// duplicating.
func CommunityUID(parentUID, slug string) string {
	sum := sha256.Sum256([]byte(parentUID + "\x1f" + slug))
	return "u" + hex.EncodeToString(sum[:])[:24]
}
