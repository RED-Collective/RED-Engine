<script setup lang="ts">
import { ref } from 'vue'
import type { Contributor } from '../../types/api'

defineProps<{ contributors: Contributor[]; busyKey: string }>()
const emit = defineEmits<{ revoke: [publicKey: string] }>()

// One-click copy of a contributor's public key, with brief "Copied!" feedback —
// so an admin never has to hand-select a 64-char hex string out of the database.
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
          <th class="px-4 py-3 font-semibold">Name</th>
          <th class="px-4 py-3 font-semibold">Public key</th>
          <th class="px-4 py-3 text-right font-semibold">Actions</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="c in contributors" :key="c.public_key" class="border-b border-line last:border-0">
          <td class="px-4 py-3 font-medium text-ink">{{ c.name }}</td>
          <td class="px-4 py-3">
            <div class="flex items-center gap-2">
              <span class="break-all font-mono text-xs text-ink-mid">{{ c.public_key }}</span>
              <button
                type="button"
                class="shrink-0 rounded border border-line px-2 py-0.5 text-xs text-ink-muted transition-colors hover:bg-paper-2"
                :title="copiedKey === c.public_key ? 'Copied!' : 'Copy public key'"
                @click="copyKey(c.public_key)"
              >{{ copiedKey === c.public_key ? '✓ Copied' : '📋 Copy' }}</button>
            </div>
          </td>
          <td class="px-4 py-3 text-right">
            <button
              type="button"
              :disabled="busyKey === c.public_key"
              class="rounded border border-imperial/40 px-2 py-1 text-xs text-imperial transition-colors hover:bg-imperial-soft disabled:opacity-50"
              @click="emit('revoke', c.public_key)"
            >Revoke</button>
          </td>
        </tr>
        <tr v-if="!contributors.length">
          <td colspan="3" class="px-4 py-8 text-center text-ink-muted">No trusted contributors yet.</td>
        </tr>
      </tbody>
    </table>
  </div>
</template>
