<script setup lang="ts">
import { ref, onMounted } from 'vue'
import type { Contributor, DetectedSigner } from '../../types/api'
import { useAdmin } from '../../composables/useAdmin'
import {
  listContributors,
  listDetectedSigners,
  addContributor,
  revokeContributor,
} from '../../api/admin'
import ContributorTable from '../../components/admin/ContributorTable.vue'
import DetectedSignersTable from '../../components/admin/DetectedSignersTable.vue'

const { token } = useAdmin()
const contributors = ref<Contributor[]>([])
const detected = ref<DetectedSigner[]>([])
const loading = ref(true)
const busyKey = ref('')
const status = ref<string | null>(null)
const errorMsg = ref<string | null>(null)

const name = ref('')
const publicKey = ref('')
const adding = ref(false)

async function refreshList() {
  loading.value = true
  try {
    // Load the keyring and the detected signers together so the "already trusted"
    // flags stay consistent across both tables.
    const [keyring, signers] = await Promise.all([
      listContributors(token.value),
      listDetectedSigners(token.value),
    ])
    contributors.value = keyring
    detected.value = signers
  } catch (e) {
    errorMsg.value = e instanceof Error ? e.message : 'Failed to load contributors'
  } finally {
    loading.value = false
  }
}

onMounted(refreshList)

// One-click: trust a detected signer, pre-filling the admin label from the note's
// self-asserted name. The pubkey is the identity; the name is just a convenience.
async function onAddDetected(signer: DetectedSigner) {
  errorMsg.value = null
  status.value = null
  busyKey.value = signer.public_key
  try {
    await addContributor(token.value, signer.name, signer.public_key)
    status.value = `Added ${signer.name || signer.public_key.slice(0, 16) + '…'} to trusted contributors.`
    await refreshList()
  } catch (e) {
    errorMsg.value = e instanceof Error ? e.message : 'Failed to add contributor'
  } finally {
    busyKey.value = ''
  }
}

async function onAdd() {
  errorMsg.value = null
  status.value = null
  if (publicKey.value.trim().length !== 64) {
    errorMsg.value = 'Public key must be a 64-character hex string.'
    return
  }
  adding.value = true
  try {
    await addContributor(token.value, name.value.trim(), publicKey.value.trim())
    status.value = 'Contributor added.'
    name.value = ''
    publicKey.value = ''
    await refreshList()
  } catch (e) {
    errorMsg.value = e instanceof Error ? e.message : 'Failed to add contributor'
  } finally {
    adding.value = false
  }
}

async function onRevoke(key: string) {
  busyKey.value = key
  try {
    await revokeContributor(token.value, key)
    await refreshList()
  } catch (e) {
    errorMsg.value = e instanceof Error ? e.message : 'Failed to revoke contributor'
  } finally {
    busyKey.value = ''
  }
}
</script>

<template>
  <div class="mx-auto max-w-4xl space-y-6">
    <h1 class="font-serif text-3xl font-bold text-ink">Contributors</h1>

    <form
      class="flex flex-wrap items-end gap-3 rounded-xl border border-line bg-white p-4"
      @submit.prevent="onAdd"
    >
      <div class="min-w-[10rem] flex-1">
        <label class="mb-1 block text-xs font-bold uppercase tracking-wide text-ink-mid">Name</label>
        <input
          v-model="name"
          type="text"
          placeholder="Jane Doe"
          class="w-full rounded-lg border border-line px-3 py-2 text-sm outline-none focus:border-imperial focus:ring-2 focus:ring-imperial/20"
        />
      </div>
      <div class="min-w-[18rem] flex-[2]">
        <label class="mb-1 block text-xs font-bold uppercase tracking-wide text-ink-mid">Public key (64 hex)</label>
        <input
          v-model="publicKey"
          type="text"
          placeholder="ed25519 public key"
          class="w-full rounded-lg border border-line px-3 py-2 font-mono text-sm outline-none focus:border-imperial focus:ring-2 focus:ring-imperial/20"
        />
      </div>
      <button
        type="submit"
        :disabled="adding"
        class="rounded-lg bg-imperial px-4 py-2 text-sm font-semibold text-white transition-colors hover:bg-imperial-dark disabled:opacity-60"
      >{{ adding ? 'Adding…' : 'Add' }}</button>
    </form>

    <p v-if="status" class="rounded-lg border border-accent/30 bg-accent-soft px-4 py-2 text-sm text-accent-deep">{{ status }}</p>
    <p v-if="errorMsg" class="rounded-lg border border-imperial/30 bg-imperial-soft px-4 py-2 text-sm text-imperial-deep">{{ errorMsg }}</p>

    <p v-if="loading" class="text-ink-muted">Loading&hellip;</p>
    <ContributorTable v-else :contributors="contributors" :busy-key="busyKey" @revoke="onRevoke" />

    <section v-if="!loading" class="space-y-3">
      <div>
        <h2 class="font-serif text-xl font-bold text-ink">Detected signers</h2>
        <p class="text-sm text-ink-muted">
          Signer keys found in this node's content. Add one to trust it — no need to copy keys by hand.
          The name is self-asserted by the signer and is only a label; the public key is the identity.
        </p>
      </div>
      <DetectedSignersTable :signers="detected" :busy-key="busyKey" @add="onAddDetected" />
    </section>
  </div>
</template>
