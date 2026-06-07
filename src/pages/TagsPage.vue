<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { useRoute } from 'vue-router'
import type { NavNode, TagCount } from '../types/api'
import { fetchTags, fetchNotesByTag } from '../api/tags'
import ArticleListItem from '../components/ArticleListItem.vue'

const route = useRoute()

// When :tag is present we show the notes for that tag; otherwise the tag cloud.
const activeTag = computed(() => {
  const t = route.params.tag
  return Array.isArray(t) ? t[0] : (t ?? '')
})

const tags = ref<TagCount[]>([])
const notes = ref<NavNode[]>([])
const loading = ref(true)
const error = ref<string | null>(null)

// Scale the tag-cloud font size between the least- and most-used tag.
const maxCount = computed(() => tags.value.reduce((m, t) => Math.max(m, t.count), 1))
function sizeFor(count: number): string {
  const min = 0.85
  const max = 1.75
  const ratio = maxCount.value > 1 ? (count - 1) / (maxCount.value - 1) : 0
  return (min + ratio * (max - min)).toFixed(2) + 'rem'
}

async function loadCloud() {
  loading.value = true
  error.value = null
  try {
    tags.value = await fetchTags()
  } catch {
    error.value = 'Failed to load tags.'
  } finally {
    loading.value = false
  }
}

async function loadNotes(tag: string) {
  loading.value = true
  error.value = null
  notes.value = []
  try {
    notes.value = await fetchNotesByTag(tag)
  } catch {
    error.value = 'Failed to load notes for this tag.'
  } finally {
    loading.value = false
  }
}

watch(
  activeTag,
  (tag) => {
    if (tag) void loadNotes(tag)
    else void loadCloud()
  },
  { immediate: true },
)
</script>

<template>
  <div class="space-y-8">
    <!-- Single tag: list its notes -->
    <template v-if="activeTag">
      <header class="flex flex-wrap items-baseline gap-3">
        <RouterLink to="/tags" class="text-sm text-imperial hover:underline">&larr; All tags</RouterLink>
        <h1 class="font-serif text-3xl font-bold text-ink">
          Tagged <span class="text-imperial">#{{ activeTag }}</span>
        </h1>
      </header>

      <p v-if="loading" class="text-ink-muted">Loading&hellip;</p>
      <p v-else-if="error" class="text-imperial">{{ error }}</p>
      <p v-else-if="!notes.length" class="text-ink-muted">No notes carry this tag.</p>

      <div v-else class="flex flex-col gap-2">
        <ArticleListItem
          v-for="n in notes"
          :key="n.path"
          :title="n.display_name"
          :path="'/' + n.path"
        />
      </div>
    </template>

    <!-- Tag cloud -->
    <template v-else>
      <header>
        <h1 class="font-serif text-3xl font-bold text-ink">Tags</h1>
        <p class="mt-1 text-ink-mid">Browse content by tag.</p>
      </header>

      <p v-if="loading" class="text-ink-muted">Loading&hellip;</p>
      <p v-else-if="error" class="text-imperial">{{ error }}</p>
      <p v-else-if="!tags.length" class="text-ink-muted">No tags yet.</p>

      <div v-else class="flex flex-wrap items-center gap-x-4 gap-y-3">
        <RouterLink
          v-for="t in tags"
          :key="t.name"
          :to="`/tags/${encodeURIComponent(t.name)}`"
          class="inline-flex items-baseline gap-1 leading-none text-ink no-underline transition-colors hover:text-imperial"
          :style="{ fontSize: sizeFor(t.count) }"
        >
          <span class="text-imperial/70">#</span>{{ t.name }}
          <span class="text-xs text-ink-muted">{{ t.count }}</span>
        </RouterLink>
      </div>
    </template>
  </div>
</template>
