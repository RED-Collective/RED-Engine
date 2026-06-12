<script setup lang="ts">
import { ref } from 'vue'
import { RouterLink } from 'vue-router'
import type { DetectedSigner } from '../../types/api'

defineProps<{ signers: DetectedSigner[]; busyKey: string }>()
const emit = defineEmits<{ add: [signer: DetectedSigner] }>()

// One-click copy of a detected signer's public key, with brief "Copied!" feedback.
const copiedKey = ref('')
async function copyKey(key: string) {
  try {
    await navigator.clipboard.writeText(key)
    copiedKey.value = key
    setTimeout(() => {
      if (copiedKey.value === key) copiedKey.value = ''
    }, 1500)
  } catch {
    /* clipboard unavailable */
  }
}
</script>

<template>
  <div class="overflow-x-auto rounded-xl border border-line bg-white">
    <table class="w-full text-sm">
      <thead>
        <tr class="border-b border-line bg-paper-2 text-left text-xs uppercase tracking-wide text-ink-muted">
          <th class="px-4 py-3 font-semibold">Name <span class="font-normal normal-case text-ink-muted">(self-asserted)</span></th>
          <th class="px-4 py-3 font-semibold">Public key</th>
          <th class="px-4 py-3 font-semibold">Notes</th>
          <th class="px-4 py-3 font-semibold">Most recent</th>
          <th class="px-4 py-3 text-right font-semibold">Actions</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="s in signers" :key="s.public_key" class="border-b border-line last:border-0">
          <td class="px-4 py-3 font-medium text-ink">
            {{ s.name || '—' }}
          </td>
          <td class="px-4 py-3">
            <div class="flex items-center gap-2">
              <span class="break-all font-mono text-xs text-ink-mid">{{ s.public_key }}</span>
              <button
                type="button"
                class="shrink-0 rounded border border-line px-2 py-0.5 text-xs text-ink-muted transition-colors hover:bg-paper-2"
                :title="copiedKey === s.public_key ? 'Copied!' : 'Copy public key'"
                @click="copyKey(s.public_key)"
              >{{ copiedKey === s.public_key ? '✓ Copied' : '📋 Copy' }}</button>
            </div>
          </td>
          <td class="px-4 py-3 text-ink-mid">{{ s.note_count }}</td>
          <td class="px-4 py-3">
            <template v-if="s.recent_path">
              <RouterLink
                :to="'/' + s.recent_path.replace(/^\/+/, '')"
                class="font-medium text-imperial hover:underline"
              >{{ s.recent_title || s.recent_path }}</RouterLink>
              <div v-if="s.recent_signed_at" class="text-xs text-ink-muted">
                signed {{ s.recent_signed_at }}
              </div>
            </template>
            <span v-else class="text-ink-muted">—</span>
          </td>
          <td class="px-4 py-3 text-right">
            <span
              v-if="s.trusted"
              class="inline-flex items-center gap-1 rounded-md border border-accent/30 bg-accent-soft px-2 py-1 text-xs font-semibold text-accent-dark"
            >&#10003; Trusted</span>
            <button
              v-else
              type="button"
              :disabled="busyKey === s.public_key"
              class="rounded border border-imperial/40 px-2 py-1 text-xs font-semibold text-imperial transition-colors hover:bg-imperial-soft disabled:opacity-50"
              @click="emit('add', s)"
            >{{ busyKey === s.public_key ? 'Adding…' : '+ Add as contributor' }}</button>
          </td>
        </tr>
        <tr v-if="!signers.length">
          <td colspan="5" class="px-4 py-8 text-center text-ink-muted">
            No signed content detected yet. Signers appear here once signed notes are present.
          </td>
        </tr>
      </tbody>
    </table>
  </div>
</template>
