/**
 * The data row under the cursor, from the payload. Recharts names the hovered
 * point by its axis label, and a long axis repeats labels, so looking the
 * label up finds the wrong row. Its own module so both chart files can use it.
 */
export function hoveredRow<T>(payload: readonly { payload?: T }[] | undefined): T | undefined {
  for (const entry of payload ?? []) {
    if (entry?.payload) return entry.payload
  }
  return undefined
}
