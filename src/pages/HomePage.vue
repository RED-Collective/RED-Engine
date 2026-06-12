<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import type { NavNode, RecentFile, TagCount } from '../types/api'
import { fetchTopLevel } from '../api/navigation'
import { fetchRecentFiles } from '../api/content'
import { fetchNodeInfo, fetchPublicPeers } from '../api/node'
import { fetchTags } from '../api/tags'
import { describe } from '../lib/branding'
import SectionCard from '../components/SectionCard.vue'
import ArticleListItem from '../components/ArticleListItem.vue'
import NodeCard from '../components/NodeCard.vue'
import type { NodeInfo, Peer } from '../types/api'

const sections = ref<NavNode[]>([])
const recent = ref<RecentFile[]>([])
const nodeInfo = ref<NodeInfo | null>(null)
const peers = ref<Peer[]>([])
const tags = ref<TagCount[]>([])
const loading = ref(true)
const error = ref<string | null>(null)

const onlinePeers = computed(() => peers.value.filter((p) => p.is_online))

onMounted(async () => {
  try {
    const [secs, files, info, peerList, tagList] = await Promise.allSettled([
      fetchTopLevel(),
      fetchRecentFiles(5),
      fetchNodeInfo(),
      fetchPublicPeers(),
      fetchTags(),
    ])
    if (secs.status === 'fulfilled') {
      sections.value = secs.value.filter((n) => !n.path.includes('/'))
    }
    if (files.status === 'fulfilled') recent.value = files.value
    if (info.status === 'fulfilled') nodeInfo.value = info.value
    if (peerList.status === 'fulfilled') peers.value = peerList.value
    if (tagList.status === 'fulfilled') tags.value = tagList.value.slice(0, 20)
    if (secs.status === 'rejected') error.value = 'Failed to load navigation.'
  } finally {
    loading.value = false
  }
})
</script>

<template>
  <div class="space-y-10">
    <!-- Hero -->
    <section
      class="rounded-2xl bg-linear-to-br from-imperial-deep via-imperial to-accent-deep p-8 text-white shadow-md sm:p-10"
    >
      <p class="text-xs font-semibold uppercase tracking-[0.2em] text-white/70">RED Engine</p>
      <h1 class="mt-1 font-serif text-4xl font-bold">
        {{ nodeInfo?.name || 'RED Engine' }}
      </h1>
      <p class="mt-2 max-w-2xl text-lg text-white/85">
        {{ describe(nodeInfo?.description) }}
      </p>
    </section>

    <p v-if="loading" class="text-ink-muted">Loading&hellip;</p>
    <p v-else-if="error" class="text-imperial">{{ error }}</p>

    <!-- Collections -->
    <section>
      <h2 class="mb-4 font-serif text-2xl font-bold text-ink">Collections</h2>
      <div v-if="sections.length" class="grid grid-cols-1 gap-5 sm:grid-cols-2 lg:grid-cols-3">
        <SectionCard v-for="s in sections" :key="s.path" :node="s" />
      </div>
      <p v-else-if="!loading" class="rounded-xl border border-line bg-white p-6 text-center text-sm text-ink-muted">
        No collections yet. Add content to your <span class="font-mono">data/</span> directory to get started.
      </p>
    </section>

    <!-- Nodes -->
    <section>
      <div class="mb-4 flex items-baseline justify-between">
        <h2 class="font-serif text-2xl font-bold text-ink">
          Nodes
          <span v-if="peers.length" class="ml-2 text-base font-normal text-ink-muted">
            {{ onlinePeers.length }}/{{ peers.length }} online
          </span>
        </h2>
        <RouterLink to="/-/nodes" class="text-sm text-imperial hover:underline">View all &rarr;</RouterLink>
      </div>
      <div v-if="peers.length" class="grid grid-cols-1 gap-4 md:grid-cols-2">
        <NodeCard v-for="peer in peers" :key="peer.url" :peer="peer" />
      </div>
      <p v-else-if="!loading" class="rounded-xl border border-line bg-white p-6 text-center text-sm text-ink-muted">
        No peers connected yet.
      </p>
    </section>

    <!-- Tags -->
    <section v-if="tags.length">
      <div class="mb-4 flex items-baseline justify-between">
        <h2 class="font-serif text-2xl font-bold text-ink">Tags</h2>
        <RouterLink to="/tags" class="text-sm text-imperial hover:underline">Browse all &rarr;</RouterLink>
      </div>
      <div class="flex flex-wrap gap-2">
        <RouterLink
          v-for="t in tags"
          :key="t.name"
          :to="`/tags/${encodeURIComponent(t.name)}`"
          class="inline-flex items-center gap-1 rounded-full border border-line bg-white px-3 py-1 text-sm text-ink no-underline transition-colors hover:border-imperial/40 hover:bg-imperial-soft/40 hover:text-imperial"
        >
          <span class="text-imperial/70">#</span>{{ t.name }}
          <span class="text-xs text-ink-muted">{{ t.count }}</span>
        </RouterLink>
      </div>
    </section>

    <!-- Recently added -->
    <section v-if="recent.length">
      <h2 class="mb-4 font-serif text-2xl font-bold text-ink">Recently Added</h2>
      <div class="flex flex-col gap-2">
        <ArticleListItem
          v-for="f in recent"
          :key="f.path"
          :title="f.title"
          :path="f.path"
          :author="f.author"
          :state="f.verification_state"
        />
      </div>
    </section>
  </div>
</template>
