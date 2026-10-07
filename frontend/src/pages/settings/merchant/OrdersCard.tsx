import { Link2, Package, Sparkles } from 'lucide-react'
import { useState } from 'react'
import { useNavigate } from 'react-router-dom'

import {
  Button,
  Card,
  ConfirmDialog,
  EmptyState,
  SkeletonRows,
  Table,
  Th,
  useConfirm,
  useToast,
} from '@/components/ui'
import { AccountSelect } from '@/components/AccountSelect'
import {
  merchantOrdersOf,
  useSuggestMerchantCategories,
  useIgnoreMerchantOrder,
  useMatchMerchant,
  useMerchantOrders,
  useMerchantSummary,
  type MerchantAccount,
  type MerchantSummary,
} from '@/lib/clients/merchant'
import { capitalize, formatCount, formatDate, plural } from '@/lib/format'
import { MERCHANTS, type MerchantId, aNoun } from '@/lib/merchants'
import type { Uuid } from '@/lib/transactions/types'

import { BankRowDialog } from './BankRowDialog'
import { OrderRow } from './OrderRow'

export function OrdersCard({
  merchant,
  accounts,
}: {
  merchant: MerchantId
  accounts: MerchantAccount[]
}) {
  const { noun, nounPlural } = MERCHANTS[merchant]
  const { show } = useToast()
  const navigate = useNavigate()
  const summary = useMerchantSummary(merchant)
  const match = useMatchMerchant(merchant)
  const suggest = useConfirm(useSuggestMerchantCategories(merchant), {
    onSuccess: (result) =>
      show({
        title:
          result.queued === 0
            ? `All ${result.rows} matched row${result.rows === 1 ? ' is' : 's are'} already waiting for a suggestion`
            : `${result.queued} of ${plural(result.rows, 'matched row')} queued for a category suggestion`,
        description:
          result.queued === 0
            ? undefined
            : 'Suggestions arrive on the Assistant page as each run finishes.',
        action:
          result.queued === 0
            ? undefined
            : { label: 'Open the Assistant', onSelect: () => navigate('/assistant') },
      }),
  })
  const matched = summary.data?.matched_transactions ?? 0
  const [accountId, setAccountId] = useState<Uuid | 'all'>('all')
  const [unmatchedOnly, setUnmatchedOnly] = useState(false)
  const [pickingId, setPickingId] = useState<Uuid | null>(null)
  const orders = useMerchantOrders(merchant, {
    accountId: accountId === 'all' ? undefined : accountId,
    unmatched: unmatchedOnly,
    // A receipt draws a line per item, so pages are kept short.
    limit: 25,
  })
  const rows = merchantOrdersOf(orders.data)
  const total = orders.data?.pages[0]?.total ?? 0
  const ignore = useIgnoreMerchantOrder(merchant)
  // The dialog reads the order off the list, so a match made in it shows up
  // in the dialog the moment the list refetches.
  const picking = rows.find((order) => order.id === pickingId) ?? null

  return (
    <Card
      title={`${capitalize(nounPlural)} on file`}
      subtitle={summary.data ? coverage(merchant, summary.data) : undefined}
      actions={
        <>
          <Button
            size="sm"
            variant="secondary"
            disabled={match.isPending}
            onClick={() =>
              match.mutate(undefined, {
                onSuccess: (result) =>
                  show({
                    title:
                      result.matched === 0
                        ? 'Nothing new matched'
                        : `${plural(result.matched, 'bank row')} matched`,
                  }),
              })
            }
          >
            <Link2 size={14} aria-hidden="true" /> Match now
          </Button>
          <Button
            size="sm"
            variant="secondary"
            disabled={suggest.dialog.pending || matched === 0}
            onClick={() => suggest.ask()}
          >
            <Sparkles size={14} aria-hidden="true" /> Suggest categories
          </Button>
        </>
      }
    >
      <ConfirmDialog
        {...suggest.dialog}
        title={`Suggest categories for ${plural(matched, 'matched row')}?`}
        description={`The assistant suggests each row's category from its ${noun}'s items. Suggestions arrive on the Assistant page; nothing changes until you accept one.`}
        confirmLabel="Suggest categories"
      />
      <div className="merchant-filters" role="group" aria-label={`Which ${nounPlural}`}>
        {accounts.length > 1 ? (
          <AccountSelect
            accounts={accounts}
            value={accountId}
            onValueChange={setAccountId}
            firstOption={{ value: 'all', label: 'Every account' }}
            className="merchant-filters__account"
            aria-label="Account"
          />
        ) : null}
        <Button
          size="sm"
          variant={unmatchedOnly ? 'primary' : 'secondary'}
          aria-pressed={unmatchedOnly}
          onClick={() => setUnmatchedOnly((on) => !on)}
        >
          Only unmatched
        </Button>
      </div>
      {orders.isPending ? (
        <SkeletonRows rows={4} />
      ) : rows.length === 0 ? (
        <EmptyState
          icon={<Package size={20} />}
          title={unmatchedOnly ? `Every ${noun} has a bank row` : `No ${nounPlural} yet`}
          body={
            unmatchedOnly
              ? `Every ${noun} on file is matched to its charge.`
              : `Import a file above to list ${nounPlural} here.`
          }
        />
      ) : (
        <>
          <Table lines={2} stack="tablet">
            <thead>
              <tr>
                <Th>Ordered</Th>
                <Th>Account</Th>
                <Th>Items</Th>
                <Th numeric>Total</Th>
                <Th>Bank row</Th>
                <Th>
                  <span className="visually-hidden">Actions</span>
                </Th>
              </tr>
            </thead>
            <tbody>
              {rows.map((order) => (
                <OrderRow
                  key={order.id}
                  merchant={merchant}
                  order={order}
                  onPick={() => setPickingId(order.id)}
                  onIgnore={(ignored) =>
                    ignore.mutate(
                      { orderId: order.id, ignored },
                      {
                        onSuccess: () =>
                          show({
                            title: ignored
                              ? `${capitalize(noun)} ${order.order_number} is ignored`
                              : `${capitalize(noun)} ${order.order_number} waits for a bank row again`,
                            description: ignored
                              ? 'It is offered to no bank row and no longer counts as unmatched.'
                              : undefined,
                          }),
                      },
                    )
                  }
                />
              ))}
            </tbody>
          </Table>
          {orders.hasNextPage ? (
            <p className="muted merchant-more">
              Showing {formatCount(rows.length)} of {formatCount(total)} {nounPlural}.{' '}
              <Button
                size="sm"
                variant="secondary"
                disabled={orders.isFetchingNextPage}
                onClick={() => void orders.fetchNextPage()}
              >
                {orders.isFetchingNextPage ? 'Loading…' : 'Load more'}
              </Button>
            </p>
          ) : null}
        </>
      )}
      <BankRowDialog
        merchant={merchant}
        order={picking}
        onClose={() => setPickingId(null)}
      />
    </Card>
  )
}

/** "10 orders, Jan 1, 2025 to Dec 31, 2025 · 30 items · 10 of 100 Amazon rows matched, 90 without an order". */
function coverage(merchant: MerchantId, summary: MerchantSummary): string {
  const { name, noun, nounPlural } = MERCHANTS[merchant]
  const span =
    summary.oldest_order && summary.newest_order
      ? `, ${formatDate(summary.oldest_order, 'short')} to ${formatDate(summary.newest_order, 'short')}`
      : ''
  const unmatched = summary.merchant_transactions - summary.matched_transactions
  return [
    `${plural(summary.orders, noun, nounPlural)}${span}`,
    plural(summary.items, 'item'),
    summary.charges > 0
      ? plural(summary.charges, 'card charge')
      : null,
    `${formatCount(summary.matched_transactions)} of ${formatCount(summary.merchant_transactions)} ${name} rows matched, ${formatCount(unmatched)} without ${aNoun(merchant)}`,
  ]
    .filter((part): part is string => part !== null)
    .join(' · ')
}
