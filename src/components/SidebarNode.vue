<script setup lang="ts">
import { computed } from 'vue'
import type { NavNode } from '../types/api'
import { withLeadingSlash } from '../api/paths'

const props = defineProps<{
  node: NavNode
  currentPath: string
  depth: number
}>()

const to = computed(() => withLeadingSlash(props.node.path))
const isCurrent = computed(() => withLeadingSlash(props.currentPath) === to.value)
// A folder is auto-expanded when the current article lives somewhere inside it.
const isAncestor = computed(() => {
  const cur = withLeadingSlash(props.currentPath)
  return cur === to.value || cur.startsWith(to.value + '/')
})
const children = computed(() => props.node.children ?? [])
const indent = computed(() => ({ paddingLeft: `${props.depth * 0.75}rem` }))
</script>

<template>
  <!-- Folder node -->
  <details v-if="!node.is_guide" :open="isAncestor" class="select-none">
    <summary
      class="flex cursor-pointer items-center gap-1 rounded px-2 py-1 text-sm text-ink-mid transition-colors hover:bg-paper-2 hover:text-imperial"
      :style="indent"
    >
      <span class="text-xs text-ink-muted transition-transform">&#9656;</span>
      <span class="truncate font-medium">{{ node.display_name }}</span>
    </summary>
    <div>
      <SidebarNode
        v-for="child in children"
        :key="child.path"
        :node="child"
        :current-path="currentPath"
        :depth="depth + 1"
      />
    </div>
  </details>

  <!-- Article / guide node -->
  <RouterLink
    v-else
    :to="to"
    class="block truncate rounded px-2 py-1 text-sm no-underline transition-colors"
    :class="isCurrent
      ? 'bg-imperial-soft font-semibold text-imperial'
      : 'text-ink-mid hover:bg-paper-2 hover:text-imperial'"
    :style="indent"
  >
    {{ node.display_name }}
  </RouterLink>
</template>

<style scoped>
/* Rotate the disclosure arrow when the folder is open. */
details[open] > summary > span:first-child {
  transform: rotate(90deg);
}
</style>
