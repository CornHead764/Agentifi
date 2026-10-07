import { useState } from 'react'

import { Money } from '@/components/Money'
import { EmptyState, SearchInput, SkeletonRows } from '@/components/ui'
import {
  paysToTheCent,
  useMerchantMatchCandidates,
  type MerchantMatchCandidate,
  type MerchantOrder,
} from '@/lib/clients/merchant'
import { formatDate } from '@/lib/format'
import { MERCHANTS, type MerchantId } from '@/lib/merchants'
import { displayPayee } from '@/lib/transactions/edits'
import { useSettled } from '@/lib/useSettled'

/**
 * The search and the candidates, keyed on the record so a new one opens with
 * an empty search. Picking always adds: one payment can settle several
 * records. Undo, in the dialog above, is the only way to remove a match.
 */
export function BankRowCandidates({
  merchant,
  order,
  busy,
  onPick,
}: {
  merchant: MerchantId
  order: MerchantOrder
  busy: boolean
  onPick: (row: MerchantMatchCandidate) => void
}) {
  const { name, noun } = MERCHANTS[merchant]
  const [search, setSearch] = useState('')
  const debounced = useSettled(search)
  const candidates = useMerchantMatchCandidates(merchant, order.id, debounced)

  return (
    <>
      <SearchInput
        placeholder="Search bank rows by payee or statement name"
        value={search}
        onChange={setSearch}
        aria-label="Search bank rows"
      />

      {candidates.isPending ? (
        <SkeletonRows rows={4} />
      ) : !candidates.data || candidates.data.candidates.length === 0 ? (
        <EmptyState
          compact
          title={
            debounced
              ? 'No bank row on any account, on any date, is worded like that.'
              : `No free ${name} bank row within two months after this ${noun}. Search to reach any row.`
          }
        />
      ) : (
        <ul className="pick-list pick-list--scroll merchant-pick__list">
          {candidates.data.candidates.map((row) => (
            <li key={row.id}>
              <button
                type="button"
                className="pick-list__row merchant-pick__row"
                disabled={busy}
                onClick={() => onPick(row)}
              >
                <span className="pick-list__name">
                  {formatDate(row.date, 'short')} · {row.account_name}
                  <small>
                    {displayPayee(row)}
                    {row.is_pending ? ' · pending' : ''}
                    {row.matched_order_number
                      ? ` · already pays for ${noun} ${row.matched_order_number}; this one joins it`
                      : ''}
                  </small>
                </span>
                <span className="pick-list__figures merchant-pick__figures">
                  <Money value={row.amount} tone="neutral" />
                  {paysToTheCent(row.amount, order.card_total) ? (
                    <small>to the cent</small>
                  ) : null}
                </span>
              </button>
            </li>
          ))}
        </ul>
      )}
    </>
  )
}
