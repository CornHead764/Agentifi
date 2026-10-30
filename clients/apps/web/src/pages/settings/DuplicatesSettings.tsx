import { CopyCheck } from 'lucide-react'

import { Money } from '@/components/Money'
import {
  Badge,
  Button,
  Card,
  EmptyState,
  PageHeader,
  RowActions,
  SkeletonRows,
  Table,
  Td,
  Th,
  useProgressToast,
  useToast,
} from '@/components/ui'
import {
  useDecideDuplicate,
  useDuplicates,
  useScanDuplicates,
  type DuplicatePair,
  type DuplicateRow,
} from '@/lib/clients/duplicates'
import { formatDate, plural } from '@/lib/format'
import type { TransactionSource } from '@/lib/transactions/types'

const SOURCE_LABELS: Record<TransactionSource, string> = {
  sync: 'Bank sync',
  simplifi_import: 'Simplifi import',
  file_import: 'File import',
  manual: 'Entered by hand',
  opening_balance: 'Opening balance',
  balance_adjustment: 'Adjustment',
}

/**
 * Charges that look recorded twice, a pair at a time, side by side.
 *
 * The usual cause is a Simplifi import followed by a SimpleFIN link: the bank
 * posts a charge a day after Simplifi dated it, under its own wording, and
 * both land. A pair is only a guess, so nothing is removed until a copy is
 * chosen to keep, and "Two transactions" is remembered so the pair is not
 * raised again.
 *
 * Dates are the posted ones.
 */
export function DuplicatesSettings() {
  const progress = useProgressToast()
  const duplicates = useDuplicates()
  const scan = useScanDuplicates()

  const pairs = duplicates.data?.pairs ?? []

  return (
    <>
      <PageHeader
        title="Possible duplicates"
        subtitle="The same amount, on the same account, within two days of each other, from different sources. Decide which are one charge recorded twice."
        actions={
          // New pairs are looked for after every sync and import; this checks
          // the whole history, for rows that were there before the check was.
          <Button
            size="sm"
            disabled={scan.isPending}
            onClick={() =>
              void progress(
                'Looking for duplicates…',
                scan.mutateAsync(undefined),
                (result) => ({
                  title:
                    result.found === 0
                      ? 'No new pairs found.'
                      : `Found ${plural(result.found, 'new pair')}.`,
                }),
                'Duplicates were not looked for',
              )
            }
          >
            {scan.isPending ? 'Looking…' : 'Look again'}
          </Button>
        }
      />

      {duplicates.isPending ? (
        <Card>
          <SkeletonRows rows={3} />
        </Card>
      ) : null}
      {duplicates.isSuccess && pairs.length === 0 ? (
        <Card>
          <EmptyState
            icon={<CopyCheck size={20} />}
            title="Nothing to check"
            body="No two transactions from different sources share an amount, an account and a day or two. This list fills after a sync or an import."
          />
        </Card>
      ) : null}
      {pairs.map((pair) => (
        <PairCard key={pair.id} pair={pair} />
      ))}
    </>
  )
}

function PairCard({ pair }: { pair: DuplicatePair }) {
  const decide = useDecideDuplicate()
  const { show } = useToast()

  const keep = (row: DuplicateRow) =>
    decide.mutate(
      { pairId: pair.id, verdict: 'duplicate', keepId: row.id },
      {
        onSuccess: () =>
          show({
            title: 'Removed the other copy',
            description: 'The one you kept is the only record of this charge now.',
          }),
      },
    )
  const distinct = () =>
    decide.mutate(
      { pairId: pair.id, verdict: 'distinct' },
      {
        onSuccess: () =>
          show({ title: 'Kept both', description: 'This pair will not be raised again.' }),
      },
    )

  return (
    <Card
      flush
      title={pair.account_name}
      subtitle={
        pair.days_apart === 0
          ? 'Dated the same day'
          : `Dated ${plural(pair.days_apart, 'day')} apart`
      }
      actions={
        <Button size="sm" disabled={decide.isPending} onClick={distinct}>
          Two transactions
        </Button>
      }
    >
      <Table stack="tablet" lines={2}>
        <thead>
          <tr>
            <Th>Source</Th>
            <Th>Date</Th>
            <Th>Payee</Th>
            <Th>Category</Th>
            <Th numeric>Amount</Th>
            <Th />
          </tr>
        </thead>
        <tbody>
          {[pair.first, pair.second].map((row) => (
            <tr key={row.id}>
              <Td label="Source">
                <Badge tone={row.source === 'sync' ? 'neutral' : 'accent'}>
                  {SOURCE_LABELS[row.source]}
                </Badge>
              </Td>
              <Td className="nowrap" label="Date">
                {formatDate(row.date)}
                {row.is_pending ? <span className="cell__sub">Pending</span> : null}
              </Td>
              <Td label="Payee">
                <span className="cell__clip" title={row.payee}>
                  {row.payee || 'No payee'}
                </span>
                {row.statement_name && row.statement_name !== row.payee ? (
                  <span className="cell__sub cell__clip" title={row.statement_name}>
                    {row.statement_name}
                  </span>
                ) : null}
              </Td>
              <Td label="Category">
                {row.category_name ?? <span className="muted">Uncategorized</span>}
                {row.is_transfer_leg ? <span className="cell__sub">Half of a transfer</span> : null}
              </Td>
              <Td numeric label="Amount">
                <Money value={row.amount} currency={row.currency} />
              </Td>
              <Td label="">
                <RowActions>
                  <Button
                    size="sm"
                    variant={row.id === pair.suggested_keep_id ? 'primary' : 'secondary'}
                    disabled={decide.isPending}
                    onClick={() => keep(row)}
                  >
                    Keep this one
                  </Button>
                </RowActions>
              </Td>
            </tr>
          ))}
        </tbody>
      </Table>
    </Card>
  )
}
