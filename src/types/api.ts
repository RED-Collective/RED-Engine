// API response types — these mirror the Go backend JSON shapes exactly.
// Path conventions (important):
//   - NavNode.path       has NO leading slash   (e.g. "physics/mechanics")
//   - Crumb.path         HAS a leading slash     (e.g. "/physics/mechanics")
//   - RecentFile.path    HAS a leading slash
//   - Article prev/next  HAVE a leading slash
//   - /api/content accepts a path with or without a leading slash.

export interface NavNode {
  id: number
  path: string
  display_name: string
  description?: string
  description_source?: string
  is_leaf: boolean
  is_guide?: boolean
  child_count?: number
  guide_count?: number
  content_type?: string
  children?: NavNode[]
}

export interface Crumb {
  label: string
  path: string
}

// The four states the Go backend actually emits (store.processArticle):
//   verified   — valid signature by a key in this node's contributor keyring
//   unverified — valid signature, but the signer key is not (yet) recognized
//   tampered   — signature/body-hash mismatch
//   unsigned   — no signature in the note frontmatter
export type VerificationState =
  | 'verified'
  | 'unverified'
  | 'tampered'
  | 'unsigned'

export interface ArticleRef {
  title: string
  path: string
}

export interface Article {
  title: string
  body_html: string
  verification_state: VerificationState
  verification_error?: string
  signer_key?: string
  author: string
  signed_at?: string // human-readable signature date (red_signed_at)
  hash: string
  crumb: Crumb[]
  prev_article: ArticleRef | null
  next_article: ArticleRef | null
  is_directory: boolean
  tags?: string[]
}

export interface RecentFile {
  title: string
  path: string
  author?: string
  signer_key?: string
  verification_state: VerificationState
}

export interface NodeInfo {
  name: string
  public_key: string
  software_version: string
  exported_paths: string[]
  public_url: string
  tunnel_type: string
  description: string
}

// Public peer entry returned by GET /-/peers.
export interface Peer {
  url: string
  public_key?: string
  name: string
  peer_type: string
  description?: string
  public_url?: string
  tunnel_type?: string
  is_online: boolean
  exported_paths: string[]
  last_seen?: string
}

// Admin peer entry returned by GET /-/admin/peers (registry.Peer).
export interface AdminPeer {
  id: number
  url: string
  public_key: string
  name: string
  peer_type: string
  description: string
  public_url: string
  tunnel_type: string
  is_online: boolean
  online_checked_at?: string | null
  exported_paths: string[]
  last_seen: string
  added_at: string
}

export interface Contributor {
  name: string
  public_key: string
}

// A signer key observed in this node's content. `name` is self-asserted (from the
// note's red_author_name frontmatter) and is only a convenience label; `trusted`
// reflects whether the key is already in the contributor keyring.
export interface DetectedSigner {
  public_key: string
  name: string
  note_count: number
  trusted: boolean
  sample_path: string
  // The signer's most recently authored note (by file mtime), so a maintainer can
  // judge an unverified signer from their latest work. Empty when none could be read.
  recent_path: string
  recent_title: string
  recent_signed_at: string
}

export interface StartupSync {
  id: number
  url: string
  filename: string
  sync_type: string
  last_synced_at: string | null
  last_error: string
  sync_status: string
  added_at: string
}

// GET /-/search-index.json returns store.SearchItem[] — title + path + tags.
// (Admin-only; used only for admin tooling now.)
export interface SearchEntry {
  title: string
  path: string
  tags?: string[]
}

// GET /api/search?q= returns navigation.SearchResult[] — FTS5 results with snippet.
export interface FtsResult {
  file_path: string
  title: string
  snippet: string
}

// GET /api/tags returns navigation.TagCount[] — a tag and how many notes carry it.
export interface TagCount {
  name: string
  count: number
}
