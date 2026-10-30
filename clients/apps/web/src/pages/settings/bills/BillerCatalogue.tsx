import { useMemo, useState } from 'react'
import { Link } from 'react-router-dom'

import { EmptyState, Radio, RadioGroup, SearchInput } from '@/components/ui'
import { BILLERS, compareProviderNames, type Biller, type BillerAccess, type BillerId } from '@/lib/billers'
import { matchesSearch } from '@/lib/search'

const ACCESS_TEXT: Record<BillerAccess, string> = {
  api: 'Signs in to your account on its website',
  browser: 'Signs in to your account on its website',
  email: 'Reads the bills it e-mails you',
}

function domainOf(url: string): string {
  try {
    return new URL(url).hostname.replace(/^www\./, '')
  } catch {
    return ''
  }
}

function matchesBiller(biller: Biller, query: string): boolean {
  return matchesSearch(query, biller.name, domainOf(biller.homeUrl))
}

/**
 * The providers this build has a module for, searchable, plus the ways out for
 * a company that is not in it. The email-only provider is not in the list (it
 * is no one company); it is the first way out instead.
 */
export function BillerCatalogue({
  value,
  onChange,
  onLeave,
}: {
  value: BillerId | null
  onChange: (biller: BillerId) => void
  /** Called before a "don't see it" link navigates, so the dialog can close. */
  onLeave: () => void
}) {
  const [query, setQuery] = useState('')
  const sorted = useMemo(
    () =>
      BILLERS.filter((one) => one.generic !== true).sort((a, b) =>
        compareProviderNames(a.name, b.name),
      ),
    [],
  )
  const shown = sorted.filter((one) => matchesBiller(one, query))

  return (
    <div className="stack stack--3 biller-catalogue">
      <SearchInput
        placeholder="Search providers"
        aria-label="Search providers"
        value={query}
        onChange={setQuery}
      />

      {shown.length === 0 ? (
        <EmptyState compact title={`No built-in provider matches “${query.trim()}”.`} />
      ) : (
        <RadioGroup
          className="biller-catalogue__list"
          aria-label="Provider"
          value={value ?? ''}
          onValueChange={(next) => {
            const found = BILLERS.find((one) => one.id === next)
            if (found) onChange(found.id)
          }}
        >
          {shown.map((one) => (
            <Radio
              key={one.id}
              value={one.id}
              label={
                <span className="biller-catalogue__option">
                  <span>{one.name}</span>
                  <span className="muted">
                    {ACCESS_TEXT[one.access]}
                    {one.site?.address === true || domainOf(one.homeUrl) === ''
                      ? ''
                      : ` · ${domainOf(one.homeUrl)}`}
                  </span>
                </span>
              }
            />
          ))}
        </RadioGroup>
      )}

      <div className="biller-catalogue__other">
        <p className="biller-catalogue__heading">Don&rsquo;t see your provider?</p>
        <RadioGroup
          aria-label="A company with no connector"
          value={value === 'email-only' ? value : ''}
          onValueChange={(next) => {
            if (next === 'email-only') onChange('email-only')
          }}
        >
          <Radio
            value="email-only"
            label={
              <span className="biller-catalogue__option">
                <span>Track it from the bills it emails you</span>
                <span className="muted">
                  Name the company; a mail rule for its bills opens next. The mail is kept as
                  the statement.
                </span>
              </span>
            }
          />
        </RadioGroup>
        <ul>
          <li>
            <Link to="/upcoming/recurring?new=1" onClick={onLeave}>
              Add it as a recurring bill
            </Link>{' '}
            — you set the amount and date; the payment is matched when it clears.
          </li>
          <li>
            <Link to="/settings/email#mail-rules" onClick={onLeave}>
              Write a mail rule
            </Link>{' '}
            if it emails receipts, to turn each one into a transaction.
          </li>
          <li>
            New built-in providers are added on the server:{' '}
            <code>docs/connectors/adding-a-bill-provider.md</code>.
          </li>
        </ul>
      </div>
    </div>
  )
}
