import type { Category, Uuid } from '@/lib/transactions/types'

/**
 * What the category picker offers: assignable categories, less those hidden
 * from the category list and the children of a hidden parent. The current
 * value stays, so a row already filed under a hidden category still shows it.
 */
export function pickableCategories(
  categories: readonly Category[],
  value: Uuid | null,
): Category[] {
  const hidden = new Set(
    categories
      .filter((category) => category.excluded_from_category_list)
      .map((category) => category.id),
  )
  return categories.filter(
    (category) =>
      category.is_user_assignable &&
      (category.id === value ||
        (!hidden.has(category.id) &&
          (category.parent_id === null || !hidden.has(category.parent_id)))),
  )
}
