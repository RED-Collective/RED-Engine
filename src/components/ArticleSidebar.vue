<script setup lang="ts">
import { ref, watch, computed } from 'vue'
import type { NavNode } from '../types/api'
import { fetchTopLevel, fetchSubtree } from '../api/navigation'
import { branchRoot, stripLeadingSlash, withLeadingSlash } from '../api/paths'
import SidebarNode from './SidebarNode.vue'

const props = defineProps<{ currentPath: string }>()

const vaults = ref<NavNode[]>([])
const branchTree = ref<NavNode | null>(null)
const loading = ref(false)
const open = ref(false)

const rootPath = computed(() => branchRoot(props.currentPath))
const rootPathBare = computed(() => stripLeadingSlash(rootPath.value))

async function load() {
  loading.value = true
  try {
    const [allNodes, tree] = await Promise.allSettled([
      fetchTopLevel(),
      rootPathBare.value ? fetchSubtree(rootPath.value) : Promise.resolve(null),
    ])
    if (allNodes.status === 'fulfilled') {
      vaults.value = allNodes.value.filter((n) => !n.path.includes('/'))
    }
    branchTree.value = tree.status === 'fulfilled' ? tree.value : null
  } finally {
    loading.value = false
  }
}

watch(rootPath, () => void load(), { immediate: true })

function vaultChildren(vault: NavNode): NavNode[] {
  if (vault.path === rootPathBare.value && branchTree.value) {
    return branchTree.value.children ?? []
  }
  return []
}
</script>

<template>
  <div>
    <!-- Mobile toggle -->
    <button
      type="button"
      class="mb-3 inline-flex items-center gap-2 rounded-md border border-line bg-white px-3 py-1.5 text-sm font-medium text-ink-mid lg:hidden"
      @click="open = !open"
    >
      <span aria-hidden="true">&#9776;</span> {{ open ? 'Hide' : 'Browse' }} files
    </button>

    <aside
      class="rounded-xl border border-line bg-white p-3 lg:sticky lg:top-20 lg:max-h-[calc(100vh-6rem)] lg:overflow-y-auto"
      :class="{ hidden: !open, block: open, 'lg:block': true }"
    >
      <p class="mb-2 px-2 text-xs font-semibold uppercase tracking-widest text-ink-muted">Vaults</p>

      <p v-if="loading" class="px-2 py-1 text-sm text-ink-muted">Loading&hellip;</p>

      <nav v-else class="flex flex-col gap-0.5">
        <template v-for="vault in vaults" :key="vault.path">
          <!-- Vault header link -->
          <RouterLink
            :to="withLeadingSlash(vault.path)"
            class="flex items-center gap-1 rounded px-2 py-1 text-sm font-semibold no-underline transition-colors"
            :class="vault.path === rootPathBare
              ? 'bg-imperial-soft text-imperial'
              : 'text-ink hover:bg-paper-2 hover:text-imperial'"
          >
            {{ vault.display_name }}
          </RouterLink>

          <!-- Current vault's subtree -->
          <div v-if="vault.path === rootPathBare && vaultChildren(vault).length" class="ml-1 mt-0.5 border-l border-line pl-1">
            <SidebarNode
              v-for="child in vaultChildren(vault)"
              :key="child.path"
              :node="child"
              :current-path="currentPath"
              :depth="0"
            />
          </div>
        </template>
      </nav>
    </aside>
  </div>
</template>
