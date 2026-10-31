/** The subscribe/unsubscribe pair every small external store outside React needs. */
export interface ListenerSet<Args extends unknown[] = []> {
  subscribe(listener: (...args: Args) => void): () => void
  notify(...args: Args): void
  /** Test seam: drop every subscriber. */
  clear(): void
}

export function createListenerSet<Args extends unknown[] = []>(): ListenerSet<Args> {
  const listeners = new Set<(...args: Args) => void>()
  return {
    subscribe(listener) {
      listeners.add(listener)
      return () => {
        listeners.delete(listener)
      }
    },
    notify(...args) {
      for (const listener of listeners) listener(...args)
    },
    clear() {
      listeners.clear()
    },
  }
}
