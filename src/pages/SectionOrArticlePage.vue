<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { useRoute } from 'vue-router'
import type { Article, NavNode } from '../types/api'
import { fetchArticle } from '../api/content'
import { fetchSubtree } from '../api/navigation'
import { describe } from '../lib/branding'
import BreadcrumbBar from '../components/BreadcrumbBar.vue'
import VerificationBadge from '../components/VerificationBadge.vue'
import PrevNextNav from '../components/PrevNextNav.vue'
import ArticleSidebar from '../components/ArticleSidebar.vue'
import SectionCard from '../components/SectionCard.vue'
import ArticleListItem from '../components/ArticleListItem.vue'

const route = useRoute()
// Build a DECODED path from the catch-all param. route.path is URL-encoded
// ("Test%20Authentication"); feeding that to encodeURIComponent downstream would
// double-encode and 404. The pathMatch segments are already decoded by the router.
const path = computed(() => {
  const m = route.params.pathMatch
  const segs = Array.isArray(m) ? m : m ? [m] : []
  return '/' + segs.join('/')
})

const article = ref<Article | null>(null)
const dirNode = ref<NavNode | null>(null)
const loading = ref(false)
const notFound = ref(false)

// View mode: directory hub vs. article reader.
const isDirectory = computed(() => article.value?.is_directory || (!article.value && !!dirNode.value))
// Guide nodes (is_guide=true) are article files; the rest are sub-folders.
const subfolders = computed(() => (dirNode.value?.children ?? []).filter((c) => !c.is_guide))
const articles = computed(() => (dirNode.value?.children ?? []).filter((c) => c.is_guide))

async function load(p: string) {
  loading.value = true
  notFound.value = false
  article.value = null
  dirNode.value = null
  try {
    article.value = await fetchArticle(p)
    if (article.value.is_directory) {
      dirNode.value = await safeSubtree(p)
    }
  } catch {
    // No article/RED_KNOWLEDGE — fall back to a navigation-only directory hub.
    const node = await safeSubtree(p)
    if (node) {
      dirNode.value = node
    } else {
      notFound.value = true
    }
  } finally {
    loading.value = false
  }
}

async function safeSubtree(p: string): Promise<NavNode | null> {
  try {
    return await fetchSubtree(p)
  } catch {
    return null
  }
}

const hubTitle = computed(
  () => article.value?.title || dirNode.value?.display_name || 'Section',
)
const hubDescription = computed(() => describe(dirNode.value?.description))

watch(path, (p) => void load(p), { immediate: true })
</script>

<template>
  <div>
    <p v-if="loading" class="text-ink-muted">Loading&hellip;</p>

    <div v-else-if="notFound" class="flex min-h-[60vh] items-center justify-center">
      <div class="w-full max-w-md rounded-xl border border-line bg-white p-10 text-center">
        <h1 class="font-serif text-2xl font-bold text-ink">Not found</h1>
        <p class="mt-2 break-words text-ink-muted">
          Nothing lives at <span class="font-mono">{{ path }}</span>.
        </p>
        <RouterLink to="/" class="mt-4 inline-block text-imperial hover:underline">&larr; Back home</RouterLink>
      </div>
    </div>

    <!-- Directory hub -->
    <div v-else-if="isDirectory" class="space-y-8">
      <BreadcrumbBar v-if="article?.crumb?.length" :crumbs="article.crumb" />
      <header>
        <h1 class="font-serif text-3xl font-bold text-ink">{{ hubTitle }}</h1>
        <p class="mt-1 text-ink-mid">{{ hubDescription }}</p>
      </header>

      <!-- RED_KNOWLEDGE intro content, when present -->
      <div
        v-if="article?.body_html"
        class="prose-red max-w-none"
        v-html="article.body_html"
      ></div>

      <section v-if="subfolders.length">
        <div class="grid grid-cols-1 gap-5 sm:grid-cols-2 lg:grid-cols-3">
          <SectionCard v-for="s in subfolders" :key="s.path" :node="s" />
        </div>
      </section>

      <section v-if="articles.length">
        <h2 class="mb-4 font-serif text-xl font-bold text-ink">Articles</h2>
        <div class="flex flex-col gap-2">
          <ArticleListItem
            v-for="a in articles"
            :key="a.path"
            :title="a.display_name"
            :path="'/' + a.path"
          />
        </div>
      </section>
    </div>

    <!-- Article reader -->
    <div v-else-if="article" class="grid grid-cols-1 gap-8 lg:grid-cols-[240px_minmax(0,1fr)]">
      <ArticleSidebar :current-path="path" />

      <article class="min-w-0">
        <BreadcrumbBar v-if="article.crumb?.length" :crumbs="article.crumb" class="mb-4" />
        <div class="mb-5 flex flex-wrap items-center justify-between gap-3 border-b border-line pb-4">
          <h1 class="font-serif text-3xl font-bold text-ink">{{ article.title }}</h1>
          <VerificationBadge
            :state="article.verification_state"
            :author="article.author"
            :hash="article.hash"
          />
        </div>
        <p v-if="article.author" class="mb-4 text-sm text-ink-muted">by {{ article.author }}</p>

        <div v-if="article.tags?.length" class="mb-6 flex flex-wrap gap-2">
          <RouterLink
            v-for="t in article.tags"
            :key="t"
            :to="`/tags/${encodeURIComponent(t)}`"
            class="inline-flex items-center rounded-full border border-line bg-imperial-soft/40 px-3 py-1 text-xs font-medium text-imperial no-underline transition-colors hover:border-imperial/40 hover:bg-imperial-soft"
          >
            #{{ t }}
          </RouterLink>
        </div>

        <div class="prose-red max-w-none" v-html="article.body_html"></div>

        <PrevNextNav :prev="article.prev_article" :next="article.next_article" />
      </article>
    </div>
  </div>
</template>
