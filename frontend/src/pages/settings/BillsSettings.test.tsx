import type { QueryClient } from '@tanstack/react-query'
import { describe, expect, it } from 'vitest'

import type {
  Bill,
  BillAgentStatus,
  BillChallenge,
  BillConnection,
  BillSubaccount,
} from '@/lib/clients/bills'
import { parseMoney } from '@/lib/money'
import { renderScreen, testQueryClient } from '@/test/renderScreen'

import { BillsSettings } from './BillsSettings'

/** The section off a seeded cache. The figures are invented. */
function render(seed?: (client: QueryClient) => void) {
  const client = testQueryClient()
  seed?.(client)
  return renderScreen(<BillsSettings />, { client })
}

const bareConnection = {
  username: '',
  site: '',
  credential_source: 'session' as const,
  has_totp: false,
  second_factor: '' as const,
  connected: false,
  signed_in_at: null,
  needs_sign_in: false,
  sign_in_paused: '' as const,
  autopay_days: null,
  autopay_day: null,
  autopay_account_id: null,
  pull_enabled: true,
  pull_at: null,
  last_pulled_at: null,
  last_pull_status: '' as const,
  last_pull_error: '',
  has_failure_screenshot: false,
  has_trail: false,
  pulling: false,
  created_at: '2026-09-01T00:00:00Z',
}

const connections: BillConnection[] = [
  {
    ...bareConnection,
    id: 'conn-power',
    biller: 'spectrum',
    label: 'Main account',
    autopay_rule: 'days_before_due',
    autopay_days: 5,
  },
  {
    ...bareConnection,
    id: 'conn-subs',
    biller: 'apple',
    label: 'Subscriptions',
    autopay_rule: 'none',
  },
]

const subaccounts: BillSubaccount[] = [
  {
    id: 'sub-meter',
    connection_id: 'conn-power',
    biller: 'spectrum',
    external_id: 'premise-1',
    label: 'Front meter',
    masked_number: '••1234',
    is_selected: true,
    series_id: 'series-power',
    account_id: null,
  },
]

const bills: Bill[] = [
  {
    id: 'bill-sept',
    subaccount_id: 'sub-meter',
    due_on: '2026-10-26',
    amount_due: parseMoney('120.00'),
    minimum_due: null,
    currency: 'USD',
    issued_on: '2026-09-24',
    period_start: null,
    period_end: null,
    // No provider states an autopay date, so the connection's own rule is
    // what resolves the date the row shows.
    autopay_on: null,
    pays_on: '2026-10-21',
    status: 'open',
    source: 'manual',
    statement_url: '',
    document_id: null,
    fetched_at: '2026-09-25T06:00:00Z',
    amended_at: null,
  },
]

/** An engine that is up, carrying the one provider these connections use. */
const agent: BillAgentStatus = {
  configured: true,
  healthy: true,
  error: '',
  providers: [
    {
      id: 'spectrum',
      name: 'Spectrum',
      access: 'browser',
      sign_in: { kinds: ['live', 'typed'], prompt: '' },
      challenges: ['sms'],
      session_persists: true,
      keepalive_days: 0,
      reports_autopay: false,
      has_documents: true,
    },
  ],
}

const challenge: BillChallenge = {
  id: 'ch-1',
  connection_id: 'conn-power',
  method: 'sms',
  prompt: '',
  image: null,
  state: 'waiting',
  answered_by: null,
  raised_by: 'pull',
  created_at: '2026-09-17T09:01:00Z',
  expires_at: '2026-09-17T09:11:00Z',
  answered_at: null,
}

function seeded(client: QueryClient) {
  client.setQueryData(['bills', 'connections'], connections)
  client.setQueryData(['bills', 'subaccounts', 'all'], subaccounts)
  client.setQueryData(['bills', 'bills', 'sub-meter'], bills)
}

/** The same cache, plus an agent that can sign in and nothing waiting. */
function bridged(client: QueryClient) {
  seeded(client)
  client.setQueryData(['bills', 'agent'], agent)
  client.setQueryData(['bills', 'challenges', 'waiting'], [])
}

describe('the bills settings section', () => {
  it('renders with no server behind it, which is where a household starts', () => {
    // `/bills` answers 404 until its migration is applied, and the section
    // still has to say what it is for.
    const markup = render()

    expect(markup).toContain('Bill providers')
    expect(markup).toContain('Add provider')
  })

  it('names the provider rather than the id stored against the connection', () => {
    const markup = render(seeded)

    expect(markup).toContain('Spectrum')
    expect(markup).toContain('Main account')
    expect(markup).not.toContain('spectrum ·')
  })

  it('says each autopay rule in words, with the figure its own kind uses', () => {
    const markup = render(seeded)

    expect(markup).toContain('Autopays 5 days before due')
    expect(markup).toContain('No autopay')
  })

  it('shows what is owed on the row rather than behind the list of bills', () => {
    const markup = render(seeded)

    expect(markup).toContain('Front meter')
    expect(markup).toContain('$120.00')
  })

  it('shows the autopay date the connection rule resolves, not only a stated one', () => {
    // `autopay_on` is the provider's own word and is empty here; reading it
    // alone would leave the row silent about a bill the household autopays.
    expect(render(seeded)).toContain('autopays Oct 21, 2026')
  })

  it('says which subaccounts a reminder already follows', () => {
    expect(render(seeded)).toMatch(/Keeps .*current/)
    expect(render(seeded)).not.toContain('Suggest a reminder')
  })

  it('offers a reminder read from the bills to an account that feeds none', () => {
    const markup = render((client) => {
      seeded(client)
      client.setQueryData(['bills', 'subaccounts', 'all'], [{ ...subaccounts[0], series_id: null }])
    })
    expect(markup).toContain('Not feeding a reminder yet')
    expect(markup).toContain('Suggest a reminder')
  })

  it('has nothing to suggest from an account with no bills', () => {
    const markup = render((client) => {
      seeded(client)
      client.setQueryData(['bills', 'subaccounts', 'all'], [{ ...subaccounts[0], series_id: null }])
      client.setQueryData(['bills', 'bills', 'sub-meter'], [])
    })
    expect(markup).not.toContain('Suggest a reminder')
  })

  it('offers no form for a bill or an account: both come from the provider', () => {
    const markup = render(seeded)

    expect(markup).toContain('Nothing billed under this login yet')
    expect(markup).not.toContain('Enter a bill')
    expect(markup).not.toContain('Add what it bills for')
    expect(markup).not.toContain('by hand')
  })

  it('keeps the password inside the sign-in dialog, never on the page', () => {
    // The connect flow asks for one; the screen behind it does not, and a
    // field rendered here would be one nobody chose to open.
    const markup = render(bridged)

    expect(markup).not.toContain('type="password"')
    expect(markup).not.toContain('Password')
  })

  it('says a kept password is kept, and offers to forget it, never the password itself', () => {
    const markup = render((client) => {
      bridged(client)
      client.setQueryData(['bills', 'connections'], [
        { ...connections[0], credential_source: 'stored', connected: true },
        connections[1],
      ])
    })

    // The kept password is said under Details; forgetting it is occasional
    // and destructive, so it waits in the card's menu rather than on its face.
    expect(markup).toContain('<summary>Details</summary>')
    expect(markup).toContain('Password kept, encrypted')
    expect(markup).toContain('aria-label="Actions for Spectrum · Main account"')
    expect(markup).not.toContain('Forget password')
    expect(markup).not.toContain('type="password"')
  })

  it('says when an authenticator key is kept too, and that forgetting takes both', () => {
    const markup = render((client) => {
      bridged(client)
      client.setQueryData(['bills', 'connections'], [
        { ...connections[0], credential_source: 'stored', connected: true, has_totp: true },
        connections[1],
      ])
    })

    expect(markup).toContain('Password and authenticator key kept, encrypted')
  })

  it('offers one way in, and no second choice beside it', () => {
    // The sign-in dialog is the one way in; releasing a held browser lives in
    // that dialog, beside the failure it answers, and not on the card.
    const markup = render((client) => {
      bridged(client)
      client.setQueryData(['bills', 'connections'], [
        { ...connections[0], connected: true },
        connections[1],
      ])
    })

    // A working login's one button is the update; signing in again is in its
    // menu, not a second button beside it.
    expect(markup).toContain('Update now')
    expect(markup).not.toContain('Sign in again')
    expect(markup).not.toContain('Release the browser')
    expect(markup).not.toContain('live browser')
  })

  it('puts the sign-in on the card of a login that needs one, and says why once', () => {
    const markup = render((client) => {
      bridged(client)
      client.setQueryData(['bills', 'connections'], [
        {
          ...connections[0],
          connected: true,
          needs_sign_in: true,
          last_pull_status: 'needs_sign_in',
          last_pull_error: 'Example Power refused the kept session',
        },
        connections[1],
      ])
    })

    expect(markup).toContain('Needs a sign-in')
    expect(markup).toContain('</svg> Sign in again</button>')
    expect(markup).not.toContain('</svg> Update now</button>')
    expect(markup.match(/refused the kept session/g)).toHaveLength(1)
  })

  it('offers the page a stopped pull ended on beside its failure, and only when one was kept', () => {
    const failed = (has_failure_screenshot: boolean) =>
      render((client) => {
        bridged(client)
        client.setQueryData(['bills', 'connections'], [
          {
            ...connections[0],
            connected: true,
            last_pull_status: 'failed',
            last_pull_error: 'Something on the page covered the "Sign in" button',
            has_failure_screenshot,
          },
          connections[1],
        ])
      })

    expect(failed(true)).toContain('>Show screenshot</button>')
    expect(failed(false)).not.toContain('Show screenshot')
  })

  it('keeps a sign-in that never landed on the card, with its page and its trail', () => {
    const ended = (has_trail: boolean) =>
      render((client) => {
        bridged(client)
        client.setQueryData(['bills', 'connections'], [
          {
            ...connections[0],
            last_pull_status: 'sign_in_failed',
            last_pull_error:
              'The sign-in was closed before it finished, while Example Power was asking for a code.',
            has_failure_screenshot: true,
            has_trail,
          },
          connections[1],
        ])
      })

    const markup = ended(true)
    expect(markup).toContain('was asking for a code')
    expect(markup).toContain('>Show screenshot</button>')
    expect(markup).toContain('<summary>What the provider showed</summary>')
    expect(ended(false)).not.toContain('What the provider showed')
  })

  it('keeps what the provider showed on an update that got in', () => {
    const markup = render((client) => {
      bridged(client)
      client.setQueryData(['bills', 'connections'], [
        { ...connections[0], connected: true, last_pull_status: 'ok', has_trail: true },
        connections[1],
      ])
    })
    expect(markup).toContain('<summary>What the provider showed</summary>')
    expect(markup).not.toContain('Show screenshot')
  })

  it('leaves a lapsed session to the kept password, and says so without alarm', () => {
    const markup = render((client) => {
      bridged(client)
      client.setQueryData(['bills', 'connections'], [
        {
          ...connections[0],
          connected: true,
          credential_source: 'stored',
          needs_sign_in: true,
          last_pull_status: 'needs_sign_in',
          last_pull_error: 'Example Power refused the kept session',
        },
        connections[1],
      ])
    })

    expect(markup).toContain('Session expired')
    expect(markup).toContain('the kept password signs in at the next update')
    expect(markup).toContain('</svg> Update now</button>')
    expect(markup).not.toContain('role="alert"')
  })

  it('warns when a code went unanswered, and waits for a sign-in', () => {
    const markup = render((client) => {
      bridged(client)
      client.setQueryData(['bills', 'connections'], [
        {
          ...connections[0],
          connected: true,
          credential_source: 'stored',
          needs_sign_in: true,
          sign_in_paused: 'code_needed',
          last_pull_status: 'needs_sign_in',
          last_pull_error:
            'Spectrum took the kept password and then asked for a code. Automatic updates wait until you sign in.',
        },
        connections[1],
      ])
    })

    expect(markup).toContain('Code needed')
    expect(markup).toContain('role="alert"')
    expect(markup).toContain('Spectrum asked for a code. Automatic updates wait until you sign in.')
    expect(markup).toContain('</svg> Sign in again</button>')
    expect(markup).not.toContain('</svg> Update now</button>')
  })

  it('warns when the kept password was refused, and asks for a sign-in', () => {
    const markup = render((client) => {
      bridged(client)
      client.setQueryData(['bills', 'connections'], [
        {
          ...connections[0],
          connected: true,
          credential_source: 'stored',
          needs_sign_in: true,
          sign_in_paused: 'password_refused',
          last_pull_status: 'needs_sign_in',
          last_pull_error:
            'Spectrum did not accept the kept password (stopped at the password page). The password is still kept, but it will not be tried on its own again until you sign in, change it, or press Update now.',
        },
        connections[1],
      ])
    })

    expect(markup).toContain('Password refused')
    expect(markup).toContain('role="alert"')
    expect(markup).toContain('Spectrum did not accept the kept password')
    expect(markup).toContain('</svg> Sign in again</button>')
  })

  it('says whether each login is signed in, and how its last pull went', () => {
    const markup = render(bridged)

    expect(markup).toContain('Not connected')
    expect(markup).toContain('Sign in')
  })

  it('calls a connection with no name by its provider alone', () => {
    const markup = render((client) => {
      bridged(client)
      client.setQueryData(['bills', 'connections'], [
        { ...connections[0], label: '' },
        connections[1],
      ])
    })

    expect(markup).toContain('<strong>Spectrum</strong>')
    expect(markup).not.toContain('Spectrum · </strong>')
  })

  it('says why a provider that texts a code is not pulled while nobody is up', () => {
    // An SMS provider with no named hour is skipped by the scheduler, and the
    // card must say so.
    expect(render(bridged)).toContain(
      'Updated only when you press Update now: Spectrum asks for a text code',
    )
  })

  it('offers the code request, not a sign-in, to a connection waiting on one', () => {
    const markup = render((client) => {
      bridged(client)
      client.setQueryData(
        ['bills', 'connections'],
        connections.map((one) =>
          one.id === 'conn-power' ? { ...one, connected: true, last_pull_status: 'challenge' } : one,
        ),
      )
      client.setQueryData(['bills', 'challenges', 'waiting'], [challenge])
    })

    expect(markup).toContain('Code needed')
    expect(markup).toContain('Answer the code request')
  })

  it('says a provider with no sign-in has none, rather than offering a dead button', () => {
    // Apple bills by receipt mail: there is nothing to connect to, and a
    // disabled Connect button would read as something that is broken.
    const markup = render(bridged)
    expect(markup).toContain('bills come by email')
    expect(markup).toContain('no sign-in needed')
  })

  it('leaves the mailbox and its rules to the Email page', () => {
    const markup = render(bridged)

    expect(markup).not.toContain('Mailed bills, sign-in codes and mail rules')
    expect(markup).not.toContain('Add mailbox')
    expect(markup).not.toContain('Mail rules</h3>')
  })

  it('names the reason signing in is off when this build has no engine', () => {
    const markup = render((client) => {
      seeded(client)
      client.setQueryData(['bills', 'agent'], { ...agent, configured: false, providers: [] })
      client.setQueryData(['bills', 'challenges', 'waiting'], [])
    })

    expect(markup).toContain('This build carries no browser engine')
    // Said once for the page, not once per card: a card's disabled button
    // carries it only as its tooltip.
    expect(markup.match(/>This build carries no browser engine/g)).toHaveLength(1)
  })
})
