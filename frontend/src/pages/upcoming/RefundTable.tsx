import { Check, Pencil, Trash2 } from 'lucide-react'

import { Money } from '@/components/Money'
import {
  ConfirmDialog,
  OverflowMenu,
  RowActions,
  Table,
  TableEmptyRow,
  Td,
  Th,
  useConfirm,
} from '@/components/ui'
import {
  invalidateOccurrenceWrites,
  settleRefund,
  useDeleteSeries,
  type Refund,
  type Series,
} from '@/lib/clients/upcoming'
import { EM_DASH, formatDate } from '@/lib/format'
import { useInvalidatingMutation } from '@/lib/queryClient'
import { useAccounts, useCategories } from '@/lib/transactions/queries'
import { AskMenuItem } from '@/components/assistant/AskMenuItem'
import { seriesSubject } from '@/lib/assistant/subjects'

export function RefundTable({
  rows,
  empty,
  onEdit,
}: {
  rows: readonly Refund[]
  empty: string
  onEdit: (series: Series) => void
}) {
  if (rows.length === 0) {
    return (
      <Table lines={2}>
        <tbody>
          <TableEmptyRow colSpan={5}>{empty}</TableEmptyRow>
        </tbody>
      </Table>
    )
  }

  return (
    <Table lines={2} stack>
      <thead>
        <tr>
          <Th>Refund</Th>
          <Th>Account</Th>
          <Th>Completed</Th>
          <Th numeric>Amount</Th>
          <Th />
        </tr>
      </thead>
      <tbody>
        {rows.map((refund) => (
          <RefundRow key={refund.series.id} refund={refund} onEdit={onEdit} />
        ))}
      </tbody>
    </Table>
  )
}

function RefundRow({ refund, onEdit }: { refund: Refund; onEdit: (series: Series) => void }) {
  const accounts = useAccounts()
  const categories = useCategories()

  const account = (accounts.data ?? []).find((one) => one.id === refund.series.account_id)
  const category = (categories.data ?? []).find((one) => one.id === refund.series.category_id)

  const settle = useInvalidatingMutation(
    () => settleRefund(refund),
    invalidateOccurrenceWrites,
  )
  const remove = useConfirm(useDeleteSeries('That refund was not removed'))
  const settled = refund.settled_on !== null

  return (
    <tr>
      <Td label="" className="series__lead">
        <span className="series__name">
          <span className="series__label">{refund.series.label}</span>
        </span>
        <span className="cell__sub">Expected {formatDate(refund.expected_on)}</span>
      </Td>
      <Td className="stack-inline nowrap">
        <span>{account?.name ?? EM_DASH}</span>
        <span className="cell__sub">{category?.name ?? 'Uncategorized'}</span>
      </Td>
      <Td className="stack-inline nowrap">
        {refund.settled_on ? formatDate(refund.settled_on) : <span className="muted">{EM_DASH}</span>}
      </Td>
      <Td numeric className="stack-inline nowrap">
        <Money value={refund.series.amount} showPlus />
      </Td>
      <Td numeric label="" className="stack-corner">
        <RowActions>
          <OverflowMenu
            label={`Actions for ${refund.series.label}`}
            actions={[
              // A settled refund has a credit in its slot already; accepting a
              // second would put two into the register for one return.
              {
                label: 'Mark received',
                icon: <Check size={14} />,
                disabled: settled || settle.isPending,
                onSelect: () => settle.mutate(),
              },
              { label: 'Edit refund', icon: <Pencil size={14} />, onSelect: () => onEdit(refund.series) },
              <AskMenuItem subject={() => seriesSubject(refund.series)} />,
              {
                label: 'Stop tracking',
                icon: <Trash2 size={14} />,
                danger: true,
                disabled: remove.dialog.pending,
                onSelect: () => remove.ask(refund.series.id),
              },
            ]}
          />
        </RowActions>
        <ConfirmDialog
          {...remove.dialog}
          title={`Stop tracking ${refund.series.label}?`}
          description="Deletes the expected credit and its open slots. Register transactions are untouched."
          confirmLabel="Stop tracking"
        />
      </Td>
    </tr>
  )
}
