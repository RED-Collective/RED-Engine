<script setup lang="ts">
import { ref, computed } from 'vue'
import type { VerificationState } from '../types/api'

const props = defineProps<{
  state: VerificationState
  author?: string
  hash?: string
}>()

const panelOpen = ref(false)

const isVerified = computed(() => props.state === 'verified')
// `unsigned` is a soft/gray warning; everything else is a serious red warning.
const isSerious = computed(() => !isVerified.value && props.state !== 'unsigned')

const MESSAGES: Record<VerificationState, string> = {
  verified: 'This article was cryptographically signed by a trusted contributor.',
  unverified:
    'This article has a valid signature, but its signer is not in this node’s trusted contributors list.',
  tampered:
    'This article’s content was modified after it was signed. The signature no longer matches.',
  unsigned:
    'This article has no cryptographic signature. Its authenticity cannot be confirmed.',
}

const TITLES: Record<VerificationState, string> = {
  verified: 'Verified',
  unverified: 'Unverified',
  tampered: 'Tampered',
  unsigned: 'Unsigned',
}

const detailMessage = computed(() => MESSAGES[props.state])
const detailTitle = computed(() => TITLES[props.state])
</script>

<template>
  <div class="relative inline-block">
    <!-- Stage 1: summary -->
    <span
      v-if="isVerified"
      class="inline-flex items-center gap-1 rounded-md border border-accent/30 bg-accent-soft px-2.5 py-1 text-xs font-semibold text-accent-dark"
    >
      <span aria-hidden="true">&#10003;</span> Verified
    </span>
    <button
      v-else
      type="button"
      @click="panelOpen = true"
      class="inline-flex items-center gap-1 rounded-md border border-amber-400 bg-amber-100 px-2.5 py-1 text-xs font-semibold text-amber-800 transition-colors hover:bg-amber-200"
    >
      <span aria-hidden="true">&#9888;</span> Unverified
    </button>

    <!-- Stage 2: detail panel -->
    <div
      v-if="panelOpen && !isVerified"
      class="absolute left-0 z-20 mt-2 w-80 rounded-lg border p-4 text-sm shadow-lg"
      :class="
        isSerious
          ? 'border-imperial bg-imperial-soft text-imperial-deep'
          : 'border-gray-300 bg-gray-100 text-gray-700'
      "
      role="alert"
    >
      <button
        type="button"
        @click="panelOpen = false"
        aria-label="Dismiss"
        class="absolute right-2 top-2 flex h-6 w-6 items-center justify-center rounded-full text-lg leading-none opacity-60 transition-opacity hover:opacity-100"
      >
        &times;
      </button>
      <p class="pr-6 font-bold uppercase tracking-wide" :class="isSerious ? 'text-imperial' : 'text-gray-600'">
        {{ detailTitle }}
      </p>
      <p class="mt-1.5 leading-relaxed">{{ detailMessage }}</p>
      <p v-if="author" class="mt-2 text-xs opacity-70">
        Author: <span class="font-mono">{{ author }}</span>
      </p>
      <p v-if="hash" class="mt-1 break-all font-mono text-[0.7rem] opacity-60">
        {{ hash }}
      </p>
    </div>
  </div>
</template>
