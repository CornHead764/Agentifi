import { ArrowRight, Link2Off, Repeat } from 'lucide-react'

import { Money } from '@/components/Money'
import {
  Badge,
  Button,
  Card,
  EmptyState,
  IconButton,
  PageHeader,
  RowActions,
  SkeletonRows,
  Table,
  Td,
  Th,
  useProgressToast,
  useToast,
  WarningMark,
} from '@/components/ui'
import { formatDate, plural } from '@/lib/format'
import {
  useOrphanLegs,
  useDetectTransfers,
  usePairByHand,
  useRepairOrphanLegs,
  useTransfers,
  useUnpairTransfer,
  type Transfer,
  type TransferLeg,
} from '@/lib/clients/transfers'
import { PairByHandDialog } from './transfers/PairByHandDialog'

/**
 * Transfer activity, and the repair job for pairs that broke.
 *
 * A leg keeps its token when its partner is deleted, and is then excluded from
 * profit and loss and can never re-match; the orphan card releases them.
 *
 * Dates are the posted ones, not the effective date: the two legs post
 * together while their effective dates can be a statement cycle apart.
 */
export function TransfersSettings() {
  const { show } = useToast()
  const progress = useProgressToast()

  const transfers = useTransfers()
  const pair = usePairByHand()
  const detect = useDetectTransfers()

  const rows = transfers.data?.transfers ?? []
  const orphanCount = transfers.data?.orphan_count ?? 0

  return (
    <>
      <PageHeader
        title="Transfer activity"
        subtitle="Money moved between your own accounts. Both halves are left out of income and expense."
        actions={
          <>
            {/* The sync pairs only what it just wrote, so an imported history
                has never been looked at. This is the only way to ask. */}
            <Button
              size="sm"
              disabled={detect.isPending}
              onClick={() =>
                void progress(
                  'Looking for transfers…',
                  detect.mutateAsync(undefined),
                  (found) => ({
                    title:
                      found.paired === 0
                        ? 'No new transfers found.'
                        : `Paired ${plural(found.paired, 'transfer')}.`,
                  }),
                  'Transfers were not looked for',
                )
              }
            >
              {detect.isPending ? 'Looking…' : 'Find transfers'}
            </Button>
            <PairByHandDialog
              pending={pair.isPending}
              onPair={async (payingId, receivingId) => {
                await pair.mutateAsync({ payingId, receivingId })
                show({
                  title: 'Paired',
                  description: 'Neither transaction now counts as income or expense.',
                })
              }}
            />
          </>
        }
      />

      {orphanCount > 0 ? <OrphanCard count={orphanCount} /> : null}

      <Card title="Paired transfers" flush={rows.length > 0}>
        {transfers.isPending ? <SkeletonRows rows={4} /> : null}
        {transfers.isSuccess && rows.length === 0 ? (
          <EmptyState
            icon={<Repeat size={20} />}
            title="No transfers yet"
            body="A payment out that matches a deposit into another account is paired here. Nothing has matched yet."
          />
        ) : null}
        {rows.length > 0 ? <TransferTable transfers={rows} /> : null}
      </Card>
    </>
  )
}

function TransferTable({
  transfers,
}: {
  transfers: Transfer[]
}) {
  const unpair = useUnpairTransfer()
  const { show } = useToast()

  const release = (transfer: Transfer) =>
    unpair.mutate(transfer.pair_id, {
      onSuccess: () =>
        show({
          title: 'Unpaired',
          description: 'Both transactions are back in the register on their own.',
        }),
    })

  return (
    <Table stack="tablet" lines={2}>
      <thead>
        <tr>
          <Th>Moved</Th>
          <Th>From</Th>
          <Th>To</Th>
          <Th numeric>Amount</Th>
          <Th>Paired</Th>
          <Th />
        </tr>
      </thead>
      <tbody>
        {transfers.map((transfer) => (
          <tr key={transfer.pair_id}>
            <Td className="nowrap" label="Moved">
              {formatDate(transfer.moved_on)}
            </Td>
            <Td label="From">
              <LegCell leg={transfer.from} />
            </Td>
            <Td label="To">
              <LegCell leg={transfer.to} arrow />
            </Td>
            <Td numeric label="Amount">
              <Money value={transfer.amount} currency={transfer.currency} tone="neutral" />
            </Td>
            <Td label="Paired">
              {transfer.paired_by_hand ? (
                <Badge tone="accent">By hand</Badge>
              ) : (
                <Badge>Automatically</Badge>
              )}
            </Td>
            <Td label="">
              <RowActions>
                <IconButton
                  label="Unpair"
                  title="Unpair"
                  size="sm"
                  variant="ghost"
                  disabled={unpair.isPending}
                  onClick={() => release(transfer)}
                >
                  <Link2Off size={14} />
                </IconButton>
              </RowActions>
            </Td>
          </tr>
        ))}
      </tbody>
    </Table>
  )
}

/**
 * The account, with the leg's own posted date under it.
 * One flex row, so the arrow cannot wrap onto a line of its own.
 */
function LegCell({ leg, arrow = false }: { leg: TransferLeg; arrow?: boolean }) {
  return (
    <span className="transfer-leg">
      {arrow ? (
        <ArrowRight size={12} className="transfer-leg__arrow" aria-hidden="true" />
      ) : null}
      <span className="transfer-leg__text">
        <span className="cell__clip" title={leg.account_name}>
          {leg.account_name}
        </span>
        <span className="cell__sub">{formatDate(leg.date, 'short')}</span>
      </span>
    </span>
  )
}

/** Legs whose partner is gone, shown only when there are some. */
function OrphanCard({ count }: { count: number }) {
  const orphans = useOrphanLegs(true)
  const repair = useRepairOrphanLegs()
  const { show } = useToast()

  const legs = orphans.data?.orphans ?? []

  const run = () =>
    repair.mutate(undefined, {
      onSuccess: (result) =>
        show({
          title: `Released ${plural(result.orphans.length, 'transaction')}. They count as income and expense again.`,
        }),
    })

  return (
    <Card
      title={
        <>
          <WarningMark
            text={`${plural(count, 'transaction')} lost the other half of ${count === 1 ? 'its' : 'their'} transfer, so ${count === 1 ? 'it is' : 'they are'} left out of every report until released.`}
          />{' '}
          {plural(count, 'half-paired transfer')}
        </>
      }
      subtitle="Half of a transfer whose other half is gone. Until released, they are left out of every report and cannot be matched again."
      actions={
        <Button variant="primary" size="sm" disabled={repair.isPending} onClick={run}>
          {repair.isPending ? 'Releasing…' : 'Release them'}
        </Button>
      }
      flush={legs.length > 0}
    >
      {orphans.isPending ? <SkeletonRows rows={2} /> : null}
      {legs.length > 0 ? (
        <Table stack>
          <thead>
            <tr>
              <Th>Date</Th>
              <Th>Account</Th>
              <Th>Payee</Th>
              <Th numeric>Amount</Th>
            </tr>
          </thead>
          <tbody>
            {legs.map((leg) => (
              <tr key={leg.transaction_id}>
                <Td className="nowrap" label="Date">
                  {formatDate(leg.date)}
                </Td>
                <Td label="Account">{leg.account_name}</Td>
                <Td label="">{leg.payee || 'No payee'}</Td>
                <Td numeric label="Amount">
                  <Money value={leg.amount} currency={leg.currency} />
                </Td>
              </tr>
            ))}
          </tbody>
        </Table>
      ) : null}
    </Card>
  )
}
