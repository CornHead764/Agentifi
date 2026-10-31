import { describe, expect, it } from 'vitest'

import type { BillConnection } from '@/lib/clients/bills'
import type { EmailConnection } from '@/lib/clients/email'
import type { MerchantAccount } from '@/lib/clients/merchant'

import {
  billPullOutcome,
  canMinimize,
  cancelLabel,
  clearsAfter,
  leaving,
  merchantPullOutcome,
  phaseOfStep,
  pillOf,
  PILL_CLEARS_AFTER_MS,
  signInReducer,
  trayOf,
  type SignInAction,
  type SignInTarget,
  type SignInTask,
} from './signInTasks'

/*
 * The sign-ins the shell holds, as pure functions: what a pill says, what
 * minimizing and closing do, and what the first pull after a sign-in reads
 * as. Every login here is invented.
 */

const power = {
  id: 'conn-power',
  biller: 'we-energies',
  label: 'Main account',
} as BillConnection

const cabin = { ...power, id: 'conn-cabin', label: 'Second property' } as BillConnection

const billAt = (connection: BillConnection): SignInTarget => ({
  kind: 'bill',
  connection,
  provider: null,
})

const amazon = { id: 'acct-alex', name: 'Alex' } as MerchantAccount

const mailbox = { id: 'mail-1', label: 'Household mail', kind: 'graph' } as EmailConnection

function run(...actions: SignInAction[]): SignInTask[] {
  return actions.reduce<SignInTask[]>(signInReducer, [])
}

describe('the sign-ins the shell holds', () => {
  it('opens one on screen, named for the company', () => {
    const [task] = run({ type: 'open', id: 'a', target: billAt(power) })
    expect(task).toMatchObject({ id: 'a', name: 'We Energies', minimized: false, phase: 'form' })
  })

  it('brings back the one already under way rather than starting a second', () => {
    const tasks = run(
      { type: 'open', id: 'a', target: billAt(power) },
      { type: 'phase', id: 'a', phase: 'code', note: '' },
      { type: 'minimize', id: 'a' },
      { type: 'open', id: 'b', target: billAt(power) },
    )
    expect(tasks).toHaveLength(1)
    expect(tasks[0]).toMatchObject({ id: 'a', minimized: false, phase: 'code' })
  })

  it('restores a minimized sign-in on the step it was left at', () => {
    const minimized = run(
      { type: 'open', id: 'a', target: billAt(power) },
      { type: 'phase', id: 'a', phase: 'approval', note: 'Example Utility: approve on your phone' },
      { type: 'minimize', id: 'a' },
    )
    expect(minimized[0].minimized).toBe(true)
    const restored = signInReducer(minimized, { type: 'restore', id: 'a' })
    expect(restored).toEqual([{ ...minimized[0], minimized: false }])
  })

  it('holds several at once, and puts the one on screen away when another comes up', () => {
    const tasks = run(
      { type: 'open', id: 'a', target: billAt(power) },
      { type: 'phase', id: 'a', phase: 'working', note: '' },
      { type: 'open', id: 'b', target: billAt(cabin) },
      { type: 'phase', id: 'b', phase: 'working', note: '' },
      { type: 'minimize', id: 'b' },
      { type: 'open', id: 'c', target: { kind: 'merchant', merchant: 'amazon', account: amazon } },
    )
    expect(tasks.map((task) => [task.id, task.minimized])).toEqual([
      ['a', true],
      ['b', true],
      ['c', false],
    ])
    expect(trayOf(tasks).map((task) => task.id)).toEqual(['a', 'b'])
  })

  it('closes a dialog nobody had started when another comes up, rather than making it a pill', () => {
    const tasks = run(
      { type: 'open', id: 'a', target: billAt(power) },
      { type: 'open', id: 'b', target: billAt(cabin) },
    )
    expect(tasks.map((task) => task.id)).toEqual(['b'])
  })

  it('keeps two shop accounts being added apart, since neither has an id yet', () => {
    const adding: SignInTarget = { kind: 'merchant', merchant: 'costco', account: null }
    const tasks = run(
      { type: 'open', id: 'a', target: adding },
      { type: 'phase', id: 'a', phase: 'working', note: '' },
      { type: 'open', id: 'b', target: adding },
    )
    expect(tasks.map((task) => task.id)).toEqual(['a', 'b'])
  })

  it('forgets a dismissed sign-in', () => {
    expect(
      run({ type: 'open', id: 'a', target: billAt(power) }, { type: 'dismiss', id: 'a' }),
    ).toEqual([])
  })
})

describe('leaving a sign-in dialog', () => {
  it('puts a running sign-in away on close, and never gives it up', () => {
    for (const phase of ['working', 'code', 'approval', 'fetching'] as const) {
      expect(leaving(phase, 'close')).toEqual({ cancel: false, then: 'minimize' })
    }
  })

  it('dismisses on close when nothing is running for a pill to stand in for', () => {
    for (const phase of ['form', 'failed'] as const) {
      expect(leaving(phase, 'close')).toEqual({ cancel: true, then: 'dismiss' })
    }
    expect(leaving('finished', 'close')).toEqual({ cancel: false, then: 'dismiss' })
    expect(leaving('stopped', 'close')).toEqual({ cancel: false, then: 'dismiss' })
  })

  it('gives the sign-in up on cancel while it is still signing in', () => {
    for (const phase of ['form', 'working', 'code', 'approval', 'failed'] as const) {
      expect(leaving(phase, 'cancel')).toEqual({ cancel: true, then: 'dismiss' })
    }
  })

  it('only dismisses on cancel once the pull is running, since the server runs that regardless', () => {
    for (const phase of ['fetching', 'finished', 'stopped'] as const) {
      expect(leaving(phase, 'cancel')).toEqual({ cancel: false, then: 'dismiss' })
    }
  })

  it('knows when something is running to walk away from', () => {
    expect(canMinimize('working')).toBe(true)
    expect(canMinimize('code')).toBe(true)
    expect(canMinimize('approval')).toBe(true)
    expect(canMinimize('fetching')).toBe(true)
    expect(canMinimize('form')).toBe(false)
    expect(canMinimize('failed')).toBe(false)
    expect(canMinimize('finished')).toBe(false)
  })

  it('names the cancel button for what it gives up', () => {
    expect(cancelLabel('code')).toBe('Cancel sign-in')
    expect(cancelLabel('form')).toBe('Cancel')
  })
})

describe('the phase of a provider step', () => {
  it('reads a call in flight as work, whatever the step said', () => {
    expect(phaseOfStep(null, true)).toBe('working')
    expect(phaseOfStep('otp', true)).toBe('working')
    expect(phaseOfStep('failed', true)).toBe('working')
  })

  it('reads what the provider is waiting on', () => {
    expect(phaseOfStep(null, false)).toBe('form')
    expect(phaseOfStep('signing_in', false)).toBe('working')
    expect(phaseOfStep('otp', false)).toBe('code')
    expect(phaseOfStep('captcha', false)).toBe('code')
    expect(phaseOfStep('interactive', false)).toBe('code')
    expect(phaseOfStep('approval', false)).toBe('approval')
    expect(phaseOfStep('accounts', false)).toBe('approval')
    expect(phaseOfStep('signed_in', false)).toBe('working')
  })

  it('reads a failure, and a state no dialog draws, as failed', () => {
    expect(phaseOfStep('failed', false)).toBe('failed')
    expect(phaseOfStep('email', false)).toBe('failed')
  })
})

describe('what a pill says', () => {
  const task = (over: Partial<SignInTask>): SignInTask => ({
    id: 'a',
    target: billAt(power),
    name: 'Example Utility',
    minimized: true,
    phase: 'working',
    note: '',
    ...over,
  })

  it('spins while the server is signing in or fetching', () => {
    expect(pillOf(task({ phase: 'working' }))).toEqual({
      tone: 'working',
      text: 'Example Utility: signing in…',
      spinning: true,
    })
    expect(pillOf(task({ phase: 'fetching' }))).toEqual({
      tone: 'working',
      text: 'Signed in to Example Utility — fetching bills…',
      spinning: true,
    })
  })

  it('asks for attention when the sign-in is waiting on somebody', () => {
    expect(pillOf(task({ phase: 'code' }))).toMatchObject({
      tone: 'attention',
      text: 'Example Utility needs a code',
      spinning: false,
    })
    expect(pillOf(task({ phase: 'approval' }))).toMatchObject({
      tone: 'attention',
      text: 'Example Utility: approve on your phone',
    })
  })

  it('says what a shop fetches in its own word', () => {
    const costco = task({
      name: 'Costco',
      phase: 'fetching',
      target: { kind: 'merchant', merchant: 'costco', account: null },
    })
    expect(pillOf(costco).text).toBe('Signed in to Costco — fetching purchases…')
  })

  it("prefers the dialog's own sentence when it has one", () => {
    expect(
      pillOf(
        task({
          target: { kind: 'mailbox', connection: mailbox },
          name: 'Household mail',
          phase: 'approval',
          note: 'Household mail: enter ABCD-1234 at Microsoft',
        }),
      ).text,
    ).toBe('Household mail: enter ABCD-1234 at Microsoft')
  })

  it('shows how it ended, and only good news leaves by itself', () => {
    const done = task({ phase: 'finished', note: 'Fetched your bills from Example Utility.' })
    expect(pillOf(done)).toMatchObject({ tone: 'success', spinning: false })
    expect(clearsAfter(done)).toBe(PILL_CLEARS_AFTER_MS)

    const stopped = task({ phase: 'stopped', note: 'Last update failed.' })
    expect(pillOf(stopped)).toMatchObject({ tone: 'failure', text: 'Last update failed.' })
    expect(clearsAfter(stopped)).toBeNull()

    expect(pillOf(task({ phase: 'failed' }))).toMatchObject({
      tone: 'failure',
      text: 'Example Utility: sign-in failed',
    })
    expect(clearsAfter(task({ phase: 'failed' }))).toBeNull()
  })

  it('does not clear a finished dialog somebody is looking at', () => {
    expect(clearsAfter(task({ phase: 'finished', minimized: false }))).toBeNull()
  })
})

describe('how the first pull after a sign-in ended', () => {
  const describe_ = () => 'Last update failed. The statements page did not load.'
  const BILL = { id: 'c0ffee00-0000-4000-8000-000000000001', has_failure_screenshot: false }
  const ACCOUNT = { id: 'c0ffee00-0000-4000-8000-000000000002', has_failure_screenshot: false }

  it('waits while the pull is running', () => {
    expect(
      billPullOutcome({ ...BILL, pulling: true, last_pull_status: 'ok' }, 'Example Utility', describe_),
    ).toBeNull()
    expect(
      merchantPullOutcome({ ...ACCOUNT, pulling: true, last_sync_status: 'ok' }, 'amazon', describe_),
    ).toBeNull()
  })

  it('reads a pull that went well', () => {
    expect(billPullOutcome({ ...BILL, pulling: false, last_pull_status: 'ok' }, 'Example Utility', describe_)).toEqual({
      ok: true,
      note: 'Fetched your bills from Example Utility.',
    })
    expect(merchantPullOutcome({ ...ACCOUNT, pulling: false, last_sync_status: 'ok' }, 'amazon', describe_)).toEqual({
      ok: true,
      note: 'Fetched your orders from Amazon.',
    })
  })

  it("reads a pull that stopped in the connection's own words", () => {
    expect(
      billPullOutcome({ ...BILL, pulling: false, last_pull_status: 'failed' }, 'Example Utility', describe_),
    ).toEqual({ ok: false, note: describe_() })
    expect(
      merchantPullOutcome({ ...ACCOUNT, pulling: false, last_sync_status: 'needs_sign_in' }, 'costco', describe_),
    ).toEqual({ ok: false, note: describe_() })
  })

  it('points at the page a pull stopped on, when one was kept', () => {
    expect(
      billPullOutcome(
        { ...BILL, has_failure_screenshot: true, pulling: false, last_pull_status: 'failed' },
        'Example Utility',
        describe_,
      ),
    ).toEqual({
      ok: false,
      note: describe_(),
      screenshot: { path: '/bills/connections/c0ffee00-0000-4000-8000-000000000001/failure-screenshot' },
    })
    expect(
      merchantPullOutcome(
        { ...ACCOUNT, has_failure_screenshot: true, pulling: false, last_sync_status: 'failed' },
        'costco',
        describe_,
      ),
    ).toEqual({
      ok: false,
      note: describe_(),
      screenshot: {
        path: '/merchants/costco/accounts/c0ffee00-0000-4000-8000-000000000002/failure-screenshot',
      },
    })
  })

  it('marks a code the pull asked for, so the dialog can take it in place', () => {
    expect(
      billPullOutcome({ ...BILL, pulling: false, last_pull_status: 'challenge' }, 'Example Utility', describe_),
    ).toEqual({
      ok: false,
      note: 'Example Utility asked for a code to finish fetching bills.',
      asksForCode: true,
    })
  })
})
