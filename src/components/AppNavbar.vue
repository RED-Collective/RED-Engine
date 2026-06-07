<script setup lang="ts">
const emit = defineEmits<{ openSearch: [] }>()
</script>

<template>
  <header class="sticky top-0 z-40 border-b border-imperial-deep bg-imperial-deep text-white shadow-md">
    <div class="mx-auto flex h-16 max-w-6xl items-center gap-4 px-4 sm:px-6">
      <RouterLink
        to="/"
        class="flex shrink-0 items-center gap-2 whitespace-nowrap font-serif text-xl font-bold tracking-wide text-white no-underline"
      >
        <span class="flex h-7 w-7 items-center justify-center rounded bg-white/15 text-sm">R</span>
        RED Engine
      </RouterLink>

      <button
        type="button"
        aria-label="Search"
        class="ml-auto flex items-center gap-2 rounded-lg border border-white/25 bg-white/10 px-3 py-1.5 text-sm text-white/80 transition-colors hover:bg-white/20"
        @click="emit('openSearch')"
      >
        <span aria-hidden="true">&#128269;</span>
        <span class="hidden sm:inline">Search</span>
        <kbd class="hidden rounded border border-white/30 px-1.5 text-[0.65rem] sm:inline">&#8984;K</kbd>
      </button>

      <nav class="flex items-center gap-1 text-sm font-medium">
        <RouterLink
          to="/"
          class="rounded px-3 py-1.5 text-white/85 no-underline transition-colors hover:bg-white/15 hover:text-white"
          exact-active-class="bg-white/15 text-white"
        >Home</RouterLink>
        <RouterLink
          to="/tags"
          class="rounded px-3 py-1.5 text-white/85 no-underline transition-colors hover:bg-white/15 hover:text-white"
          active-class="bg-white/15 text-white"
        >Tags</RouterLink>
        <RouterLink
          to="/-/nodes"
          class="rounded px-3 py-1.5 text-white/85 no-underline transition-colors hover:bg-white/15 hover:text-white"
          active-class="bg-white/15 text-white"
        >Nodes</RouterLink>
        <RouterLink
          to="/-/admin"
          class="rounded px-3 py-1.5 text-white/85 no-underline transition-colors hover:bg-white/15 hover:text-white"
        >Admin</RouterLink>
      </nav>
    </div>
  </header>
</template>
