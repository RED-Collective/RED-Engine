import { ref, watch } from 'vue'
import type { FtsResult } from '../types/api'
import { searchFts } from '../api/search'

export function useSearch() {
  const query = ref('')
  const results = ref<FtsResult[]>([])
  let debounce: ReturnType<typeof setTimeout> | null = null

  watch(query, (q) => {
    if (debounce) clearTimeout(debounce)
    if (!q.trim()) {
      results.value = []
      return
    }
    debounce = setTimeout(async () => {
      try {
        results.value = await searchFts(q.trim())
      } catch {
        results.value = []
      }
    }, 200)
  })

  // No-op: FTS is server-side, nothing to pre-load.
  function ensureIndex() {
    return Promise.resolve()
  }

  return { query, results, ensureIndex }
}
