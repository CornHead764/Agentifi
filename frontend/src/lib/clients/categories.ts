import { useQuery, type QueryKey } from '@tanstack/react-query'

import {
  CreateTagRequestSchema,
  CreateTagResponseSchema,
  TagService,
  UpdateTagRequestSchema,
  UpdateTagResponseSchema,
} from '@/gen/agentifi/v1/tag_pb'
import { api } from '@/lib/api'
import { plural } from '@/lib/format'
import { useInvalidatingMutation } from '@/lib/queryClient'
import { fromWire, toPatch, toWire } from '@/lib/rpc/wire'
import { rpcClient, unary } from '@/lib/rpcSession'
import { DEFAULT_QUERY, createFilter, listTransactions } from '@/lib/transactions/api'
import { CATEGORIES_KEY, TAGS_KEY, TRANSACTIONS_KEY } from '@/lib/transactions/cache'
import type {
  Category,
  CategoryKind,
  FilterItemWrite,
  Tag,
  Uuid,
} from '@/lib/transactions/types'

/** An omitted field is left alone; `parent_id: null` promotes a subcategory to a group. */
export interface CategoryWrite {
  name?: string
  kind?: CategoryKind
  parent_id?: Uuid | null
  /** Null is "not tax related". */
  txf_id?: string | null
  /** Sent together with `txf_id`, which holds the first of these. */
  txf_ids?: string[]
  excluded_from_reports?: boolean
  excluded_from_spending_plan?: boolean
  excluded_from_category_list?: boolean
  sort_order?: number
}

export interface CategoryCreate extends CategoryWrite {
  name: string
  kind: CategoryKind
}

export interface TagWrite {
  name?: string
  color?: string | null
}

/** A listed group's subcategories are all listed too. */
export interface UnusedCategory {
  id: Uuid
  parent_id: Uuid | null
  name: string
  kind: CategoryKind
  /** Two live categories may carry the same name. */
  path: string
}

export interface UnusedList {
  categories: UnusedCategory[]
  tags: Tag[]
  categories_checked: string[]
  tags_checked: string[]
}

export interface PurgeRequest {
  category_ids: Uuid[]
  tag_ids: Uuid[]
}

export interface PurgeResult {
  categories_deleted: number
  tags_deleted: number
  filters_repaired: number
  filters_retired: number
  /** Unreviewed rows an automation had filed under a purged category, asked about again. */
  resuggested: number
}

export interface CategoryDefaultsResult {
  created: number
  existing: number
  categories: Category[]
}

/**
 * All three exclusions are sent explicitly: a category created excluded would
 * silently drop every row filed under it (ground rule 4).
 */
export function inlineCategoryCreate(name: string): CategoryCreate {
  return {
    name: name.trim(),
    kind: 'expense',
    parent_id: null,
    excluded_from_reports: false,
    excluded_from_spending_plan: false,
    excluded_from_category_list: false,
  }
}

/** Every write here changes what the register renders, so transactions refresh too. */
export function useCreateCategory() {
  return useInvalidatingMutation(
    (body: CategoryCreate) => api.post<Category>('/categories', body),
    [CATEGORIES_KEY, TRANSACTIONS_KEY],
    { failure: 'The category was not created' },
  )
}

/** Skips every path the space already has, so it is safe to repeat. */
export function useAdoptDefaultCategories() {
  return useInvalidatingMutation(
    () => api.post<CategoryDefaultsResult>('/categories/defaults'),
    [CATEGORIES_KEY, TRANSACTIONS_KEY],
  )
}

export function useUpdateCategory() {
  return useInvalidatingMutation(
    ({ id, patch }: { id: Uuid; patch: CategoryWrite }) =>
      api.patch<Category>(`/categories/${id}`, patch),
    [CATEGORIES_KEY, TRANSACTIONS_KEY],
  )
}

/**
 * Soft delete. Transactions filed under it keep it; children keep a `parent_id`
 * the list no longer returns and surface as top-level.
 */
export function useDeleteCategory() {
  return useInvalidatingMutation(
    (id: Uuid) => api.delete<void>(`/categories/${id}`),
    [CATEGORIES_KEY, TRANSACTIONS_KEY],
  )
}

const UNUSED_KEY = [...CATEGORIES_KEY, 'unused'] as const

/** Expensive (a query per reference kind over all history): only while the dialog is open. */
export function useUnused(enabled: boolean) {
  return useQuery({
    queryKey: UNUSED_KEY,
    enabled,
    staleTime: 0,
    queryFn: ({ signal }) => api.get<UnusedList>('/unused', undefined, signal),
  })
}

/** The server re-checks each id and refuses the whole request if any is now used. */
export function usePurgeUnused() {
  return useInvalidatingMutation(
    (body: PurgeRequest) => api.post<PurgeResult>('/unused/purge', body),
    [CATEGORIES_KEY, TAGS_KEY, TRANSACTIONS_KEY],
  )
}

const tags = rpcClient(TagService)

export function useCreateTag() {
  return useInvalidatingMutation(
    (body: { name: string } & TagWrite) =>
      unary(TagService.method.createTag, async () => {
        const created = await tags.createTag(toWire(CreateTagRequestSchema, body))
        return fromWire<{ tag: Tag }>(CreateTagResponseSchema, created).tag
      }),
    [TAGS_KEY, TRANSACTIONS_KEY],
    { failure: 'That tag was not created' },
  )
}

export function useUpdateTag() {
  return useInvalidatingMutation(
    ({ id, patch }: { id: Uuid; patch: TagWrite }) =>
      unary(TagService.method.updateTag, async () => {
        const updated = await tags.updateTag(toPatch(UpdateTagRequestSchema, { tag_id: id }, patch))
        return fromWire<{ tag: Tag }>(UpdateTagResponseSchema, updated).tag
      }),
    [TAGS_KEY, TRANSACTIONS_KEY],
    { failure: 'That tag did not save' },
  )
}

/**
 * Unlinks the tag from every transaction and split, then soft deletes it, so
 * no chip is left resolving to nothing. Irreversible.
 */
export function useDeleteTag() {
  return useInvalidatingMutation(
    (id: Uuid) =>
      unary(TagService.method.deleteTag, async () => {
        await tags.deleteTag({ tagId: id })
      }),
    [TAGS_KEY, TRANSACTIONS_KEY],
    { failure: 'That tag was not deleted' },
  )
}

const MEMBERSHIP: Omit<FilterItemWrite, 'field' | 'value_ids'> = {
  operator: 'in',
  group_index: 0,
  position: 0,
  negated: false,
  value_texts: [],
  text: null,
  amount_min: null,
  amount_max: null,
  date_from: null,
  date_to: null,
  date_preset: null,
  state: null,
}

/**
 * Over all time, deliberately: it backs a delete warning. `/transactions`
 * narrows only by a stored filter, hence the ad-hoc one; `count` covers the
 * whole match set, so one row is enough. Reviewed rows only, the same
 * definition the unused list applies: an unreviewed row's category or tag is
 * still a guess.
 */
async function countCarrying(
  field: 'category' | 'tag',
  id: Uuid,
  signal?: AbortSignal,
): Promise<number> {
  const filter = await createFilter({
    name: null,
    scope: 'ad_hoc',
    query_text: null,
    items: [
      { ...MEMBERSHIP, field, value_ids: [id] },
      { ...MEMBERSHIP, field: 'is_reviewed', operator: 'is_true', state: true, value_ids: [], position: 1 },
    ],
  })
  const page = await listTransactions({ ...DEFAULT_QUERY, filterId: filter.id, limit: 1 }, signal)
  return page.count
}

/** Costs a filter row and a list query, so only while the dialog is open. */
function useUsage(key: QueryKey, field: 'category' | 'tag', id: Uuid | null) {
  return useQuery({
    queryKey: [...key, id ?? 'none', 'usage'],
    enabled: id !== null,
    staleTime: 30_000,
    queryFn: ({ signal }) => (id === null ? Promise.resolve(0) : countCarrying(field, id, signal)),
  })
}

export function useCategoryUsage(id: Uuid | null) {
  return useUsage(CATEGORIES_KEY, 'category', id)
}

export function useTagUsage(id: Uuid | null) {
  return useUsage(TAGS_KEY, 'tag', id)
}

export function reviewedTransactionCount(count: number): string {
  return plural(count, 'reviewed transaction')
}
