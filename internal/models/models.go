package models

import "html/template"

type Article struct {
	Path              string
	Title             string
	Body              template.HTML
	Raw               string
	Hash              string
	Verified          bool
	Author            string
	VerificationError string
}

type Section struct {
	Name     string
	Articles []*Article
	Sub      map[string]*Section
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
	NodeName          string // <-- ADD THIS LINE
	Nav               map[string]*Section
	Body              template.HTML
	Title             string
	Path              string
	TopCat            string
	Crumb             []Crumb
	Verified          bool
	Author            string
	Hash              string
	VerificationError string
}
