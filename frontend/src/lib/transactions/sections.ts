/** Which register sections the reader has closed, kept on the device. Every section starts open. */

import { readStoredJson, writeStoredJson } from '@/lib/storage'

const SECTIONS_COLLAPSED_KEY = 'register.collapsed'

/** Anything that is not a list of strings is treated as nothing stored. */
export function parseCollapsedSections(stored: unknown): ReadonlySet<string> {
  if (!Array.isArray(stored)) return new Set()
  return new Set(stored.filter((one): one is string => typeof one === 'string'))
}

/** Returns a new set, since the register holds this in React state. */
export function toggleCollapsedSection(
  sections: ReadonlySet<string>,
  section: string,
): ReadonlySet<string> {
  const next = new Set(sections)
  if (!next.delete(section)) next.add(section)
  return next
}

export function loadCollapsedSections(): ReadonlySet<string> {
  return parseCollapsedSections(readStoredJson(SECTIONS_COLLAPSED_KEY))
}

export function saveCollapsedSections(sections: ReadonlySet<string>): void {
  writeStoredJson(SECTIONS_COLLAPSED_KEY, [...sections])
}
