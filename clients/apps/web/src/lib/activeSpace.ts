/**
 * Which space every request is about, sent as `X-Space-Id` (absent, the server
 * uses the caller's oldest membership). Outside React because the API client
 * reads it synchronously. Stored per user, so a different sign-in never sends
 * the previous session's space.
 */

import { createListenerSet } from './listenerSet'
import { clearStored, readStored, writeStored } from './storage'

export const SPACE_HEADER = 'X-Space-Id'

const STORAGE_PREFIX = 'activeSpace.'

/** `agentifi.activeSpace.<user id>` — exported so a test can name it. */
export function activeSpaceStorageKey(userId: string): string {
  return STORAGE_PREFIX + userId
}

const listeners = createListenerSet()

let owner: string | null = null
let chosen: string | null = null

/** Null for "whichever the server defaults to". */
export function activeSpaceId(): string | null {
  return chosen
}

/**
 * Bind the store to whoever is signed in, and return their remembered space.
 * Called during render, not in an effect: a child's query effects run before
 * its parent's and would fetch under the default space.
 */
export function adoptUser(userId: string | null): string | null {
  if (userId === owner) return chosen
  owner = userId
  chosen = userId === null ? null : (readStored(activeSpaceStorageKey(userId)) || null)
  return chosen
}

/** Choose a space, or pass null to go back to the server's default. */
export function setActiveSpace(spaceId: string | null): void {
  if (chosen === spaceId) return
  chosen = spaceId
  if (owner !== null) {
    if (spaceId === null) clearStored(activeSpaceStorageKey(owner))
    else writeStored(activeSpaceStorageKey(owner), spaceId)
  }
  listeners.notify()
}

/** Drop a choice the server answers "Space not found" for; the API client calls this. */
export function forgetActiveSpace(): void {
  setActiveSpace(null)
}

export function onActiveSpaceChange(listener: () => void): () => void {
  return listeners.subscribe(listener)
}

/** Test seam: forget both the user and their choice. */
export function resetActiveSpace(): void {
  owner = null
  chosen = null
}
