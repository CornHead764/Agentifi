import type { Category } from '@/lib/transactions/types'

/** Which facets a surface offers, and in what order. Kept out of `FilterFacets` for fast refresh. */
export type FacetId = 'categories' | 'payees' | 'tags' | 'accounts' | 'flags' | 'amount' | 'advanced'

/** What the register's Filter popover offers. */
export const REGISTER_FACETS: readonly FacetId[] = [
  'categories',
  'payees',
  'tags',
  'accounts',
  'flags',
  'advanced',
]

/** An automation trigger's facets: the builders' plus Payees, since a trigger has no name column. */
export const TRIGGER_FACETS: readonly FacetId[] = [
  'categories',
  'payees',
  'tags',
  'accounts',
  'flags',
  'amount',
  'advanced',
]

/**
 * What the rules and guidance builders offer beside their name column. No
 * Payees, which the name column already asks.
 */
export const BUILDER_FACETS: readonly FacetId[] = [
  'categories',
  'tags',
  'accounts',
  'flags',
  'amount',
  'advanced',
]

/** What the Categories facet offers: the assignable categories. */
export function facetCategories(categories: readonly Category[]): Category[] {
  return categories.filter((category) => category.is_user_assignable)
}
