import {
  CircleCheck,
  Download,
  FileUp,
  PanelTop,
  Plus,
  Receipt,
  Sparkles,
  Tag,
  Target,
} from 'lucide-react'
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router-dom'

import { CONNECT_PATH, IMPORT_PATH } from '@/components/onboarding/paths'
import { accountIdsFor, buildAccountTree, nodeName } from '@/components/shell/accountTree'
import { AccountHeader, AllAccountsHeader } from '@/components/transactions/AccountHeader'
import { AggregateView, type ChartShape } from '@/components/transactions/AggregateView'
import {
  CreateFromTransaction,
  type CreateTarget,
} from '@/components/transactions/CreateFromTransaction'
import { CategoryCheckStrip } from '@/components/transactions/CategoryCheckStrip'
import { DuplicatesStrip } from '@/components/transactions/DuplicatesStrip'
import { CustomizeColumns } from '@/components/transactions/CustomizeColumns'
import { DateRangeControl } from '@/components/transactions/DateRangeControl'
import { DrillChips } from '@/components/transactions/DrillChips'
import { FilterPanel } from '@/components/transactions/FilterPanel'
import { RegisterGrid } from '@/components/transactions/RegisterGrid'
import { ProjectedCashFlow } from '@/components/transactions/ProjectedCashFlow'
import { RemindersStrip } from '@/components/transactions/RemindersStrip'
import { PurchaseDialog } from '@/components/transactions/PurchaseDialog'
import { BulkGoalDialog } from '@/components/transactions/BulkGoalDialog'
import { TagRowsDialog, type TagRowsMode } from '@/components/transactions/TagRowsDialog'
import { LinkRefundDialog } from '@/components/transactions/LinkRefundDialog'
import { LinkSeriesDialog } from '@/components/transactions/LinkSeriesDialog'
import { RowMenu } from '@/components/transactions/RowMenu'
import { SearchBox, SearchNotes } from '@/components/transactions/SearchBox'
import { SelectionBar } from '@/components/transactions/SelectionBar'
import { TransactionDialog } from '@/components/transactions/TransactionDialog'
import type { RegisterView } from '@/components/transactions/register-context'
import {
  Button,
  Callout,
  Card,
  ConfirmDialog,
  EmptyState,
  OverflowMenu,
  PageHeader,
  Tabs,
  TabsList,
  TabsTrigger,
  useConfirm,
  useToast,
} from '@/components/ui'
import { categoryName as categoryNameFor } from '@/lib/categoryNames'
import { useSettled } from '@/lib/useSettled'
import { RunDialog } from '@/pages/assistant/RunDialog'
import { useSuggestCategories } from '@/pages/assistant/suggestCategories'
import { useAssistantStatus } from '@/lib/clients/assistant'
import { useUpdateAccountSettings } from '@/lib/clients/connections'
import { usePendingAutomationActions } from '@/lib/clients/automations'
import { useLinkRefund } from '@/lib/clients/refunds'
import {
  useCancelSuggestionBatch,
  useDismissSuggestionBatch,
  useLatestSuggestionBatch,
} from '@/lib/clients/suggestionBatches'
import { useCurrentSpace } from '@/lib/clients/spaces'
import { useHeaderOverride } from '@/contexts/headerOverride'
import { acceptsManualTransactions, accountNamer } from '@/lib/accounts'
import { defaultScopeNode, rememberScopeNode, useChosenScopeNode } from '@/lib/accountScope'
import { formatCount } from '@/lib/format'
import type { GroupBy } from '@/lib/transactions/aggregate'
import { DEFAULT_QUERY, type RegisterQuery } from '@/lib/transactions/api'
import { flattenPages } from '@/lib/transactions/cache'
import {
  ACTIVITY_COLUMNS,
  loadColumnPrefs,
  NARROW_QUERY,
  saveColumnPrefs,
  visibleColumns,
  type ColumnId,
  type ColumnPrefs,
} from '@/lib/transactions/columns'
import {
  ALL_TIME_SELECTION,
  asRangePreset,
  labelForSelection,
  selectionForRangePreset,
  type DateSelection,
} from '@/lib/dateRanges'
import { displayPayee } from '@/lib/transactions/edits'
import { canBeARefund, categoryKindLookup } from '@/lib/transactions/refunds'
import {
  DEFAULT_SWIPE_LEFT,
  DEFAULT_SWIPE_RIGHT,
  REVIEW_QUEUE_SWIPE,
} from '@/lib/transactions/gestures'
import { useRoamingPreferences } from '@/lib/preferences'
import { openingFilter } from '@/lib/transactions/openingFilter'
import {
  builtInQuickFilters,
  isQuickFilterOn,
  quickFilterFromSaved,
  type QuickFilter,
} from '@/lib/transactions/quickFilters'
import { useCreateQuickFilter, useSavedQuickFilters } from '@/lib/clients/quickFilters'
import {
  EMPTY_DRAFT,
  activeFacetCount,
  mergeDrafts,
  toFilterItems,
  type FilterDraft,
} from '@/lib/transactions/filter'
import {
  drillFacets,
  drillLabel,
  drillUnder,
  readDrill,
  withDrill,
  type DrillStep,
} from '@/lib/transactions/drill'
import {
  REGISTER_TABS,
  openingTab,
  readTab,
  registerTabLabel,
  withTab,
} from '@/lib/transactions/registerTab'
import {
  describeApiError,
  useAccountSummary,
  useAccounts,
  useAdHocFilter,
  useApplySuggestion,
  useCategories,
  useCategoryChecks,
  useCreateTransaction,
  useDeleteTransaction,
  useDiscardSuggestion,
  useEditTransaction,
  useLinkSeries,
  useMarkAllReviewed,
  useRegister,
  useTransactionAggregate,
  useSetReviewed,
  useSetTags,
  usePayees,
  useTags,
  useTransaction,
  useUnlinkSeries,
} from '@/lib/transactions/queries'
import { categoryIds } from '@/lib/transactions/category'
import { buildRows } from '@/lib/transactions/rows'
import {
  loadCollapsedSections,
  saveCollapsedSections,
  toggleCollapsedSection,
} from '@/lib/transactions/sections'
import { menuTargets, nextSelection, withoutSection } from '@/lib/transactions/selection'
import { parseSearch } from '@/lib/transactions/search'
import type { Transaction, Uuid } from '@/lib/transactions/types'
import { useMediaQuery } from '@/lib/useMediaQuery'
import { downloadCsv } from '@/lib/csv'
import { registerToCsv } from '@/lib/transactions/csv'
import { rangeToggled } from '@/lib/selection'
import { ManageQuickFiltersDialog, QuickFilterDialog } from './transactions/QuickFilterDialogs'
import { QuickFilters } from './transactions/QuickFilters'

/** All time, as one object so the derived window keeps its identity. */

/** A closed selection, as one object so the goal dialog does not remount on every render. */
const EMPTY_SELECTION: ReadonlySet<Uuid> = new Set()

export function TransactionsPage() {
  const [params, setParams] = useSearchParams()
  const toast = useToast()
  const navigate = useNavigate()

  // A row a link pointed at — a merchant's orders list names the bank row it
  // matched — marked in the register for as long as the link's URL stands.
  const highlight = params.get('highlight')
  // And a row a link asked to *open*: the assistant's card edits one category,
  // and anything else about the row is edited here.
  const requestedEdit = params.get('edit')

  // Read once, on mount: the link's own filters are where the page starts, not
  // a value it stays bound to.
  const [opening] = useState(() => openingFilter(params))
  const [search, setSearch] = useState('')
  const [panel, setPanel] = useState<FilterDraft>(opening.panel)
  const [picked, setPicked] = useState<DateSelection | null>(null)
  const [order, setOrder] = useState<RegisterQuery['order']>(DEFAULT_QUERY.order)
  // Padding income rows stay out of the list until asked for; the result chip
  // says how many there are and brings them back.
  const [padding, setPadding] = useState<RegisterQuery['padding']>('hide')
  const [prefs, setPrefs] = useState<ColumnPrefs>(loadColumnPrefs)
  // Which group headings are shut, read from the device so they stay shut
  // across a reload.
  const [collapsedSections, setCollapsedSections] =
    useState<ReadonlySet<string>>(loadCollapsedSections)
  const [selected, setSelected] = useState<ReadonlySet<Uuid>>(new Set())
  const selectionAnchor = useRef<Uuid | null>(null)
  // Selection mode, phone only: the desktop always draws its checkbox column.
  // Kept here because the bulk actions live in this page's toolbar.
  const [selecting, setSelecting] = useState(false)
  const [reviewMode, setReviewMode] = useState(false)
  const [savingQuick, setSavingQuick] = useState(false)
  const [managingQuick, setManagingQuick] = useState(false)
  const [groupBy, setGroupBy] = useState<GroupBy>('category')
  const [shape, setShape] = useState<ChartShape>('total')
  const [editing, setEditing] = useState<Transaction | null>(null)
  const [dialogOpen, setDialogOpen] = useState(false)
  const [viewingRun, setViewingRun] = useState<Uuid | null>(null)
  const [creating, setCreating] = useState<CreateTarget>(null)
  const [linking, setLinking] = useState<Transaction | null>(null)
  const [refunding, setRefunding] = useState<Transaction | null>(null)
  // The row whose purchase — any merchant's — is being picked.
  const [pickingFor, setPickingFor] = useState<Transaction | null>(null)
  const [filingUnderGoal, setFilingUnderGoal] = useState(false)
  const [tagging, setTagging] = useState<{ mode: TagRowsMode; ids: Uuid[] } | null>(null)
  const suggestions = useSuggestCategories()

  // Clearing the box clears the search at once; only typing waits.
  const settledSearch = useSettled(search)
  const debounced = search === '' ? '' : settledSearch

  // The row a link asked to open, read by id: the dialog must open whether or
  // not the register's own query contains it. Derived from the URL rather than
  // copied into state, so the two cannot disagree.
  const requested = useTransaction(requestedEdit)
  const linkedRow = requested.data?.id === requestedEdit ? requested.data : null

  /** Whatever else is on the URL, this row is no longer the one being shown. */
  const forgetRequestedEdit = useCallback(() => {
    if (params.get('edit') === null) return
    const next = new URLSearchParams(params)
    next.delete('edit')
    setParams(next, { replace: true })
  }, [params, setParams])

  /** Open a row by id through the `edit` link, which finds it whether or not the register lists it. */
  const openRow = useCallback(
    (id: Uuid) => {
      const next = new URLSearchParams(params)
      next.set('edit', id)
      setParams(next, { replace: true })
    },
    [params, setParams],
  )

  // Dropping `edit` here as well as on close, so that opening a second row
  // from the register does not lose to the one the URL still names.
  const openDetail = useCallback(
    (txn: Transaction) => {
      setEditing(txn)
      setDialogOpen(true)
      forgetRequestedEdit()
    },
    [forgetRequestedEdit],
  )

  const closeDetail = () => {
    setDialogOpen(false)
    forgetRequestedEdit()
  }

  const accounts = useAccounts()
  const categories = useCategories()
  // One lookup for the whole page rather than a search inside every row's menu:
  // whether "This refunds…" is offered depends on the row's category kind.
  const categoryKind = useMemo(
    () => categoryKindLookup(categories.data ?? []),
    [categories.data],
  )
  const tags = useTags()
  const { data: space } = useCurrentSpace()

  // The window the register opens on: what the reader clicked, else the link's,
  // else the space's default range, else all time. Derived rather than set by
  // an effect, since the space lands a render after mount.
  const preferred = useMemo(
    () => selectionForRangePreset(asRangePreset(space?.default_date_range)),
    [space?.default_date_range],
  )
  const dates = picked ?? opening.dates ?? preferred ?? ALL_TIME_SELECTION

  // `displayNode` can be a group or class rather than an account, and the
  // endpoint takes account ids only, so the node is resolved through the tree.
  const accountTree = useMemo(() => buildAccountTree(accounts.data ?? []), [accounts.data])
  // The scope: the node this URL names, else the last one picked, else Banking
  // (see lib/accountScope). "All Accounts" counts as a pick; only never having
  // chosen gets the default.
  const urlNode = params.get('displayNode')
  const rememberedNode = useChosenScopeNode()
  const chosenNode = urlNode ?? rememberedNode
  const displayNode = chosenNode ?? defaultScopeNode(accountTree)
  useEffect(() => {
    if (urlNode !== null) rememberScopeNode(urlNode)
  }, [urlNode])
  const accountIds = useMemo(
    () => accountIdsFor(accountTree, displayNode),
    [accountTree, displayNode],
  )
  // A group resolves through the tree, so before the accounts land there is
  // nothing to ask for; asking would answer an empty register.
  const treePending = accounts.data === undefined
  const unresolved =
    (treePending && accountIds !== null && accountIds.length === 0) ||
    // With no pick, the default waits on the tree too. A failed read is
    // decided: the accounts are not coming.
    (treePending && chosenNode === null && !accounts.isError)

  const account = accounts.data?.find((row) => row.id === displayNode) ?? null
  // The account's own opening tab, unless the URL names one.
  const accountTab = openingTab(account)
  const tab = readTab(params, accountTab)
  const updateAccount = useUpdateAccountSettings()
  const scoped = useMemo(() => {
    const rows = accounts.data ?? []
    return accountIds === null ? rows : rows.filter((row) => accountIds.includes(row.id))
  }, [accountIds, accounts.data])

  const universe = useMemo(
    () => ({ categories: categories.data ?? [], tags: tags.data ?? [] }),
    [categories.data, tags.data],
  )

  // A register scoped to one account offers New for that account only, so an
  // account that takes no manual rows has no New button at all.
  const canCreate = account
    ? acceptsManualTransactions(account)
    : (accounts.data ?? []).some(acceptsManualTransactions)

  const savedQuick = useSavedQuickFilters()
  const createQuick = useCreateQuickFilter()
  const today = useMemo(() => new Date(), [])
  const quickFilters = useMemo(
    () => [
      ...builtInQuickFilters(today),
      ...(savedQuick.data ?? []).map((row) => quickFilterFromSaved(row, universe, today)),
    ],
    [savedQuick.data, universe, today],
  )
  // The chart's drill, on the URL so Back steps out of it, folded into the
  // panel's facets: one filter, whichever control narrowed it.
  const drill = useMemo(() => readDrill(params), [params])
  const drilled = useMemo(() => ({ ...panel, ...drillFacets(drill) }), [panel, drill])
  const crumbs = useMemo(
    () =>
      drill.map((step) => ({ key: `${step.by}:${step.key}`, label: drillLabel(step, universe) })),
    [drill, universe],
  )
  const setDrill = (steps: readonly DrillStep[]) => setParams(withDrill(params, steps))

  const activeQuick = useMemo(
    () =>
      quickFilters.find((quick) =>
        isQuickFilterOn(quick, { panel: drilled, search, dates }, universe),
      )?.id ?? null,
    [quickFilters, drilled, search, dates, universe],
  )

  const parsed = useMemo(() => parseSearch(debounced, { universe }), [debounced, universe])
  const draft = useMemo(() => mergeDrafts(drilled, parsed.draft), [drilled, parsed.draft])
  const items = useMemo(() => toFilterItems(draft, universe), [draft, universe])
  const filter = useAdHocFilter(items, debounced)

  const query: RegisterQuery = useMemo(
    () => ({
      ...DEFAULT_QUERY,
      accountIds,
      from: dates.range.from,
      to: dates.range.to,
      // The register shows the posted date; the aggregate tabs are reports and
      // file a row under its effective date. Conflating them shifts a whole
      // month of credit-card spending.
      dateField: tab === 'all' ? 'posted' : 'effective',
      filterId: filter.filterId,
      padding,
      order,
    }),
    [accountIds, dates.range.from, dates.range.to, filter.filterId, padding, order, tab],
  )

  const register = useRegister(query, !filter.pending && !unresolved)
  // The chart and its total over the whole match set, from the register's own
  // query (trap 5).
  const aggregate = useTransactionAggregate(
    query,
    tab === 'income' ? 'income' : 'spending',
    groupBy,
    groupBy === 'category' ? drillUnder(drill) : null,
    tab !== 'all' && !filter.pending && !unresolved,
  )
  // Trap 5: the summary takes the register's own query, not its own window.
  const carried = useAccountSummary(account?.id ?? null, query)
  const rows = useMemo(() => flattenPages(register.data), [register.data])
  const summary = register.data?.pages[0]

  const { fetchNextPage, hasNextPage, isFetchingNextPage } = register

  const edit = useEditTransaction()
  const linkRefund = useLinkRefund()
  const review = useSetReviewed()
  const retag = useSetTags()
  const create = useCreateTransaction()
  const remove = useConfirm(useDeleteTransaction())
  // "Mark all as reviewed" takes the whole query, not the page, and cannot be
  // undone once the Unreviewed filter empties, so it asks at least as loudly
  // as the smaller bulk actions.
  const bulkReview = useConfirm(useMarkAllReviewed(), {
    variables: (all: RegisterQuery) => ({ query: all, reviewed: true }),
  })
  const linkToSeries = useLinkSeries()
  const unlinkFromSeries = useUnlinkSeries()
  const applySuggestion = useApplySuggestion()
  const discardSuggestion = useDiscardSuggestion()
  const deciding = applySuggestion.isPending || discardSuggestion.isPending

  // How many proposals are waiting anywhere, for the chip on the unreviewed
  // view: the register is where they are decided.
  const waiting = usePendingAutomationActions()
  // Only while a run is being read: the catalogue is needed only to say which
  // tools write.
  const assistant = useAssistantStatus(viewingRun !== null)

  const decideSuggestion = useCallback(
    (txn: Transaction, categoryId?: Uuid | null, splitOverrides?: { index: number; category_id: string }[]) => {
      const suggestion = txn.suggestion
      if (suggestion === null) return
      applySuggestion.mutate({ txn, suggestion, categoryId, splitOverrides })
    },
    [applySuggestion],
  )

  // Every category check this register is waiting on, the rows this page asked
  // for and the rows the server says are busy, in one set, so each row keeps
  // its own poll.
  const checks = useCategoryChecks(rows)
  const startChecks = checks.start
  // The latest request as a whole, which may be far larger than the loaded
  // rows, followed on the server.
  const latestBatch = useLatestSuggestionBatch().data ?? null
  const cancelBatch = useCancelSuggestionBatch()
  const dismissBatch = useDismissSuggestionBatch()
  // "Which were they" is answered by the register, through the one Filter
  // (ground rule 3).
  const showUndetermined = useCallback(
    () => setPanel((current) => ({ ...current, categoryUndetermined: true })),
    [],
  )

  const dropSuggestion = useCallback(
    (txn: Transaction) => {
      const suggestion = txn.suggestion
      if (suggestion === null) return
      discardSuggestion.mutate({ txn, suggestion })
    },
    [discardSuggestion],
  )

  // `mutate` is stable across renders; the mutation *result* is not, and a
  // context value that changed identity every render would re-render every
  // visible row of the grid on each keystroke.
  const editRow = edit.mutate
  const reviewRow = review.mutate
  const tagRow = retag.mutate

  // A delete is a soft delete that also releases a transfer partner, so it is
  // confirmed, in the app's own dialog.

  const accountName = useMemo(() => accountNamer(accounts.data), [accounts.data])
  const categoryName = useCallback(
    (id: Uuid | null) => categoryNameFor(categories.data ?? [], id),
    [categories.data],
  )
  const tagName = useCallback(
    (id: Uuid) => tags.data?.find((tag) => tag.id === id)?.name ?? 'Unknown tag',
    [tags.data],
  )

  // What a swipe on a phone row runs, read from the account so it follows the
  // person to their next phone.
  const roaming = useRoamingPreferences()
  const inReviewQueue = draft.isReviewed === false
  const swipeLeft = inReviewQueue ? REVIEW_QUEUE_SWIPE.left : (roaming?.swipeLeft ?? DEFAULT_SWIPE_LEFT)
  const swipeRight = inReviewQueue
    ? REVIEW_QUEUE_SWIPE.right
    : (roaming?.swipeRight ?? DEFAULT_SWIPE_RIGHT)

  const frequentCategoryIds = useMemo(() => rankCategories(rows), [rows])
  // The whole space's payees, not just the loaded pages': a facet that only
  // offers what is in view cannot narrow to a payee the filter would exclude.
  const allPayees = usePayees()
  const payees = useMemo(() => allPayees.data ?? distinctPayees(rows), [allPayees.data, rows])

  const registerRows = useMemo(
    () => buildRows(rows, { collapsed: collapsedSections }),
    [rows, collapsedSections],
  )

  // What select-all may reach: the built list, so exactly what is on screen,
  // with closed sections' rows left out.
  const visibleIds = useMemo(
    () => registerRows.flatMap((row) => (row.kind === 'transaction' ? [row.txn.id] : [])),
    [registerRows],
  )

  /** Open or close a section, and deselect what closing it hid, so no bulk action runs over hidden rows. */
  const toggleSection = useCallback(
    (section: string) => {
      const closing = !collapsedSections.has(section)
      const next = toggleCollapsedSection(collapsedSections, section)
      setCollapsedSections(next)
      saveCollapsedSections(next)
      if (closing) setSelected((current) => withoutSection(current, rows, section))
    },
    [collapsedSections, rows],
  )

  const view: RegisterView = useMemo(
    () => ({
      lookups: {
        accounts: accounts.data ?? [],
        categories: categories.data ?? [],
        tags: tags.data ?? [],
        accountName,
        categoryName,
        tagName,
        frequentCategoryIds,
        checkingCategories: checks.pending,
      },
      actions: {
        edit: (txn, patch, optimistic) => editRow({ id: txn.id, patch, optimistic }),
        setReviewed: (txn, next) => reviewRow({ id: txn.id, reviewed: next }),
        setTags: (txn, tagIds) => tagRow({ id: txn.id, tagIds }),
        openDetail,
        reviewMode,
        applySuggestion: decideSuggestion,
        discardSuggestion: dropSuggestion,
        decidingSuggestion: deciding,
        refuse: (reason) => toast.show({ title: 'Not editable', description: reason }),
        showRun: setViewingRun,
      },
      selection: {
        enabled: true,
        active: selecting,
        ids: selected,
        toggle: (id, extend = false) => {
          const anchor = selectionAnchor.current
          selectionAnchor.current = id
          setSelected((current) => rangeToggled(visibleIds, current, anchor, id, extend))
        },
        begin: (id) => {
          setSelecting(true)
          setSelected((current) => (current.has(id) ? current : new Set(current).add(id)))
        },
        visibleIds,
        toggleAll: () => setSelected((current) => nextSelection(visibleIds, current)),
      },
      sections: { collapsed: collapsedSections, toggle: toggleSection },
      swipe: { left: swipeLeft, right: swipeRight },
      multiAccount: account === null,
    }),
    [
      account,
      accountName,
      accounts.data,
      categories.data,
      categoryName,
      checks.pending,
      collapsedSections,
      editRow,
      frequentCategoryIds,
      deciding,
      decideSuggestion,
      dropSuggestion,
      openDetail,
      tagRow,
      reviewMode,
      reviewRow,
      selected,
      selecting,
      swipeLeft,
      swipeRight,
      tagName,
      tags.data,
      toast,
      toggleSection,
      visibleIds,
    ],
  )

  // The row the dialog shows, re-read from the register every render: a copy
  // captured before a decision would keep offering the applied proposal.
  const shown = linkedRow ?? editing
  const current = useMemo(
    () => (shown === null ? null : (rows.find((row) => row.id === shown.id) ?? shown)),
    [rows, shown],
  )

  // Only the loaded rows: a selection carried through a filter change can name
  // rows no longer here.
  const selectedRows = useMemo(() => rows.filter((row) => selected.has(row.id)), [rows, selected])
  const allSelectedReviewed =
    selectedRows.length > 0 && selectedRows.every((row) => row.is_reviewed)

  /** Leave selection mode and forget what was picked in it. */
  const exitSelection = useCallback(() => {
    setSelecting(false)
    setSelected(new Set())
  }, [])

  // Held here because two surfaces run them: the desktop's ⋮ menu and the
  // phone's selection bar.
  const reviewSelected = useCallback(() => {
    for (const id of selected) reviewRow({ id, reviewed: !allSelectedReviewed })
    exitSelection()
  }, [allSelectedReviewed, exitSelection, reviewRow, selected])

  const suggestFor = suggestions.suggest
  const suggesting = suggestions.busy

  // Forced, like the row menu's "Suggest a category": the arriving-rows
  // predicate refuses every source but a bank sync and a file import, so an
  // imported ledger would queue nothing.
  const suggestSelected = useCallback(() => {
    suggestFor({ ids: [...selected] }, (ids) => {
      exitSelection()
      // The rows carry their own spinners and the strip above the grid counts
      // them off.
      startChecks(ids)
    })
  }, [exitSelection, selected, startChecks, suggestFor])

  // Every row the query matches, loaded or not, as "Mark all as reviewed"
  // reaches them.
  const matchingCount = summary?.count ?? 0
  const suggestAllMatching = useCallback(() => {
    suggestFor({ query, count: matchingCount }, exitSelection)
  }, [exitSelection, matchingCount, query, suggestFor])
  // Select-all reaches only the loaded rows; once it holds them all and the
  // query matches more, the rest are offered by count.
  const moreThanSelected =
    selected.size > 0 &&
    matchingCount > selected.size &&
    visibleIds.every((id) => selected.has(id))

  // One row, from the ⋮ and from the row's dialog, through the same tracker as
  // the bulk suggestion; the row's spinner is the acknowledgement.
  const suggestRow = useCallback(
    (row: Transaction) => suggestFor({ ids: [row.id] }, startChecks),
    [startChecks, suggestFor],
  )

  // Escape is the way out of every other mode in the app, and a bar with an X
  // in it that a keyboard cannot dismiss is a trap on a tablet with a case.
  useEffect(() => {
    if (!selecting) return
    const onKey = (event: KeyboardEvent) => {
      if (event.key === 'Escape') exitSelection()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [exitSelection, selecting])

  // The header, while this mode is on. Memoized because the shell holds it as
  // state: a fresh element every render would set that state every render.
  const selectionBar = useMemo(
    () =>
      selecting ? (
        <SelectionBar
          count={selected.size}
          allReviewed={allSelectedReviewed}
          busy={suggesting}
          onExit={exitSelection}
          onMarkReviewed={reviewSelected}
          onSuggestCategories={suggestSelected}
          onCountTowardGoal={() => setFilingUnderGoal(true)}
        />
      ) : null,
    [
      allSelectedReviewed,
      exitSelection,
      suggesting,
      reviewSelected,
      suggestSelected,
      selected.size,
      selecting,
    ],
  )
  useHeaderOverride(selectionBar)

  /** Whether the register's own toolbar carries the bulk actions, or the bar does. */
  const bulkInToolbar = !selecting && selected.size > 0

  const narrow = useMediaQuery(NARROW_QUERY)
  const columns = useMemo(
    () =>
      visibleColumns(
        prefs,
        tab === 'all' ? undefined : ACTIVITY_COLUMNS,
        account === null ? 'many-accounts' : 'one-account',
        narrow,
      ),
    [prefs, tab, account, narrow],
  )

  const updatePrefs = (next: ColumnPrefs) => {
    setPrefs(next)
    saveColumnPrefs(next)
  }

  // What the grid could draw, so Customize Columns can say which chosen
  // columns the width is holding back.
  const [shownColumns, setShownColumns] = useState<readonly ColumnId[] | undefined>(undefined)

  const noAccounts = accounts.isSuccess && accounts.data.length === 0
  const narrowed =
    debounced !== '' ||
    activeFacetCount(panel) > 0 ||
    drill.length > 0 ||
    dates.range.from !== null ||
    dates.range.to !== null

  const clearAll = () => {
    setSearch('')
    setPanel(EMPTY_DRAFT)
    if (drill.length > 0) setDrill([])
    setPicked(ALL_TIME_SELECTION)
    exitSelection()
  }

  // Choosing a quick filter sets the whole filter to it; choosing the one
  // already showing clears it.
  const chooseQuick = (quick: QuickFilter) => {
    if (drill.length > 0) setDrill([])
    if (quick.id === activeQuick) {
      setSearch('')
      setPanel(EMPTY_DRAFT)
      if (quick.dates !== null) setPicked(ALL_TIME_SELECTION)
    } else {
      setSearch(quick.search)
      setPanel(quick.panel)
      if (quick.dates !== null) setPicked(quick.dates)
    }
    exitSelection()
  }

  const grid = (
    <RegisterGrid
      rows={registerRows}
      columns={columns}
      height={prefs.height}
      view={view}
      onShownChange={setShownColumns}
      sort={{
        order,
        // A flip is a new query, so the pages already loaded are dropped and
        // the register is paged from the other end rather than reversed here.
        onToggle: () => setOrder((current) => (current === 'desc' ? 'asc' : 'desc')),
      }}
      loading={
        filter.error === null &&
        (filter.pending || unresolved || (register.isFetching && rows.length === 0))
      }
      // With every section shut, the list is shorter than the window and the
      // load-more trigger would fire on its own, paging in the whole ledger.
      hasMore={tab === 'all' && hasNextPage && visibleIds.length > 0}
      onLoadMore={() => {
        if (!isFetchingNextPage) void fetchNextPage()
      }}
      highlight={highlight}
      rowMenu={(txn, control) => (
        <RowMenu
          {...control}
          txn={txn}
          onEdit={openDetail}
          onReview={openDetail}
          onDelete={(row) => remove.ask(row.id)}
          onCreateRule={(row) => setCreating({ kind: 'rule', txn: row })}
          onCreateSeries={(row) => setCreating({ kind: 'series', txn: row })}
          onSetReviewed={(row, next) => reviewRow({ id: row.id, reviewed: next })}
          onToggleExclusion={(row, field, next) =>
            editRow({
              id: row.id,
              patch:
                field === 'reports'
                  ? { excluded_from_reports: next }
                  : { excluded_from_spending_plan: next },
              optimistic:
                field === 'reports'
                  ? { excluded_from_reports: next }
                  : { excluded_from_spending_plan: next },
            })
          }
          onLinkSeries={(row) => setLinking(row)}
          onUnlinkSeries={(row) =>
            unlinkFromSeries.mutate(row.id, {
              onSuccess: () =>
                toast.show({
                  title: 'Unlinked from its recurring item',
                  description:
                    'The row stays. Its reminder is projected again in Bills & Income.',
                }),
            })
          }
          onToggleSubscription={(row, next) =>
            editRow({
              id: row.id,
              patch: { is_subscription: next },
              optimistic: { is_subscription: next },
            })
          }
          onToggleBill={(row, next) =>
            editRow({ id: row.id, patch: { is_bill: next }, optimistic: { is_bill: next } })
          }
          canBeARefund={canBeARefund(txn, categoryKind)}
          onLinkRefund={(row) => setRefunding(row)}
          onMerchantOrder={(row) => setPickingFor(row)}
          onSuggestCategory={suggestRow}
          onEditTags={(row, mode) => setTagging({ mode, ids: menuTargets(selected, row.id) })}
          tagTargetCount={menuTargets(selected, txn.id).length}
        />
      )}
      empty={
        filter.error === null && noAccounts ? (
          <EmptyState
            icon={<Receipt size={20} />}
            title="No accounts yet"
            body="Connect a bank or import a statement."
            action={
              <span className="empty__actions">
                <Button asChild variant="primary">
                  <Link to={CONNECT_PATH}>Connect with SimpleFIN</Link>
                </Button>
                <Button asChild>
                  <Link to={IMPORT_PATH}>Import a file</Link>
                </Button>
              </span>
            }
          />
        ) : filter.error === null && !narrowed ? (
          <EmptyState
            icon={<Receipt size={20} />}
            title="No transactions here yet"
            body="They arrive from a bank sync, an import or a hand entry."
            action={
              <Button asChild>
                <Link to={IMPORT_PATH}>Import a file</Link>
              </Button>
            }
          />
        ) : filter.error === null ? (
          <EmptyState
            icon={<Receipt size={20} />}
            title="No transactions match"
            body="Nothing in this window matches the current search and filters."
            action={
              <Button variant="secondary" onClick={clearAll}>
                Clear all filters
              </Button>
            }
          />
        ) : (
          <EmptyState
            icon={<Receipt size={20} />}
            title="Could not apply this filter"
            body={`${describeApiError(filter.error)} No rows shown.`}
            action={
              <Button variant="secondary" onClick={clearAll}>
                Clear all filters
              </Button>
            }
          />
        )
      }
    />
  )

  return (
    <div className="register-page">
      <Tabs value={tab} onValueChange={(next) => setParams(withTab(params, next, accountTab))}>
        <PageHeader
          tabs={
            <TabsList>
              {REGISTER_TABS.map((one) => (
                <TabsTrigger key={one.id} value={one.id}>
                  {one.label}
                </TabsTrigger>
              ))}
            </TabsList>
          }
          actions={
            canCreate ? (
              <Button
                variant="primary"
                size="sm"
                onClick={() => {
                  setEditing(null)
                  setDialogOpen(true)
                  forgetRequestedEdit()
                }}
              >
                <Plus size={13} /> New transaction
              </Button>
            ) : null
          }
        />
      </Tabs>

      <SearchNotes limitations={parsed.limitations} errors={parsed.errors} />

      {account ? (
        <AccountHeader
          account={account}
          accounts={accounts.data ?? []}
        />
      ) : (
        <AllAccountsHeader accounts={scoped} title={nodeName(accountTree, displayNode)} />
      )}

      {/* Only over the register: the Spending and Income tabs are over a
          chosen window, and the projection and what is due next are about
          today. Over one account the reminders are in the projection's card. */}
      {tab === 'all' && account ? <ProjectedCashFlow account={account} /> : null}
      {tab === 'all' && !account ? <RemindersStrip accountIds={accountIds} /> : null}
      {tab === 'all' ? <DuplicatesStrip /> : null}

      {tab === 'all' ? null : (
        <Card>
          <AggregateView
            direction={tab === 'spending' ? 'spending' : 'income'}
            shape={shape}
            data={aggregate.data}
            from={dates.range.from}
            to={dates.range.to}
            groupBy={groupBy}
            under={groupBy === 'category' ? drillUnder(drill) : null}
            trail={crumbs}
            onGroupByChange={setGroupBy}
            onShapeChange={setShape}
            onDrill={(step) => {
              const taken = drill.some((one) => one.by === step.by && one.key === step.key)
              if (!taken) setDrill([...drill, step])
            }}
            onStep={(depth) => setDrill(drill.slice(0, depth))}
          />
        </Card>
      )}

      {latestBatch === null ? null : (
        <CategoryCheckStrip
          batch={latestBatch}
          onShowUndetermined={showUndetermined}
          onCancel={() => cancelBatch.mutate(latestBatch.id)}
          cancelling={cancelBatch.isPending}
          onDismiss={() => dismissBatch.mutate(latestBatch.id)}
        />
      )}

      {reviewMode && !narrow ? (
        <Callout icon={<CircleCheck size={14} />}>
          Review mode — click ✓ to mark a row reviewed. A row with a suggestion still opens, so
          it is approved or changed first.
        </Callout>
      ) : null}

      <Card
        flush
        title={tab === 'all' ? 'Transactions' : 'Transaction activity'}
        subtitle={labelForSelection(dates)}
        actions={
          <>
            {/* Hidden while the phone is selecting: the header bar is the only
                place bulk actions live then. */}
            {!selecting && draft.isReviewed === false ? (
              <Button
                variant="primary"
                size="sm"
                // The review facet is in the filter, so until the filter
                // exists the query names every row in the window.
                disabled={bulkReview.dialog.pending || (selected.size === 0 && filter.pending)}
                onClick={() => {
                  // Selected rows win over the query, and need no confirming;
                  // the whole query does.
                  if (selected.size > 0) {
                    for (const id of selected) reviewRow({ id, reviewed: true })
                    setSelected(new Set())
                    return
                  }
                  if (query.filterId !== null) bulkReview.ask(query)
                }}
              >
                {selected.size > 0
                  ? `Mark ${selected.size} as reviewed`
                  : `Mark all ${formatCount(summary?.count ?? 0)} as reviewed`}
              </Button>
            ) : null}
            {narrow ? null : (
              <Button
                variant={reviewMode ? 'primary' : 'secondary'}
                size="sm"
                aria-pressed={reviewMode}
                onClick={() => setReviewMode((on) => !on)}
              >
                <CircleCheck size={14} aria-hidden="true" /> Review mode
              </Button>
            )}
            {bulkInToolbar ? (
              <Button
                variant="secondary"
                size="sm"
                disabled={suggesting}
                aria-label={`Suggest categories for ${selected.size} selected ${
                  selected.size === 1 ? 'row' : 'rows'
                }`}
                onClick={suggestSelected}
              >
                <Sparkles size={14} aria-hidden="true" /> Suggest categories
              </Button>
            ) : null}
            {bulkInToolbar && moreThanSelected ? (
              <Button
                variant="ghost"
                size="sm"
                disabled={suggesting || filter.pending}
                onClick={suggestAllMatching}
              >
                For all {formatCount(matchingCount)} matching
              </Button>
            ) : null}
            <CustomizeColumns
              prefs={prefs}
              shown={shownColumns}
              only={tab === 'all' ? undefined : ACTIVITY_COLUMNS}
              onChange={updatePrefs}
            />
            <OverflowMenu
              label="Actions for the register"
              actions={[
                {
                  label: 'Export loaded rows to CSV',
                  icon: <Download size={14} />,
                  onSelect: () =>
                    downloadCsv('transactions.csv', registerToCsv(rows, accountName, categoryName)),
                },
                { label: 'Import transactions…', icon: <FileUp size={14} />, to: '/settings/accounts' },
                account !== null &&
                  tab !== accountTab && {
                    label: `Make ${registerTabLabel(tab)} this account’s default tab`,
                    icon: <PanelTop size={14} />,
                    disabled: updateAccount.isPending,
                    onSelect: () =>
                      updateAccount.mutate({
                        id: account.id,
                        patch: { default_register_tab: tab === 'all' ? null : tab },
                      }),
                  },
                // While the phone is selecting, the header bar holds the bulk
                // actions; the desktop keeps them here.
                bulkInToolbar && {
                  label: `Mark ${selected.size} selected ${selected.size === 1 ? 'row' : 'rows'} as ${
                    allSelectedReviewed ? 'unreviewed' : 'reviewed'
                  }`,
                  icon: <CircleCheck size={14} />,
                  onSelect: reviewSelected,
                },
                matchingCount > 0 && {
                  label: `Suggest categories for all ${formatCount(matchingCount)} matching…`,
                  icon: <Sparkles size={14} />,
                  disabled: suggesting || filter.pending,
                  onSelect: suggestAllMatching,
                },
                bulkInToolbar && {
                  label: `Add tags to ${selected.size} selected ${selected.size === 1 ? 'row' : 'rows'}…`,
                  icon: <Tag size={14} />,
                  onSelect: () => setTagging({ mode: 'add', ids: [...selected] }),
                },
                bulkInToolbar && {
                  label: `Count ${selected.size} selected ${
                    selected.size === 1 ? 'row' : 'rows'
                  } toward a goal…`,
                  icon: <Target size={14} />,
                  onSelect: () => setFilingUnderGoal(true),
                },
              ]}
            />
          </>
        }
      >
        <QuickFilters
          filter={
            <FilterPanel
              draft={panel}
              categories={categories.data ?? []}
              tags={tags.data ?? []}
              accounts={accounts.data ?? []}
              payees={payees}
              onApply={setPanel}
            />
          }
          narrowing={
            <DrillChips
              crumbs={crumbs}
              onRemove={(index) => setDrill(drill.filter((_, at) => at !== index))}
            />
          }
          dates={<DateRangeControl value={dates} onChange={setPicked} />}
          search={<SearchBox value={search} onChange={setSearch} />}
          summary={summary ?? null}
          carried={carried.data ?? null}
          quickFilters={quickFilters}
          activeId={activeQuick}
          waiting={draft.isReviewed === false ? (waiting.data?.length ?? 0) : 0}
          padding={padding}
          onPadding={setPadding}
          onChoose={chooseQuick}
          onCreate={() => setSavingQuick(true)}
          onManage={() => setManagingQuick(true)}
          onClearAll={clearAll}
        />

        {grid}
      </Card>

      <TransactionDialog
        open={dialogOpen || linkedRow !== null}
        onOpenChange={(next) => (next ? setDialogOpen(true) : closeDetail())}
        transaction={current}
        accounts={accounts.data ?? []}
        categories={categories.data ?? []}
        tags={tags.data ?? []}
        frequentCategoryIds={frequentCategoryIds}
        defaultAccountId={account?.id ?? null}
        spaceCurrency={space?.primary_currency ?? null}
        saving={edit.isPending || create.isPending}
        onCreate={(body) => {
          create.mutate(body)
          closeDetail()
        }}
        onUpdate={(id, patch, optimistic) => {
          edit.mutate({ id, patch, optimistic })
          closeDetail()
        }}
        onDelete={(id) => {
          remove.ask(id)
          closeDetail()
        }}
        onInvalid={(message) => toast.show({ title: 'Check that value', description: message })}
        onCreateRule={(row) => {
          closeDetail()
          setCreating({ kind: 'rule', txn: row })
        }}
        onCreateSeries={(row) => {
          closeDetail()
          setCreating({ kind: 'series', txn: row })
        }}
        onTrackRefund={(row) => {
          closeDetail()
          setCreating({ kind: 'refund', txn: row })
        }}
        onOpenRow={openRow}
        onMerchantOrder={(row) => {
          closeDetail()
          setPickingFor(row)
        }}
        onSuggestCategory={suggestRow}
        suggestingCategory={editing !== null && checks.pending.has(editing.id)}
        review={
          current?.suggestion
            ? {
                suggestion: current.suggestion,
                busy: deciding,
                onApprove: (splitOverrides) => decideSuggestion(current, undefined, splitOverrides),
                onUseMine: (categoryId) => decideSuggestion(current, categoryId),
                onDiscard: () => dropSuggestion(current),
                onShowRun: setViewingRun,
              }
            : null
        }
      />

      <RunDialog
        runId={viewingRun}
        status={assistant.data ?? null}
        onClose={() => setViewingRun(null)}
      />

      <CreateFromTransaction
        target={creating}
        onClose={() => setCreating(null)}
        accounts={accounts.data ?? []}
        categories={categories.data ?? []}
        tags={tags.data ?? []}
        onCreated={(kind) =>
          toast.show(
            kind === 'rule'
              ? { title: 'Rule created' }
              : kind === 'refund'
                ? {
                    title: 'Refund tracked',
                    description: 'The credit will claim it when it posts.',
                    action: {
                      label: 'Open the refund tracker',
                      onSelect: () => navigate('/upcoming/refund-tracker'),
                    },
                  }
                : {
                    title: 'Recurring item created',
                    description: 'It is now projected in Bills & Income.',
                    action: { label: 'Open Recurring', onSelect: () => navigate('/upcoming/recurring') },
                  },
          )
        }
      />

      <PurchaseDialog txn={pickingFor} onClose={() => setPickingFor(null)} />

      {tagging !== null ? (
        <TagRowsDialog
          mode={tagging.mode}
          ids={tagging.ids}
          tags={tags.data ?? []}
          onClose={() => setTagging(null)}
          onDone={() => setSelected(new Set())}
        />
      ) : null}

      <BulkGoalDialog
        ids={filingUnderGoal ? selected : EMPTY_SELECTION}
        onClose={() => setFilingUnderGoal(false)}
        onDone={() => setSelected(new Set())}
      />

      <LinkSeriesDialog
        txn={linking}
        accounts={accounts.data ?? []}
        onClose={() => setLinking(null)}
        onLink={(row, series, dueOn) => {
          linkToSeries.mutate(
            { id: row.id, seriesId: series.id, dueOn },
            {
              onSuccess: (updated) =>
                toast.show({
                  title: 'Linked to the recurring item',
                  description: `Filed under ${series.label}, due ${updated.series_due_on ?? 'this occurrence'}.`,
                }),
            },
          )
          setLinking(null)
        }}
        onCreateInstead={(row) => {
          setLinking(null)
          setCreating({ kind: 'series', txn: row })
        }}
      />

      <LinkRefundDialog
        txn={refunding}
        onClose={() => setRefunding(null)}
        onLink={(refundId, chargeId) => {
          linkRefund.mutate(
            { refundId, chargeId },
            {
              onSuccess: (links) =>
                toast.show({
                  title: 'Filed as a refund',
                  description: `This credit comes off ${
                    links.refunds[0]?.category_name ?? 'the charge'
                  } rather than counting as income.`,
                }),
            },
          )
          setRefunding(null)
        }}
      />

      {suggestions.dialog}

      <ConfirmDialog
        {...bulkReview.dialog}
        title={`Mark all ${formatCount(summary?.count ?? 0)} as reviewed?`}
        description="Marks every row this filter matches, not just those on screen. Categories and amounts are unchanged. This cannot be undone."
        confirmLabel={
          bulkReview.dialog.pending
            ? 'Marking…'
            : `Mark ${formatCount(summary?.count ?? 0)} as reviewed`
        }
      />

      {savingQuick ? (
        <QuickFilterDialog
          title="New custom filter"
          submitLabel="Save custom filter"
          initial={{ name: '', panel, search, dates: null }}
          window={dates}
          universe={universe}
          accounts={accounts.data ?? []}
          payees={payees}
          pending={createQuick.isPending}
          onSubmit={(body) =>
            createQuick.mutateAsync({
              ...body,
              position: Math.max(-1, ...(savedQuick.data ?? []).map((row) => row.position ?? 0)) + 1,
            })
          }
          onClose={() => setSavingQuick(false)}
        />
      ) : null}

      {managingQuick ? (
        <ManageQuickFiltersDialog
          rows={savedQuick.data ?? []}
          window={dates}
          today={today}
          universe={universe}
          accounts={accounts.data ?? []}
          payees={payees}
          onClose={() => setManagingQuick(false)}
        />
      ) : null}

      <ConfirmDialog
        {...remove.dialog}
        title="Delete this transaction?"
        description="Any transfer partner is unpaired and kept."
        confirmLabel="Delete transaction"
      />
    </div>
  )
}

/** Categories by how often they appear in the rows on screen. */
function rankCategories(rows: readonly Transaction[]): Uuid[] {
  const counts = new Map<Uuid, number>()
  for (const row of rows) {
    for (const id of new Set(categoryIds(row))) counts.set(id, (counts.get(id) ?? 0) + 1)
  }
  return [...counts.entries()].sort((a, b) => b[1] - a[1]).map(([id]) => id)
}

/** The fallback while `/transactions/payees` is still loading. */
function distinctPayees(rows: readonly Transaction[]): string[] {
  const names = new Set<string>()
  for (const row of rows) {
    const name = displayPayee(row)
    if (name) names.add(name)
  }
  return [...names].sort((a, b) => a.localeCompare(b))
}
