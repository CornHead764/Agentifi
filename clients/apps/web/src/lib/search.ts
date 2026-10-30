/** Text as a search compares it: trimmed, lower case, accents dropped ("Café" is "cafe"). */
export function foldText(text: string): string {
  return text
    .trim()
    .normalize('NFD')
    .replace(/\p{Diacritic}/gu, '')
    .toLocaleLowerCase()
}

/**
 * Whether any of `texts` contains what was typed, ignoring case and accents.
 * A blank search matches everything.
 */
export function matchesSearch(search: string, ...texts: readonly (string | null | undefined)[]): boolean {
  const needle = foldText(search)
  if (needle === '') return true
  return texts.some((text) => text != null && foldText(text).includes(needle))
}
