import { ArrowDown, ArrowUp, Pencil, Trash2 } from 'lucide-react'
import type { ReactNode } from 'react'

import { QueryBoundary, type QueryLike } from '@/components/QueryBoundary'

import {
  Badge,
  IconButton,
  type OverflowAction,
  OverflowMenu,
  RowActions,
  Switch,
  Table,
  Td,
  Th,
} from '@/components/ui'
import type { RuleCondition } from '@/lib/clients/rules'
import { capitalize } from '@/lib/format'
import type { FilterUniverse } from '@/lib/transactions/filter'

import { describeConditions, readConditions } from './conditions'

import { AskMenuItem } from '@/components/assistant/AskMenuItem'
import { guidanceSubject, ruleSubject } from '@/lib/assistant/subjects'

/**
 * The list both tabs of the Rules screen are drawn with: a rule and a guidance
 * note are both a filter and a consequence. The filter summary is
 * `describeConditions`, the one rendering of the one `Filter` (ground rule 3).
 *
 * **The order is the answer to "which one wins".** Rules run top to bottom and
 * the first to set a field keeps it; guidance notes about the same payee
 * arrive as one paragraph in this order. So rows carry move controls rather
 * than sorting by name.
 */
export interface RuleListEntry {
  id: string
  name: string
  /** The filter's own clauses — the same wire shape for a rule and a note. */
  items: readonly RuleCondition[]
  /** What happens once it matches: chips for a rule, the words for a note. */
  then: ReactNode
  active: boolean
  /** Menu items between Edit and Delete. A rule's review step is the only one. */
  menu?: readonly OverflowAction[]
}

export interface RuleListProps {
  /** The word every label and prompt on these rows uses: "rule", "guidance". */
  noun: string
  /**
   * The resource, for the assistant. `noun` is display wording; the paths the
   * model is pointed at depend on this.
   */
  askKind: 'rule' | 'guidance'
  /** The heading over the `then` column. */
  thenHeading: string
  entries: readonly RuleListEntry[]
  /** What a stored facet's ids are read against; see `readConditions`. */
  universe: FilterUniverse
  /** The list's own query: pending and failed are drawn from it, never as an empty list. */
  query: QueryLike<unknown>
  /** Drawn in place of the table while there is nothing to list. */
  empty?: ReactNode
  /** Two when the `then` column is prose clamped to two lines. */
  lines?: 1 | 2
  /** A reorder is in flight, so the move controls wait for it to land. */
  reordering?: boolean
  /** The row a link pointed at, marked so it can be found. */
  linkedId?: string | null
  onMove: (index: number, by: number) => void
  onEdit: (id: string) => void
  onDelete: (id: string) => void
  onActiveChange: (id: string, active: boolean) => void
}

export function RuleList({
  noun,
  askKind,
  thenHeading,
  entries,
  universe,
  query,
  empty,
  lines = 1,
  reordering = false,
  linkedId = null,
  onMove,
  onEdit,
  onDelete,
  onActiveChange,
}: RuleListProps) {
  const Noun = capitalize(noun)

  return (
    <QueryBoundary
      query={query}
      rows={3}
      empty={() => (entries.length === 0 ? <>{empty}</> : undefined)}
    >
      {() => (
        <Table density="sm" lines={lines} className="rule-list">
          <thead>
            <tr>
              <Th>Order</Th>
              <Th>{Noun}</Th>
              <Th>If a transaction matches</Th>
              <Th>{thenHeading}</Th>
              <Th>Active</Th>
              <Th aria-label="Actions" />
            </tr>
          </thead>
          <tbody>
            {entries.map((entry, index) => (
              <tr
                key={entry.id}
                className={entry.active ? undefined : 'rule-row--off'}
                data-linked={entry.id === linkedId ? 'true' : undefined}
              >
                <Td>
                  <span className="rule-order">
                    <span className="rule-order__number">{index + 1}</span>
                    <IconButton
                      label={`Move ${entry.name} earlier`}
                      size="sm"
                      disabled={index === 0 || reordering}
                      onClick={() => onMove(index, -1)}
                    >
                      <ArrowUp size={13} />
                    </IconButton>
                    <IconButton
                      label={`Move ${entry.name} later`}
                      size="sm"
                      disabled={index === entries.length - 1 || reordering}
                      onClick={() => onMove(index, 1)}
                    >
                      <ArrowDown size={13} />
                    </IconButton>
                  </span>
                </Td>
                <Td>
                  <span className="rule-name" title={entry.name}>
                    {entry.name}
                  </span>
                </Td>
                <Td>
                  <ChipLine texts={describeConditions(readConditions(entry.items, universe))} />
                </Td>
                <Td>{entry.then}</Td>
                <Td>
                  <Switch
                    aria-label={`${entry.active ? 'Deactivate' : 'Activate'} ${entry.name}`}
                    checked={entry.active}
                    onCheckedChange={(checked) => onActiveChange(entry.id, checked)}
                  />
                </Td>
                <Td numeric>
                  <RowActions>
                    <OverflowMenu
                      label={`Actions for ${entry.name}`}
                      actions={[
                        { label: `Edit ${noun}`, icon: <Pencil size={14} />, onSelect: () => onEdit(entry.id) },
                        ...(entry.menu ?? []),
                        <AskMenuItem
                          subject={() =>
                            askKind === 'guidance'
                              ? guidanceSubject({
                                  id: entry.id,
                                  name: entry.name,
                                })
                              : ruleSubject({ id: entry.id, name: entry.name })
                          }
                        />,
                        {
                          label: `Delete ${noun}`,
                          icon: <Trash2 size={14} />,
                          danger: true,
                          onSelect: () => onDelete(entry.id),
                        },
                      ]}
                    />
                  </RowActions>
                </Td>
              </tr>
            ))}
          </tbody>
        </Table>
      )}
    </QueryBoundary>
  )
}

/**
 * Chips on one line. When they do not fit, each is cut with an ellipsis rather
 * than wrapping the row taller; the tooltip names them all.
 */
export function ChipLine({ texts }: { texts: readonly string[] }) {
  return (
    <span className="chips chips--wrap rule-chips" title={texts.join(' · ')}>
      {texts.map((text) => (
        <Badge key={text} className="rule-chip">
          {text}
        </Badge>
      ))}
    </span>
  )
}
