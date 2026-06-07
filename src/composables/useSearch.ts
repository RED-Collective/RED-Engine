import { ref, computed } from 'vue'
import type { SearchEntry } from '../types/api'
import { fetchSearchIndex } from '../api/search'

// Lazy-loaded full-text-ish search over the title/path index. The index is
// loaded once on first use and cached at module scope.
const index = ref<SearchEntry[]>([])
let loaded = false
let loadingPromise: Promise<void> | null = null

async function ensureIndex(): Promise<void> {
  if (loaded) return
  if (!loadingPromise) {
    loadingPromise = fetchSearchIndex()
      .then((entries) => {
        index.value = entries
        loaded = true
      })
      .catch(() => {
        index.value = []
      })
      .finally(() => {
        loadingPromise = null
      })
  }
  return loadingPromise
}

export function useSearch() {
  const query = ref('')

  const results = computed<SearchEntry[]>(() => {
    const q = query.value.trim().toLowerCase()
    if (!q) return []
    return index.value
      .filter(
        (e) =>
          e.title.toLowerCase().includes(q) ||
          e.path.toLowerCase().includes(q) ||
          (e.tags ?? []).some((t) => t.toLowerCase().includes(q)),
      )
      .slice(0, 20)
  })

  return { index, query, results, ensureIndex }
}
