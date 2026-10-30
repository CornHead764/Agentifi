import { Link2Off, RefreshCw, Upload } from 'lucide-react'
import { useState } from 'react'

import {
  Button,
  ConfirmDialog,
  DialogActions,
  DialogContent,
  Field,
  FormDialog,
  Input,
  MoneyInput,
  OptionSelect,
  Spinner,
  Switch,
  Textarea,
  useConfirm,
  useToast,
} from '@/components/ui'
import {
  useUnlinkAccount,
  useUpdateAccountSettings,
  useValuationSources,
} from '@/lib/clients/connections'
import { accountTypeOptions, effectiveKind, valuationSourceLabel } from '@/lib/accountTypes'
import {
  asHideMode,
  draftOf,
  patchOf,
  SUGGESTED_THRESHOLD,
  thresholdProblem,
  thresholdWire,
  UNSECURED,
  wireOrNull,
  type AccountDraft,
  type HideMode,
} from '@/lib/accountDraft'
import { currencyOptions } from '@/lib/currencies'
import { formatDate, plural } from '@/lib/format'
import { formatPercent, parseRate } from '@/lib/format'
import { Money as MoneyGlyph } from '@/components/Money'
import { Facts } from '@/components/Facts'
import { InfoTip } from '@/components/InfoTip'
import { AccountSelect } from '@/components/AccountSelect'
import { describeStatementSource } from '@/lib/accounts'
import {
  useInstitutions,
  useSetInstitutionThreshold,
  type Institution,
} from '@/lib/clients/institutions'
import { useAccounts } from '@/lib/transactions/queries'
import { REGISTER_TABS } from '@/lib/transactions/registerTab'
import type { AccountWithBalances } from '@/lib/transactions/types'

import { useRepriceAccount } from './useRepriceAccount'
import { ValueHistoryDialog } from './ValueHistoryDialog'
import { amountToWire, formatMoney } from '@/lib/money'

/**
 * The account facts that are not balances. The type is editable because
 * SimpleFIN carries no account type, so a mortgage can arrive guessed as
 * checking; changing the type changes the kind, which every balance
 * calculation switches on. A blank field clears the stored value: for mileage,
 * null is "unknown" and zero is a real reading.
 */
export function AccountDetailsDialog({
  account,
  open,
  onOpenChange,
}: {
  account: AccountWithBalances
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const unlink = useConfirm(useUnlinkAccount())

  return (
    <>
      <FormDialog open={open} onOpenChange={onOpenChange}>
        {/* The dialog outlives the account it was opened for (the register
            header stays mounted across accounts), so the form is keyed to
            the account as well as the opening. */}
        <AccountDetailsForm
          key={account.id}
          account={account}
          onOpenChange={onOpenChange}
          onDetach={() => {
            if (account.connection_id !== null) {
              unlink.ask({ connectionId: account.connection_id, accountId: account.id })
            }
          }}
        />
      </FormDialog>

      <ConfirmDialog
        {...unlink.dialog}
        title={`Make ${account.name} manual?`}
        description="Stops syncing; you keep the balance. Transactions stay."
        confirmLabel="Make manual"
      >
        <p className="hint">To reconnect later, match it again with a sync floor.</p>
      </ConfirmDialog>
    </>
  )
}

function AccountDetailsForm({
  account,
  onOpenChange,
  onDetach,
}: {
  account: AccountWithBalances
  onOpenChange: (open: boolean) => void
  onDetach: () => void
}) {
  const { show } = useToast()

  const sources = useValuationSources()
    // Assets only: the server refuses securing a loan on a checking account.
  const accounts = useAccounts()
  const update = useUpdateAccountSettings()
  const reprice = useRepriceAccount(account)

  const [draft, setDraft] = useState(() => draftOf(account))
  const edit =
    <K extends keyof AccountDraft>(field: K) =>
    (value: AccountDraft[K]) =>
      setDraft((current) => ({ ...current, [field]: value }))
  const {
    name,
    notes,
    type,
    currency,
    masked,
    opening,
    openingOn,
    historyStart,
    limit,
    closeDay,
    statement,
    minimum,
    dueDate,
    apr,
    excludePending,
    requiresReceipts,
    securedBy,
    logo,
    address,
    vin,
    mileage,
    mileageOn,
    perYear,
    closed,
    hideMode,
    hideBelow,
    openingTab,
  } = draft
  const [importing, setImporting] = useState(false)

  const institutions = useInstitutions()
  const setInstitutionThreshold = useSetInstitutionThreshold()
  const institution = institutions.data?.find((one) => one.id === account.institution_id) ?? null
  // Null until touched, so institutions that arrive after the dialog opened
  // still decide what the control starts on.
  const [institutionDraft, setInstitutionDraft] = useState<InstitutionRule | null>(null)
  const institutionRule = institutionDraft ?? ruleOf(institution)

  // Read off the chosen type rather than the stored one, so switching an
  // account to Real Estate reveals the address field without a save first.
  const canPrice = sources.data?.asset_types.includes(type) ?? false
  const isConfigured = sources.data?.configured.includes(type) ?? false
  const isProperty = type === 'real_estate'
  const isLoan = effectiveKind(type, account) === 'loan'
  const isCredit = effectiveKind(type, account) === 'credit_card'
  const assets = (accounts.data ?? []).filter(
    (one) => one.kind === 'asset' && one.id !== account.id,
  )

  function save() {
    const saved = patchOf(draft, account, canPrice)
    if ('problem' in saved) {
      show({ title: saved.problem, tone: 'error' })
      return
    }

    if (institution !== null && institutionDraft !== null) {
      let ruleAt: string | null = null
      if (institutionDraft.hides) {
        ruleAt = thresholdWire(institutionDraft.below)
        if (ruleAt === null) {
          show({ title: thresholdProblem(institutionDraft.below), tone: 'error' })
          return
        }
      }
      if (ruleAt !== wireOrNull(institution.hide_below_balance)) {
        setInstitutionThreshold.mutate({ id: institution.id, threshold: ruleAt })
      }
    }

    update.mutate({ id: account.id, patch: saved.patch }, { onSuccess: () => onOpenChange(false) })
  }

  const typeField = (
    <Field label="Type" hint="A sync never changes it back.">
      <OptionSelect
        value={type}
        onValueChange={edit('type')}
        aria-label="Account type"
        options={accountTypeOptions(account.type)}
      />
    </Field>
  )

  return (
    <DialogContent
      title={account.name}
      description="How this account is drawn, and what it is worth."
      onSubmit={save}
      footer={
        <DialogActions>
          <Button type="submit" variant="primary" disabled={update.isPending}>
            {update.isPending ? 'Saving…' : 'Save'}
          </Button>
        </DialogActions>
      }
    >
      <div className="stack">
        <section className="stack stack--3">
          <Field label="Name">
            <Input value={name} onChange={(event) => edit('name')(event.target.value)} />
          </Field>
          {typeField}
          <Field label="Account number" hint="Last four digits. Display only.">
            <Input
              value={masked}
              onChange={(event) => edit('masked')(event.target.value)}
              placeholder="1234"
              autoComplete="off"
            />
          </Field>
          {isLoan ? (
            <Field
              label="Secured on"
              hint="The asset behind this loan. Pairing them shows its equity."
            >
              <AccountSelect
                accounts={assets}
                value={securedBy}
                onValueChange={edit('securedBy')}
                firstOption={{ value: UNSECURED, label: 'Not secured on anything' }}
                aria-label="Secured on"
              />
            </Field>
          ) : null}
          <Field label="Notes">
            <Textarea value={notes} onChange={(event) => edit('notes')(event.target.value)} rows={3} />
          </Field>
          <Field label="Opens on" hint="The tab its register shows first.">
            <OptionSelect
              value={openingTab}
              onValueChange={edit('openingTab')}
              aria-label="Opens on"
              options={REGISTER_TABS.map((tab) => ({ value: tab.id, label: tab.label }))}
            />
          </Field>
          <div className="setting-row">
            <div className="setting-row__text">
              <p className="setting-row__label">Closed</p>
              <p className="hint">Kept with its history, out of the account lists.</p>
            </div>
            <Switch
              checked={closed}
              onCheckedChange={edit('closed')}
              label="Closed"
              labelPosition="before"
            />
          </div>
        </section>

        {isCredit ? (
          <section className="stack stack--3">
            <h3 className="eyebrow">Statement</h3>
            <Field label="Credit limit" hint="Blank hides utilization in the register.">
              <MoneyInput
                value={limit}
                currency={currency}
                placeholder="0.00"
                onChange={(event) => edit('limit')(event.target.value)}
              />
            </Field>
            <Field label="Statement close day" hint="1 to 31, or blank.">
              <Input
                type="number"
                min={1}
                max={31}
                value={closeDay}
                onChange={(event) => edit('closeDay')(event.target.value)}
              />
            </Field>
            {/* The statement. SimpleFIN has no field for these, so they are
                typed here. */}
            <Field label="Statement balance" hint="Amount owed on the last statement.">
              <MoneyInput
                value={statement}
                currency={currency}
                placeholder="0.00"
                onChange={(event) => edit('statement')(event.target.value)}
              />
            </Field>
            <Field label="Minimum due" hint="Amount owed, no minus sign.">
              <MoneyInput
                value={minimum}
                currency={currency}
                placeholder="0.00"
                onChange={(event) => edit('minimum')(event.target.value)}
              />
            </Field>
            <Field label="Payment due date" hint="Blank falls back to the close day.">
              <Input
                type="date"
                value={dueDate}
                onChange={(event) => edit('dueDate')(event.target.value)}
              />
            </Field>
            {account.statement_source ? (
              <p className="hint">
                {describeStatementSource(account.statement_source)}.{' '}
                <InfoTip>
                  A figure you change here is kept until a statement with a newer due date
                  replaces it.
                </InfoTip>
              </p>
            ) : null}
            <Field label="Interest rate" hint="APR as a percentage, e.g. 24.99.">
              <Input
                value={apr}
                inputMode="decimal"
                placeholder="24.99"
                onChange={(event) => edit('apr')(event.target.value)}
              />
            </Field>
          </section>
        ) : null}

        {canPrice || account.equity ? (
          <section className="stack stack--3">
            <h3 className="eyebrow">Value</h3>
            {account.equity ? (
              <Facts
                facts={[
                  {
                    label: 'Value',
                    value: (
                      <MoneyGlyph value={account.equity.value} tone="neutral" showCents={false} />
                    ),
                  },
                  {
                    label: `Owed on ${plural(account.equity.loan_ids.length, 'loan')}`,
                    value: (
                      <MoneyGlyph value={account.equity.owed} tone="neutral" showCents={false} />
                    ),
                  },
                  account.equity.loan_to_value !== null && {
                    label: 'Financed',
                    value: formatPercent(parseRate(account.equity.loan_to_value), { digits: 1 }),
                  },
                  {
                    label: 'Equity',
                    value: (
                      <MoneyGlyph value={account.equity.equity} tone="neutral" showCents={false} />
                    ),
                  },
                ]}
              />
            ) : null}

            {canPrice && isProperty ? (
              <Field label="Address" hint="Full street address.">
                <Input value={address} onChange={(event) => edit('address')(event.target.value)} />
              </Field>
            ) : null}
            {canPrice && !isProperty ? (
              <>
                <Field label="VIN">
                  <Input value={vin} onChange={(event) => edit('vin')(event.target.value)} />
                </Field>
                <Field label="Odometer" hint="The last reading you took.">
                  <Input
                    type="number"
                    value={mileage}
                    onChange={(event) => edit('mileage')(event.target.value)}
                  />
                </Field>
                <Field label="Read on" hint="When that reading was taken.">
                  <Input
                    type="date"
                    value={mileageOn}
                    onChange={(event) => edit('mileageOn')(event.target.value)}
                  />
                </Field>
                <Field label="Miles a year" hint="Projects the odometer between readings.">
                  <Input
                    type="number"
                    value={perYear}
                    onChange={(event) => edit('perYear')(event.target.value)}
                  />
                </Field>
              </>
            ) : null}

            {canPrice ? (
              <div className="setting-row">
                <div className="setting-row__text">
                  <p className="setting-row__label">Estimate</p>
                  <p className="hint">
                    {!isConfigured
                      ? 'Optional Zillow and Kelley Blue Book estimates are available once the server admin configures the Camoufox browser.'
                      : account.valued_at
                        ? `Last priced ${formatDate(account.valued_at)} by ${valuationSourceLabel(account.valuation_source ?? '')}`
                        : 'Never priced. Re-priced monthly.'}
                  </p>
                </div>
                <div className="setting-row__actions">
                  <Button onClick={() => setImporting(true)}>
                    <Upload size={13} /> Import history
                  </Button>
                  <Button onClick={reprice.reprice} disabled={!isConfigured || reprice.pending}>
                    {reprice.pending ? (
                      <Spinner size={13} />
                    ) : (
                      <RefreshCw size={13} />
                    )}
                    {reprice.pending ? 'Getting an estimate…' : 'Get an estimate'}
                  </Button>
                </div>
              </div>
            ) : null}
          </section>
        ) : null}

        {account.connection_id !== null ? (
          <section className="stack stack--3">
            <h3 className="eyebrow">Sync</h3>
            <div className="setting-row">
              <div className="setting-row__text">
                <p className="setting-row__label">Synced through SimpleFIN</p>
                <p className="hint">Making it manual stops the sync; its transactions stay.</p>
              </div>
              <Button onClick={() => onDetach()}>
                <Link2Off size={13} aria-hidden="true" /> Make manual
              </Button>
            </div>
          </section>
        ) : null}

        <details className="account-details__advanced">
          <summary>Advanced</summary>
          <div className="stack stack--3">
            <Field
              label="Currency"
              hint="Balances on it are in this currency; totals convert from it."
            >
              <OptionSelect
                value={currency}
                onValueChange={edit('currency')}
                aria-label="Currency"
                options={currencyOptions(account.currency)}
              />
            </Field>

            <Field label="Logo" hint="Image URL. Blank uses the bank's icon.">
              <Input
                value={logo}
                onChange={(event) => edit('logo')(event.target.value)}
                placeholder="https://…"
              />
            </Field>

            <Field
              label="Opening balance"
              hint="Before the first transaction. Changing it shifts every running balance."
            >
              <MoneyInput
                value={opening}
                currency={currency}
                signed
                placeholder="0.00"
                onChange={(event) => edit('opening')(event.target.value)}
              />
            </Field>

            <Field label="Opening balance on" hint="Blank means before the earliest row.">
              <Input
                type="date"
                value={openingOn}
                onChange={(event) => edit('openingOn')(event.target.value)}
              />
            </Field>

            <Field label="History starts" hint={historyStartHint(historyStart, account)}>
              <div className="row row--wrap history-start">
                <Input
                  type="date"
                  value={historyStart}
                  onChange={(event) => edit('historyStart')(event.target.value)}
                />
                {historyStart === '' ? null : (
                  <Button onClick={() => edit('historyStart')('')}>Use automatic</Button>
                )}
              </div>
            </Field>

            <div className="setting-row">
              <div className="setting-row__text">
                <p className="setting-row__label">Exclude pending bank transactions</p>
                <p className="hint">For a feed whose pending rows never settle.</p>
              </div>
              <Switch
                checked={excludePending}
                onCheckedChange={edit('excludePending')}
                label="Exclude pending bank transactions"
                labelPosition="before"
              />
            </div>

            <div className="setting-row">
              <div className="setting-row__text">
                <p className="setting-row__label">Requires receipts</p>
                <p className="hint">
                  Every payment from this account needs a receipt, and rows without one are
                  counted on the dashboard. On by default for a banking HSA.
                </p>
              </div>
              <Switch
                checked={requiresReceipts}
                onCheckedChange={edit('requiresReceipts')}
                label="Requires receipts"
                labelPosition="before"
              />
            </div>

            <Field label="Small balance" hint={hideModeHint(hideMode, institution)}>
              <div className="row row--wrap history-start">
                {/* With no institution there is nothing to inherit, and following
                    nothing reads the same as always showing. */}
                <OptionSelect
                  value={institution === null && hideMode === 'inherit' ? 'always' : hideMode}
                  onValueChange={(value) => edit('hideMode')(asHideMode(value))}
                  aria-label="Small balance"
                  options={[
                    ...(institution === null
                      ? []
                      : [{ value: 'inherit', label: `Use the ${institution.name} setting` }]),
                    { value: 'always', label: 'Always show' },
                    { value: 'below', label: 'Hide under an amount' },
                  ]}
                />
                {hideMode === 'below' ? (
                  <MoneyInput
                    value={hideBelow}
                    currency={currency}
                    placeholder={SUGGESTED_THRESHOLD}
                    aria-label="Hide this account under"
                    onChange={(event) => edit('hideBelow')(event.target.value)}
                  />
                ) : null}
              </div>
            </Field>

            {institution !== null ? (
              <div className="setting-row">
                <div className="setting-row__text">
                  <p className="setting-row__label">Hide small {institution.name} balances</p>
                  <p className="hint">
                    Every {institution.name} account, from account lists only; totals still
                    count them.
                  </p>
                </div>
                <div className="setting-row__actions">
                  {institutionRule.hides ? (
                    <MoneyInput
                      value={institutionRule.below}
                      placeholder={SUGGESTED_THRESHOLD}
                      aria-label={`Hide ${institution.name} accounts under`}
                      onChange={(event) =>
                        setInstitutionDraft({ ...institutionRule, below: event.target.value })
                      }
                    />
                  ) : null}
                  <Switch
                    checked={institutionRule.hides}
                    onCheckedChange={(hides) => setInstitutionDraft({ ...institutionRule, hides })}
                    label={`Hide all ${institution.name} accounts under an amount`}
                    labelPosition="before"
                  />
                </div>
              </div>
            ) : null}
          </div>
        </details>
      </div>

      {canPrice ? (
        <ValueHistoryDialog account={account} open={importing} onOpenChange={setImporting} />
      ) : null}
    </DialogContent>
  )
}

/**
 * The history-start hint: a date input shows no placeholder, so the hint
 * carries the automatic day or the one a cleared field would go back to.
 */
function historyStartHint(typed: string, account: AccountWithBalances): string {
  const automatic = account.history?.automatic_starts_on ?? null
  const effect = 'Counts as zero in net worth before this.'
  if (automatic === null) return `First day with a balance. ${effect}`
  const when = formatDate(automatic)
  return typed === ''
    ? `Automatic: ${when}. ${effect}`
    : `Set by hand (automatic: ${when}). ${effect}`
}

/** An institution's small-balance rule as the dialog edits it. */
interface InstitutionRule {
  hides: boolean
  below: string
}

function ruleOf(institution: Institution | null): InstitutionRule {
  const threshold = institution?.hide_below_balance ?? null
  return threshold === null
    ? { hides: false, below: SUGGESTED_THRESHOLD }
    : { hides: true, below: amountToWire(threshold) }
}

/** What the small-balance field says under itself, naming the rule in force. */
function hideModeHint(mode: HideMode, institution: Institution | null): string {
  const effect = 'Totals still count it.'
  if (mode === 'below') return `Hidden while its balance, either side of zero, is under this. ${effect}`
  if (mode === 'always') return 'Always listed, even if its bank hides small balances.'
  const threshold = institution?.hide_below_balance ?? null
  if (institution === null || threshold === null) return 'Listed whatever it holds.'
  return `${institution.name} hides accounts under ${formatMoney(threshold)}. ${effect}`
}
