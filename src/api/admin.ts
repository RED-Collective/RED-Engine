import type { AdminPeer, Contributor, DetectedSigner, StartupSync } from '../types/api'
import { getJSON, send, getText, adminHeaders } from './http'

// --- Auth ---------------------------------------------------------------

// GET /-/admin/verify (adminOnly) → 200 {"status":"ok"} when the token is valid.
export async function verifyAdmin(token: string): Promise<void> {
  await send('/-/admin/verify', { headers: adminHeaders(token) })
}

// --- Peers --------------------------------------------------------------

export async function listPeers(token: string): Promise<AdminPeer[]> {
  // Empty lists come back as JSON `null`, so coerce to [].
  return (
    (await getJSON<AdminPeer[] | null>('/-/admin/peers', {
      headers: adminHeaders(token),
    })) ?? []
  )
}

export async function addPeer(
  token: string,
  url: string,
  peerType = 'upstream',
  importPeers = false,
): Promise<void> {
  await send('/-/admin/peers/add', {
    method: 'POST',
    headers: adminHeaders(token, true),
    body: JSON.stringify({ url, peer_type: peerType, import_peers: importPeers }),
  })
}

// Peer identity for delete/refresh is the URL (not the row id).
export async function deletePeer(token: string, url: string): Promise<void> {
  await send('/-/admin/peers/delete', {
    method: 'POST',
    headers: adminHeaders(token, true),
    body: JSON.stringify({ url }),
  })
}

export async function refreshPeer(token: string, url: string): Promise<void> {
  await send('/-/admin/peers/refresh', {
    method: 'POST',
    headers: adminHeaders(token, true),
    body: JSON.stringify({ url }),
  })
}

// GET /-/admin/peers/health?url=<peer> → {"status":"up"|"down"}.
export async function checkPeerHealth(token: string, url: string): Promise<boolean> {
  const res = await getJSON<{ status: string }>(
    `/-/admin/peers/health?url=${encodeURIComponent(url)}`,
    { headers: adminHeaders(token) },
  )
  return res.status === 'up'
}

// --- Contributors -------------------------------------------------------

export async function listContributors(token: string): Promise<Contributor[]> {
  return (
    (await getJSON<Contributor[] | null>('/-/admin/contributors', {
      headers: adminHeaders(token),
    })) ?? []
  )
}

export async function addContributor(
  token: string,
  name: string,
  publicKey: string,
): Promise<void> {
  await send('/-/admin/contributors/add', {
    method: 'POST',
    headers: adminHeaders(token, true),
    body: JSON.stringify({ name, public_key: publicKey }),
  })
}

// GET /-/admin/contributors/detected → signer keys seen in content, so the admin
// can add one to the keyring without hand-copying a hex key from the database.
export async function listDetectedSigners(token: string): Promise<DetectedSigner[]> {
  return (
    (await getJSON<DetectedSigner[] | null>('/-/admin/contributors/detected', {
      headers: adminHeaders(token),
    })) ?? []
  )
}

export async function revokeContributor(token: string, publicKey: string): Promise<void> {
  await send('/-/admin/contributors/delete', {
    method: 'POST',
    headers: adminHeaders(token, true),
    body: JSON.stringify({ public_key: publicKey }),
  })
}

// --- Startup sync -------------------------------------------------------

export async function listSyncs(token: string): Promise<StartupSync[]> {
  return (
    (await getJSON<StartupSync[] | null>('/-/admin/config', {
      headers: adminHeaders(token),
    })) ?? []
  )
}

// Sync identity for removal is the filename.
export async function removeSync(
  token: string,
  filename: string,
  deleteLocalFiles = false,
): Promise<string> {
  return getText('/-/admin/remove', {
    method: 'POST',
    headers: adminHeaders(token, true),
    body: JSON.stringify({ filename, deleteLocalFiles }),
  })
}

// POST /-/import → pulls remote content; returns a human-readable status string.
export async function triggerImport(
  token: string,
  url: string,
  filename = '',
  saveToStartup = true,
): Promise<string> {
  return getText('/-/import', {
    method: 'POST',
    headers: adminHeaders(token, true),
    body: JSON.stringify({ url, filename, saveToStartup }),
  })
}

// POST /-/reload → re-scan the data directory. Returns 204.
export async function triggerReload(token: string): Promise<void> {
  await send('/-/reload', { method: 'POST', headers: adminHeaders(token) })
}

// POST /-/admin/navigation/rescan → rebuild the navigation index.
export async function rescanNavigation(token: string): Promise<void> {
  await send('/-/admin/navigation/rescan', {
    method: 'POST',
    headers: adminHeaders(token),
  })
}
