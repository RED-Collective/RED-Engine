package models

import "html/template"

type Article struct {
	Path              string
	Title             string
	Body              template.HTML
	Raw               string
	Hash              string
	Verified          bool
	SignerKey         string // hex ed25519 pubkey of the signer (when present); no identity, no trust
	VerificationError string
	VerificationState string   // "verified","unverified","tampered","unsigned"
	Tags              []string // user-defined tags from the note's red_tags frontmatter
}

type Section struct {
	Name     string
	Path     string // URL path for this section (e.g. "/databases/sql")
	Articles []*Article
	Sub      map[string]*Section
	HasCover bool `json:"has_cover"` // .meta/cover.jpg exists
	HasIcon  bool `json:"has_icon"`  // .meta/icon.svg exists
}

type Contributor struct {
	Name      string `json:"name"`
	PublicKey string `json:"public_key"`
}

type ManifestEntry struct {
	FileHash  string `json:"file_hash"`
	Hash      string `json:"hash"`
	PublicKey string `json:"public_key"`
	Signature string `json:"signature"`
}

type Manifest struct {
	Files map[string]ManifestEntry `json:"files"`
}

type Crumb struct {
	Label string
	Path  string
}

type PageData struct {
	Site              string
	NodeName          string
	Nav               map[string]*Section
	Body              template.HTML
	Title             string
	Path              string
	TopCat            string
	Crumb             []Crumb
	Verified          bool
	SignerKey         string
	Hash              string
	VerificationError string
	VerificationState string
	Depth             int      // number of path segments
	Section           *Section // filled for hub pages
	PrevArticle       *Article // previous sibling in same section
	NextArticle       *Article // next sibling in same section
	DevMode           bool
}
