/**
 * The display locale; `undefined` is the browser's. A module variable, not a
 * context, because chart ticks and CSV builders cannot read a context.
 */
let displayLocaleTag: string | undefined

/** A tag `Intl` rejects falls back to the browser's locale rather than throwing in every formatter. */
export function setDisplayLocale(tag: string | null | undefined): void {
  const trimmed = tag?.trim() ?? ''
  if (trimmed === '') {
    displayLocaleTag = undefined
    return
  }
  try {
    displayLocaleTag = Intl.getCanonicalLocales(trimmed)[0]
  } catch {
    displayLocaleTag = undefined
  }
}

export function displayLocale(): string | undefined {
  return displayLocaleTag
}
