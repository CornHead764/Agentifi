import { Plus, X } from 'lucide-react'

import { Money } from '@/components/Money'
import {
  Button,
  IconButton,
  RowActions,
  Table,
  TableEmptyRow,
  Td,
  Th,
} from '@/components/ui'
import {
  type Suggestion,
  dismissSuggestion,
  invalidateOccurrenceWrites,
  promoteSuggestion,
  restoreSuggestion,
  seriesKeys,
} from '@/lib/clients/upcoming'
import { formatDate, formatPercent } from '@/lib/format'
import { useInvalidatingMutation } from '@/lib/queryClient'
import { shortLabel } from '@/lib/recurrence'
import { matchesSearch } from '@/lib/search'

/** Detected patterns. A promote writes a real series; nothing is auto-created. */
export function SuggestionTable({
  rows,
  search,
  dismissed,
}: {
  rows: readonly Suggestion[]
  search: string
  /** The waved-away view: the one action is to put a pattern back in play. */
  dismissed: boolean
}) {
  const promote = useInvalidatingMutation(promoteSuggestion, invalidateOccurrenceWrites, {
    failure: 'That suggestion was not made recurring',
  })
  // The key prefix both views read; a change to either pile that does not
  // invalidate it leaves the row on screen until the tab is left and re-entered.
  const dismiss = useInvalidatingMutation(dismissSuggestion, [seriesKeys.suggested], {
    failure: 'That suggestion was not dismissed',
  })
  const restore = useInvalidatingMutation(restoreSuggestion, [seriesKeys.suggested], {
    failure: 'That suggestion was not restored',
  })

  const shown = rows.filter((one) => matchesSearch(search, one.label))

  if (shown.length === 0) {
    return (
      <Table lines={2}>
        <tbody>
          <TableEmptyRow colSpan={5}>
            {dismissed
              ? 'Nothing dismissed is still repeating.'
              : 'No repeating patterns detected that are not already recurring.'}
          </TableEmptyRow>
        </tbody>
      </Table>
    )
  }

  return (
    <Table lines={2} stack>
      <thead>
        <tr>
          <Th>Pattern</Th>
          <Th>Seen</Th>
          <Th numeric>Per occurrence</Th>
          <Th numeric>Confidence</Th>
          <Th />
        </tr>
      </thead>
      <tbody>
        {shown.map((one) => (
          <tr key={one.signature}>
            <Td label="" className="series__lead">
              <span className="series__name">
                <span className="series__label">{one.label}</span>
              </span>
              <span className="cell__sub">{shortLabel(one.recurrence)}</span>
            </Td>
            <Td className="stack-inline nowrap">
              <span>{one.occurrences}×</span>
              <span className="cell__sub">
                {formatDate(one.first_seen, 'short')} – {formatDate(one.last_seen, 'short')}
              </span>
            </Td>
            <Td numeric className="stack-inline nowrap">
              <Money value={one.amount} showPlus={one.amount > 0} />
            </Td>
            <Td numeric className="stack-inline nowrap">
              {formatPercent(one.confidence, { digits: 0 })}
            </Td>
            <Td numeric label="" className="stack-corner">
              <RowActions>
                {dismissed ? (
                  <Button
                    size="sm"
                    disabled={restore.isPending}
                    onClick={() => restore.mutate(one.signature)}
                  >
                    {restore.isPending ? 'Restoring…' : 'Restore'}
                  </Button>
                ) : (
                  <>
                    <IconButton
                      label={`Not recurring: ${one.label}`}
                      size="sm"
                      disabled={dismiss.isPending}
                      onClick={() => dismiss.mutate(one.signature)}
                    >
                      <X size={13} />
                    </IconButton>
                    <IconButton
                      label={`Add ${one.label} as a series`}
                      variant="primary"
                      size="sm"
                      disabled={promote.isPending}
                      onClick={() => promote.mutate(one)}
                    >
                      <Plus size={13} />
                    </IconButton>
                  </>
                )}
              </RowActions>
            </Td>
          </tr>
        ))}
      </tbody>
    </Table>
  )
}
