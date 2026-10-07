/**
 * *Create a rule* and *Create a series*, started from one transaction, in the
 * same editors the Rules and Bills & Income screens open, seeded from the row.
 *
 * A saved rule only applies to what arrives next, so the *Review existing
 * transactions* step follows, or the row in hand stays untouched. A series
 * asks the server for the cadence the charge has kept first, and falls back to
 * the row alone.
 */

import { useQuery } from '@tanstack/react-query'
import { useRef, useState } from 'react'

import { SeriesEditor } from '@/components/SeriesEditor'
import { Dialog, DialogContent, useToast } from '@/components/ui'
import { useCreateRule, type Rule } from '@/lib/clients/rules'
import { suggestSeriesFor, seriesKeys } from '@/lib/clients/upcoming'
import type { Account, Category, Tag, Transaction } from '@/lib/transactions/types'
import type { RuleSeed } from '@/pages/rules/conditions'
import { RuleEditor } from '@/pages/rules/RuleEditor'
import { RuleReviewDialog } from '@/pages/rules/RuleReviewDialog'
import { amountToWire } from '@/lib/money'

/** Which editor is open, and the row it was opened from. */
export type CreateTarget = { kind: 'rule' | 'series' | 'refund'; txn: Transaction } | null

export interface CreateFromTransactionProps {
  target: CreateTarget
  onClose: () => void
  accounts: readonly Account[]
  categories: readonly Category[]
  tags: readonly Tag[]
  /** Told what was made, so the page can say where to find it. */
  onCreated?: (kind: 'rule' | 'series' | 'refund') => void
}

export function CreateFromTransaction({
  target,
  onClose,
  accounts,
  categories,
  tags,
  onCreated,
}: CreateFromTransactionProps) {
  if (target === null) return null
  if (target.kind === 'refund') {
    return (
      <RefundFromTransaction
        txn={target.txn}
        onClose={onClose}
        accounts={accounts}
        categories={categories}
        tags={tags}
        onCreated={onCreated}
      />
    )
  }
  if (target.kind === 'rule') {
    return (
      <RuleCreateFlow
        seed={{
          statement_name: target.txn.statement_name,
          payee: target.txn.payee,
          category_id: target.txn.category_id,
        }}
        onClose={onClose}
        accounts={accounts}
        categories={categories}
        tags={tags}
        onCreated={() => onCreated?.('rule')}
      />
    )
  }
  return (
    <SeriesFromTransaction
      txn={target.txn}
      onClose={onClose}
      accounts={accounts}
      categories={categories}
      tags={tags}
      onCreated={onCreated}
    />
  )
}

/**
 * A new rule, then *Review existing transactions* for it: the Rules screen's
 * *New rule* and the register's *Create a rule* both end on the review, since
 * saving alone reaches only what arrives next.
 */
export function RuleCreateFlow({
  seed = null,
  onClose,
  accounts,
  categories,
  tags,
  onCreated,
}: {
  seed?: RuleSeed | null
  onClose: () => void
  accounts: readonly Account[]
  categories: readonly Category[]
  tags: readonly Tag[]
  onCreated?: (rule: Rule) => void
}) {
  const create = useCreateRule()
  const { show } = useToast()
  const [made, setMade] = useState<Rule | null>(null)
  // The editor's own onClose after save would unmount the review dialog with
  // this component. A ref, because it must be true before that promise chain
  // continues.
  const reviewing = useRef(false)

  if (made !== null) {
    return (
      <RuleReviewDialog
        rule={made}
        categories={categories}
        tags={tags}
        onApplied={(count) =>
          show({
            title: count === 1 ? '1 transaction updated' : `${count} transactions updated`,
            tone: 'success',
          })
        }
        onClose={onClose}
      />
    )
  }

  return (
    <RuleEditor
      rule={null}
      seed={seed}
      accounts={accounts}
      categories={categories}
      tags={tags}
      pending={create.isPending}
      onSubmit={(body) =>
        create.mutateAsync(body).then((rule) => {
          onCreated?.(rule)
          reviewing.current = true
          setMade(rule)
          return rule
        })
      }
      onClose={() => {
        if (!reviewing.current) onClose()
      }}
    />
  )
}

function SeriesFromTransaction({
  txn,
  onClose,
  accounts,
  categories,
  tags,
  onCreated,
}: {
  txn: Transaction
  onClose: () => void
  accounts: readonly Account[]
  categories: readonly Category[]
  tags: readonly Tag[]
  onCreated?: (kind: 'rule' | 'series') => void
}) {
  // Not cached beyond this dialog: the register is being edited, and a stale
  // cadence is worse than a second request.
  const suggestion = useQuery({
    queryKey: seriesKeys.suggestedFor(txn.id),
    queryFn: ({ signal }) => suggestSeriesFor(txn.id, signal),
    gcTime: 0,
    staleTime: 0,
    retry: false,
  })

  // A failed lookup is not a failed action — the row itself is enough to open
  // on, and the editor is where the user was going anyway.
  if (suggestion.isPending) {
    return (
      <Dialog open onOpenChange={(next) => (next ? undefined : onClose())}>
        <DialogContent title="New series">
          <p className="hint">Reading what this charge has done before…</p>
        </DialogContent>
      </Dialog>
    )
  }

  return (
    <SeriesEditor
      open
      onOpenChange={(next) => {
        if (!next) onClose()
      }}
      seed={{
        statement_name: txn.statement_name,
        payee: txn.payee,
        account_id: txn.account_id,
        category_id: txn.category_id,
        amount: amountToWire(txn.amount),
        date: txn.date,
      }}
      suggestion={suggestion.data ?? null}
      accounts={accounts}
      categories={categories}
      tags={tags}
      onSaved={() => onCreated?.('series')}
    />
  )
}

/**
 * *Track a refund*, from the charge being returned: a one-expected-credit
 * series, positive, one-time, so accepting it closes the card.
 */
function RefundFromTransaction({
  txn,
  onClose,
  accounts,
  categories,
  tags,
  onCreated,
}: {
  txn: Transaction
  onClose: () => void
  accounts: readonly Account[]
  categories: readonly Category[]
  tags: readonly Tag[]
  onCreated?: (kind: 'rule' | 'series' | 'refund') => void
}) {
  return (
    <SeriesEditor
      open
      fixedKind="refund"
      onOpenChange={(next) => {
        if (!next) onClose()
      }}
      seed={{
        statement_name: txn.statement_name,
        payee: txn.payee,
        account_id: txn.account_id,
        category_id: txn.category_id,
        amount: amountToWire(txn.amount).replace(/^-/, ''),
        date: txn.date,
      }}
      accounts={accounts}
      categories={categories}
      tags={tags}
      onSaved={() => onCreated?.('refund')}
    />
  )
}
