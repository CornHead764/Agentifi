/**
 * What a scene changes as it is clicked through: a filter it saved, a receipt
 * it attached, a suggestion it asked for, a proposal it accepted. `serveFixtures`
 * resets it at the start of every page.
 */

export interface FilterItem {
  field: string
  operator: string
  negated?: boolean
  value_ids?: string[]
  value_texts?: string[]
  text?: string | null
  state?: boolean | null
}

export interface DemoState {
  filters: Map<string, { query_text: string | null; items: FilterItem[] }>
  nextFilter: number
  /** Transaction id to the attachment uploaded on it in this scene. */
  uploads: Map<string, string>
  /** Rows a category check was asked for, and when, so the poll answers "checking" for a moment. */
  checks: Map<string, number>
  /** Category suggestions accepted from the register. */
  applied: Set<number>
  /** The assistant conversation, once a question has been asked. */
  asked: boolean
  /** Assistant proposals accepted or declined in the chat. */
  decided: Map<string, 'applied' | 'discarded'>
  /** The receipt image the scene serves for an upload, rendered by the scene itself. */
  receiptImage: Buffer | null
}

export const state: DemoState = {
  filters: new Map(),
  nextFilter: 1,
  uploads: new Map(),
  checks: new Map(),
  applied: new Set(),
  asked: false,
  decided: new Map(),
  receiptImage: null,
}

export function resetState(): void {
  state.filters.clear()
  state.nextFilter = 1
  state.uploads.clear()
  state.checks.clear()
  state.applied.clear()
  state.asked = false
  state.decided.clear()
}
