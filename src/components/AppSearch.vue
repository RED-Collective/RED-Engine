<script setup lang="ts">
import { ref, watch, nextTick } from 'vue'
import { useRouter } from 'vue-router'
import { useSearch } from '../composables/useSearch'
import { withLeadingSlash } from '../api/paths'

const props = defineProps<{ open: boolean }>()
const emit = defineEmits<{ close: [] }>()

const router = useRouter()
const { query, results, ensureIndex } = useSearch()
const inputEl = ref<HTMLInputElement | null>(null)
const activeIndex = ref(0)

watch(
  () => props.open,
  async (isOpen) => {
    if (isOpen) {
      void ensureIndex()
      query.value = ''
      activeIndex.value = 0
      await nextTick()
      inputEl.value?.focus()
    }
  },
)

watch(results, () => {
  activeIndex.value = 0
})

function go(filePath: string) {
  emit('close')
  router.push(withLeadingSlash(filePath))
}

function onKeydown(e: KeyboardEvent) {
  if (e.key === 'Escape') {
    emit('close')
  } else if (e.key === 'ArrowDown') {
    e.preventDefault()
    activeIndex.value = Math.min(activeIndex.value + 1, results.value.length - 1)
  } else if (e.key === 'ArrowUp') {
    e.preventDefault()
    activeIndex.value = Math.max(activeIndex.value - 1, 0)
  } else if (e.key === 'Enter') {
    const hit = results.value[activeIndex.value]
    if (hit) go(hit.file_path)
  }
}
</script>

<template>
  <Teleport to="body">
    <div
      v-if="open"
      class="fixed inset-0 z-50 flex items-start justify-center bg-black/40 p-4 pt-[12vh]"
      @click.self="emit('close')"
    >
      <div class="w-full max-w-xl overflow-hidden rounded-xl border border-line bg-white shadow-2xl">
        <div class="flex items-center gap-2 border-b border-line px-4">
          <span class="text-ink-muted" aria-hidden="true">&#128269;</span>
          <input
            ref="inputEl"
            v-model="query"
            type="text"
            placeholder="Search articles&hellip;"
            class="w-full bg-transparent py-3.5 text-base outline-none placeholder:text-ink-muted"
            @keydown="onKeydown"
          />
          <kbd class="rounded border border-line bg-paper-2 px-1.5 py-0.5 text-[0.65rem] text-ink-muted">ESC</kbd>
        </div>

        <ul v-if="results.length" class="max-h-80 overflow-y-auto py-2">
          <li v-for="(hit, i) in results" :key="hit.file_path">
            <button
              type="button"
              class="flex w-full flex-col items-start gap-0.5 px-4 py-2.5 text-left transition-colors"
              :class="i === activeIndex ? 'bg-imperial-soft' : 'hover:bg-paper-2'"
              @click="go(hit.file_path)"
              @mouseenter="activeIndex = i"
            >
              <span class="font-medium text-ink">{{ hit.title }}</span>
              <span
                v-if="hit.snippet"
                class="line-clamp-1 text-xs text-ink-muted"
                v-html="hit.snippet"
              ></span>
              <span v-else class="font-mono text-xs text-ink-muted">{{ hit.file_path }}</span>
            </button>
          </li>
        </ul>
        <p v-else-if="query.trim()" class="px-4 py-6 text-center text-sm text-ink-muted">
          No results for &ldquo;{{ query }}&rdquo;
        </p>
        <p v-else class="px-4 py-6 text-center text-sm text-ink-muted">
          Type to search the knowledge base.
        </p>
      </div>
    </div>
  </Teleport>
</template>
