import type { CategoryKind } from '@/lib/transactions/types'

/** Expense first: it is what almost every new category turns out to be. */
export const CATEGORY_KINDS: { value: CategoryKind; label: string }[] = [
  { value: 'expense', label: 'Expense' },
  { value: 'income', label: 'Income' },
  { value: 'transfer', label: 'Transfer' },
]

export function kindLabel(kind: CategoryKind): string {
  return CATEGORY_KINDS.find((entry) => entry.value === kind)?.label ?? kind
}

/** A select hands back a string; only the three the server accepts get through. */
export function readKind(value: string, fallback: CategoryKind): CategoryKind {
  return CATEGORY_KINDS.find((entry) => entry.value === value)?.value ?? fallback
}
