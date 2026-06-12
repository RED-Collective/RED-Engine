import type { FtsResult } from '../types/api'
import { getJSON } from './http'

// GET /api/search?q= → FTS5 server-side search, returns up to 20 results.
export function searchFts(q: string): Promise<FtsResult[]> {
  return getJSON<FtsResult[]>(`/api/search?q=${encodeURIComponent(q)}`)
}
