/**
 * Bills & Income. An occurrence is `series_id` + `due_on`, never a series
 * alone, or a twice-monthly bill's slots are both settled by one payment. A
 * series' `description` is matching input; display reads `label`.
 */

export {
  autopayNote,
  billerPaidNote,
  billLinkMark,
  type CashFlowLine,
  dueNote,
  isPayManually,
  reminderAccount,
  type MatchCriteria,
  type Occurrence,
  type OccurrenceBillLink,
  occurrenceKey,
  type OccurrenceList,
  occurrenceMarkers,
  type OccurrenceStatus,
  type Refund,
  type Series,
  SERIES_KIND_LABELS,
  SERIES_TAB_LABELS,
  SERIES_TABS,
  type SeriesHistory,
  type SeriesHistoryRow,
  type SeriesKind,
  type SeriesTab,
  type Suggestion,
} from './upcoming/types'

export {
  type AcceptEdits,
  acceptOccurrence,
  acceptSlot,
  deleteSeries,
  dismissSuggestion,
  promoteSuggestion,
  restoreSuggestion,
  settleRefund,
  skipOccurrence,
  skipSlot,
  slotWindow,
  suggestSeriesFor,
} from './upcoming/api'

export {
  useAcceptOccurrence,
  useAllSeries,
  useCashFlow,
  useCreateSeries,
  useDeleteSeries,
  useOccurrences,
  useRefunds,
  useSeriesHistory,
  useSeriesList,
  useSetSeriesActive,
  useSkipOccurrence,
  useSuggestions,
  useUpdateSeries,
} from './upcoming/hooks'

export {
  invalidateOccurrenceWrites,
  seriesKeys,
} from './upcoming/keys'

export {
  draftFromEdits,
  editsFromSeries,
  editsFromSuggestion,
  patchFromEdits,
  seriesAmountErrors,
  type SeriesEdits,
  type SuggestedSeries,
  splitParent,
  splitsBalance,
  startSplitting,
} from './upcoming/drafts'
