import {
  ChevronDown,
  ChevronRight,
  CreditCard,
  Eye,
  EyeOff,
  History,
  Link2,
  Sparkles,
} from 'lucide-react'
import { useState } from 'react'

import { Money } from '@/components/Money'
import { useMoneyText } from '@/components/moneyText'
import { QueryBoundary } from '@/components/QueryBoundary'
import { StatementButton } from '@/components/StatementButton'
import { Badge, Button, OptionSelect, Table, Td, Th } from '@/components/ui'
import { AccountSelect } from '@/components/AccountSelect'
import { maskedNumber } from '@/lib/accounts'
import {
  useBills,
  useLinkSeriesToBill,
  useUnlinkSeriesFromBill,
  useUpdateBillSubaccount,
  type Bill,
  type BillSubaccount,
} from '@/lib/clients/bills'
import { useAllSeries } from '@/lib/clients/upcoming'
import { formatDate, plural, timeAgo } from '@/lib/format'
import { useAccounts } from '@/lib/transactions/queries'

import { latestOpenBill, reminderChoices } from './draft'
import { SuggestedReminder } from './SuggestedReminder'
import { useMatchHistory } from './useMatchHistory'

/** One thing a login bills for, with the next open bill on the row itself. */
export function SubaccountRow({
  subaccount,
  provider,
  medical = false,
}: {
  subaccount: BillSubaccount
  /** The provider's name, for what matching its history reports. */
  provider: string
  /**
   * Its statements are receipts filed on the payments that settled them, one
   * visit at a time, so it feeds no recurring reminder.
   */
  medical?: boolean
}) {
  const [showing, setShowing] = useState(false)
  const bills = useBills(subaccount.id)
  const update = useUpdateBillSubaccount()
  const rows = bills.data ?? []
  const open = latestOpenBill(rows)
  const hidden = !subaccount.is_selected
  const setHidden = (next: boolean) =>
    update.mutate({ id: subaccount.id, patch: { is_selected: !next } })
  const [linking, setLinking] = useState(false)
  const [linkingAccount, setLinkingAccount] = useState(false)

  return (
    <li className={hidden ? 'bill-subaccount bill-subaccount--hidden' : 'bill-subaccount'}>
      <div className="bill-subaccount__head">
        <span className="bill-subaccount__who">
          <span className="bill-subaccount__name">
            <strong>{subaccount.label}</strong>
            {subaccount.masked_number ? (
              <span className="muted">{maskedNumber(subaccount.masked_number)}</span>
            ) : null}
          </span>
          <span className="bill-subaccount__note muted">
            {hidden ? (
              'Hidden: not updated, and not offered to reminders'
            ) : open ? (
              <>
                Due {formatDate(open.due_on)} · <Money value={open.amount_due} tone="neutral" />
                {/* The household's resolved autopay date, not the provider's. */}
                {open.pays_on ? ` · autopays ${formatDate(open.pays_on)}` : ''}
              </>
            ) : bills.isPending ? (
              'Looking for bills…'
            ) : (
              'No open bill on file'
            )}
          </span>
          {hidden || medical ? null : (
            <LinkedReminder
              subaccount={subaccount}
              provider={provider}
              hasBills={rows.length > 0}
              linking={linking}
              onLinking={setLinking}
            />
          )}
          {hidden ? null : (
            <LinkedAccount
              subaccount={subaccount}
              linking={linkingAccount}
              onLinking={setLinkingAccount}
            />
          )}
        </span>

        <span className="row row--1 bill-subaccount__actions">
          {rows.length === 0 ? null : (
            <Button
              size="sm"
              variant="ghost"
              aria-expanded={showing}
              onClick={() => setShowing(!showing)}
            >
              {showing ? (
                <ChevronDown size={14} aria-hidden="true" />
              ) : (
                <ChevronRight size={14} aria-hidden="true" />
              )}{' '}
              {plural(rows.length, 'bill')}
            </Button>
          )}
          <Button
            size="sm"
            variant="ghost"
            disabled={update.isPending}
            aria-label={hidden ? `Show ${subaccount.label}` : `Hide ${subaccount.label}`}
            onClick={() => setHidden(!hidden)}
          >
            {hidden ? <Eye size={14} aria-hidden="true" /> : <EyeOff size={14} aria-hidden="true" />}{' '}
            {hidden ? 'Show' : 'Hide'}
          </Button>
        </span>
      </div>

      {showing ? (
        <QueryBoundary query={bills} rows={2}>
          {(loaded) => <BillTable bills={loaded} />}
        </QueryBoundary>
      ) : null}
    </li>
  )
}

/**
 * Which reminder this account keeps current, and the way to change that. An
 * unlinked account fetches bills nobody sees on a date, so one with bills is
 * offered the reminder they describe.
 */
function LinkedReminder({
  subaccount,
  provider,
  hasBills,
  linking,
  onLinking,
}: {
  subaccount: BillSubaccount
  provider: string
  hasBills: boolean
  linking: boolean
  onLinking: (open: boolean) => void
}) {
  const [suggesting, setSuggesting] = useState(false)
  const series = useAllSeries('')
  const history = useMatchHistory(provider)
  const link = useLinkSeriesToBill()
  const unlink = useUnlinkSeriesFromBill()
  const moneyText = useMoneyText()
  const rows = series.data ?? []
  const linked = subaccount.series_id ? rows.find((one) => one.id === subaccount.series_id) : null
  const choose = (seriesId: string) => {
    if (seriesId === UNLINK) {
      if (subaccount.series_id) unlink.mutate(subaccount.series_id)
      onLinking(false)
      return
    }
    link.mutate(
      { seriesId, subaccountId: subaccount.id },
      { onSuccess: () => onLinking(false) },
    )
  }

  if (linking) {
    return (
      <span className="bill-subaccount__note">
        <OptionSelect
          value={subaccount.series_id ?? UNLINK}
          disabled={series.isPending || link.isPending || unlink.isPending}
          onValueChange={choose}
          aria-label={`Reminder for ${subaccount.label}`}
          options={[
            { value: UNLINK, label: 'Not linked' },
            ...reminderChoices(rows, subaccount.series_id, moneyText).map((one) => ({
              value: one.id,
              label: one.label,
            })),
          ]}
        />
        <Button size="sm" variant="ghost" onClick={() => onLinking(false)}>
          Done
        </Button>
      </span>
    )
  }

  return (
    <span className="bill-subaccount__note muted">
      {subaccount.series_id ? (
        <>
          <Link2 size={13} aria-hidden="true" /> Keeps {linked ? `“${linked.label}”` : 'a reminder'} current
          {linked && !linked.is_active ? ', a canceled reminder' : ''}
        </>
      ) : (
        'Not feeding a reminder yet'
      )}
      <Button size="sm" variant="ghost" onClick={() => onLinking(true)}>
        {subaccount.series_id ? 'Change' : 'Link a reminder'}
      </Button>
      {subaccount.series_id ? (
        <Button
          size="sm"
          variant="ghost"
          disabled={history.pending}
          aria-label={`Match the history of ${subaccount.label}`}
          onClick={() => void history.run({ subaccountId: subaccount.id })}
        >
          <History size={14} aria-hidden="true" /> Match history
        </Button>
      ) : null}
      {!subaccount.series_id && hasBills ? (
        <Button size="sm" variant="ghost" onClick={() => setSuggesting(true)}>
          <Sparkles size={13} aria-hidden="true" /> Suggest a reminder
        </Button>
      ) : null}
      {suggesting ? (
        <SuggestedReminder subaccount={subaccount} onClose={() => setSuggesting(false)} />
      ) : null}
    </span>
  )
}

/** The picker's value for "no reminder"; a Radix Select item cannot be the empty string. */
const UNLINK = '__none__'

/**
 * Which card or loan this billed account is. SimpleFIN carries a card's balance
 * but none of its statement, so a statement filed here fills the statement
 * balance, minimum due and due date. Only cards and loans are offered.
 */
function LinkedAccount({
  subaccount,
  linking,
  onLinking,
}: {
  subaccount: BillSubaccount
  linking: boolean
  onLinking: (open: boolean) => void
}) {
  const accounts = useAccounts()
  const update = useUpdateBillSubaccount()
  const all = accounts.data ?? []
  const rows = all.filter((one) => one.kind === 'credit_card' || one.kind === 'loan')
  const linked = subaccount.account_id
    ? all.find((one) => one.id === subaccount.account_id)
    : undefined
  const choose = (accountId: string) => {
    update.mutate(
      { id: subaccount.id, patch: { account_id: accountId === UNLINK ? null : accountId } },
      { onSuccess: () => onLinking(false) },
    )
  }

  if (linking) {
    return (
      <span className="bill-subaccount__note">
        <AccountSelect
          accounts={rows.sort((a, b) => a.name.localeCompare(b.name))}
          value={subaccount.account_id ?? UNLINK}
          disabled={accounts.isPending || update.isPending}
          onValueChange={choose}
          firstOption={{ value: UNLINK, label: 'No account' }}
          aria-label={`Card or loan for ${subaccount.label}`}
        />
        <Button size="sm" variant="ghost" onClick={() => onLinking(false)}>
          Done
        </Button>
      </span>
    )
  }

  if (!subaccount.account_id && rows.length === 0) return null
  return (
    <span className="bill-subaccount__note muted">
      {subaccount.account_id ? (
        <>
          <CreditCard size={13} aria-hidden="true" /> Fills the statement on{' '}
          {linked ? `“${linked.name}”` : 'an account'}
        </>
      ) : (
        'Not filling a card’s statement'
      )}
      <Button size="sm" variant="ghost" onClick={() => onLinking(true)}>
        {subaccount.account_id ? 'Change' : 'Link a card'}
      </Button>
    </span>
  )
}

/**
 * Every statement on file for one subaccount. `fetched_at` is when somebody
 * typed a hand-entered bill, so the column says "Entered" or "Fetched" per row.
 */
function BillTable({ bills }: { bills: readonly Bill[] }) {
  return (
    <Table density="sm" stack>
      <thead>
        <tr>
          <Th>Due</Th>
          <Th numeric>Amount</Th>
          <Th>Source</Th>
          <Th>Recorded</Th>
          <Th />
        </tr>
      </thead>
      <tbody>
        {bills.map((bill) => (
          <tr key={bill.id} className={bill.status === 'superseded' ? 'row--excluded' : undefined}>
            <Td className="stack-inline">
              <span>{formatDate(bill.due_on)}</span>
              {bill.period_start && bill.period_end ? (
                <span className="cell__sub">
                  {formatDate(bill.period_start, 'short')} to {formatDate(bill.period_end, 'short')}
                </span>
              ) : null}
            </Td>
            <Td numeric>
              <Money value={bill.amount_due} tone="neutral" />
            </Td>
            <Td className="stack-inline">
              <span>{SOURCE_LABELS[bill.source]}</span>
              {bill.status === 'open' ? null : <Badge>{STATUS_LABELS[bill.status]}</Badge>}
            </Td>
            <Td className="stack-inline">
              <span>{bill.source === 'manual' ? 'Entered' : 'Fetched'}</span>
              <span className="cell__sub">
                {timeAgo(bill.fetched_at)}
                {bill.amended_at ? ` · corrected ${timeAgo(bill.amended_at)}` : ''}
              </span>
            </Td>
            <Td label="">
              {bill.document_id ? <StatementButton documentId={bill.document_id} /> : null}
            </Td>
          </tr>
        ))}
      </tbody>
    </Table>
  )
}

/** Where the figures came from, in the words the connectors documentation uses. */
const SOURCE_LABELS: Record<Bill['source'], string> = {
  provider: 'From the provider',
  email: 'From an email',
  manual: 'Entered by hand',
  assistant: 'Read by the assistant',
}

const STATUS_LABELS: Record<Bill['status'], string> = {
  open: 'Open',
  paid: 'Paid',
  superseded: 'Superseded',
}
