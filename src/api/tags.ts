import type { NavNode, TagCount } from '../types/api'
import { getJSON } from './http'

// GET /api/tags → [{name, count}] for every tag, by descending count.
export async function fetchTags(): Promise<TagCount[]> {
  return (await getJSON<TagCount[] | null>('/api/tags')) ?? []
}

// GET /api/tags?tag=<t> → the guide nodes carrying tag <t>. Each node has
// is_guide=true and a slash-less path with the ".md" stripped.
export async function fetchNotesByTag(tag: string): Promise<NavNode[]> {
  const q = encodeURIComponent(tag)
  return (await getJSON<NavNode[] | null>(`/api/tags?tag=${q}`)) ?? []
}
