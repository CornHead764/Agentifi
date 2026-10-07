import { describe, expect, it } from 'vitest'

import { signInStuck, signInSubmit, trailLine, trailText, type SignInStand } from './signIn'

const stand = (over: Partial<SignInStand> = {}): SignInStand => ({
  busy: false,
  state: null,
  keyProblem: false,
  ...over,
})

describe('signInSubmit', () => {
  it('sends the typed form, then the code, then keeps the session', () => {
    expect(signInSubmit(stand())).toBe('start')
    expect(signInSubmit(stand({ state: 'failed' }))).toBe('start')
    expect(signInSubmit(stand({ state: 'otp' }))).toBe('answer')
    expect(signInSubmit(stand({ state: 'captcha' }))).toBe('answer')
    expect(signInSubmit(stand({ state: 'accounts' }))).toBe('finish')
    expect(signInSubmit(stand({ state: 'signed_in' }))).toBe('finish')
    expect(signInSubmit(stand({ state: 'approval' }))).toBe('refetch')
  })

  it('treats a state it cannot draw as the failure it is', () => {
    // Neither can be typed at; showing the form again avoids posting an empty
    // code, which the provider counts towards locking the account.
    for (const state of ['interactive', 'email', 'password'] as const) {
      expect(signInStuck(state)).toBe(true)
      expect(signInSubmit(stand({ state }))).toBe('start')
      expect(signInSubmit(stand({ state, keyProblem: true }))).toBe('nothing')
    }
  })

  it('knows the states it does draw', () => {
    for (const state of [
      'signing_in',
      'otp',
      'captcha',
      'approval',
      'accounts',
      'signed_in',
      'failed',
    ] as const) {
      expect(signInStuck(state)).toBe(false)
    }
    expect(signInStuck(null)).toBe(false)
  })

  it('presses nothing while the agent is still signing in', () => {
    // The agent is filling the provider's pages; a press would post an empty
    // code into the middle of that.
    expect(signInSubmit(stand({ state: 'signing_in' }))).toBe('nothing')
    expect(signInSubmit(stand({ state: 'signing_in', keyProblem: true }))).toBe('nothing')
  })

  it('does nothing at all while a call is with the provider', () => {
    // Eleven presses in one second is one answer and ten that must not leave:
    // a provider counts a refused code towards locking the account.
    for (const state of ['otp', 'captcha', 'accounts', 'approval', null] as const) {
      expect(signInSubmit(stand({ busy: true, state }))).toBe('nothing')
    }
  })

  it('will not start a sign-in whose authenticator key is not one', () => {
    expect(signInSubmit(stand({ keyProblem: true }))).toBe('nothing')
    expect(signInSubmit(stand({ keyProblem: true, state: 'failed' }))).toBe('nothing')
  })
})

const didNothing = { acted: false, pressed: '', words: '', waited: false, changed: false }

describe('trailLine', () => {
  it('reads as one line somebody can paste', () => {
    const line = trailLine({
      at: '2026-09-19T20:33:52.000Z',
      step: 'sign-in',
      state: 'password',
      url: 'https://login.example.test/sso',
      title: 'Sign On',
      form: { password: true, username: true, otp: false, sign_out_link: false },
      inputs: { email: 1, password: 1, checkbox: 1 },
      error: 'That password was not recognized. Try again.',
      did: { acted: true, pressed: 'button', words: 'LOG IN', waited: false, changed: true },
    })

    expect(line).toContain('sign-in → password')
    expect(line).toContain('https://login.example.test/sso')
    expect(line).toContain('“Sign On”')
    expect(line).toContain('showing: password box, username box')
    expect(line).toContain('boxes: email 1, password 1, checkbox 1')
    expect(line).toContain('said: That password was not recognized.')
    expect(line).toContain('did: pressed the button “LOG IN”')
    expect(line).toContain('the page changed')
    expect(line).not.toContain('\n')
  })

  it('says that a round pressed nothing and that the page did not move', () => {
    const line = trailLine({
      at: '2026-09-20T20:28:51.000Z',
      step: 'sign-in',
      state: 'email',
      url: 'https://account.example.test/signin/v2/',
      title: 'Log In',
      form: { password: false, username: true, otp: false, sign_out_link: false },
      inputs: { text: 1 },
      error: '',
      did: { acted: true, pressed: 'enter', words: '', waited: false, changed: false },
    })

    expect(line).toContain('did: pressed Enter, nothing on the page matched a button')
    expect(line).toContain('the page did not change')
  })

  it('says when a matched button never became pressable and Enter was pressed', () => {
    const line = trailLine({
      at: '',
      step: 'sign-in',
      state: 'password',
      url: 'https://account.example.test/login',
      title: 'Sign In',
      form: { password: true, username: true, otp: false, sign_out_link: false },
      inputs: { text: 1, password: 1 },
      error: '',
      did: { acted: true, pressed: 'enter', words: 'Sign in', waited: true, changed: false },
    })

    expect(line).toContain('did: pressed Enter, the “Sign in” button never became pressable')
    expect(line).not.toContain('not pressable at first')
  })

  it('says when a control had to become pressable first', () => {
    const line = trailLine({
      at: '',
      step: 'sign-in',
      state: 'email',
      url: '',
      title: '',
      form: { password: false, username: true, otp: false, sign_out_link: false },
      inputs: { text: 1 },
      error: '',
      did: { acted: true, pressed: 'button', words: 'Next', waited: true, changed: true },
    })

    expect(line).toContain('did: pressed the button “Next”, which was not pressable at first')
  })

  it('says when a round declined a cookie banner before it typed', () => {
    const line = trailLine({
      at: '',
      step: 'sign-in',
      state: 'password',
      url: '',
      title: '',
      form: { password: true, username: true, otp: false, sign_out_link: false },
      inputs: { text: 1, password: 1 },
      error: '',
      did: {
        acted: true,
        pressed: 'button',
        words: 'Log In',
        waited: false,
        changed: true,
        dismissed: 'OneTrust “Opt Out”',
      },
    })

    expect(line).toContain(
      'did: dismissed the cookie banner (OneTrust “Opt Out”), then pressed the button “Log In”',
    )
  })

  it('says what a factor page was offering, and which of it was taken', () => {
    const line = trailLine({
      at: '2026-09-20T21:26:53.000Z',
      step: 'sign-in',
      state: 'factor',
      url: 'https://account.example.test/sap/v2/actions/choosemethod',
      title: '',
      form: { password: false, username: false, otp: false, sign_out_link: false },
      inputs: { radio: 3 },
      error: '',
      choices: [
        { kind: 'radio', words: 'Text message' },
        { kind: 'radio', words: 'Authenticator app' },
        { kind: 'radio', words: 'Security key' },
      ],
      chose: 'Authenticator app',
      did: { acted: true, pressed: 'button', words: 'Continue', waited: false, changed: true },
    })

    expect(line).toContain('boxes: radio 3')
    expect(line).toContain(
      'offered: “Text message” (radio), “Authenticator app” (radio), “Security key” (radio)',
    )
    expect(line).toContain('chose: “Authenticator app”')
    expect(line).toContain('did: pressed the button “Continue”')
  })

  it('says when a choice had to be forced', () => {
    const line = trailLine({
      at: '',
      step: 'sign-in',
      state: 'factor',
      url: '',
      title: '',
      form: { password: false, username: false, otp: false, sign_out_link: false },
      inputs: { radio: 2 },
      error: '',
      choices: [
        { kind: 'radio', words: 'Text me a code' },
        { kind: 'radio', words: 'Use Google Authenticator' },
      ],
      chose: 'Use Google Authenticator',
      forced: true,
      did: didNothing,
    })

    expect(line).toContain('chose: “Use Google Authenticator”, forced')
  })

  it('says a menu was offered even when nothing on it was taken', () => {
    const line = trailLine({
      at: '',
      step: 'sign-in',
      state: 'factor',
      url: '',
      title: '',
      form: { password: false, username: false, otp: false, sign_out_link: false },
      inputs: { radio: 3 },
      error: '',
      choices: [
        { kind: 'radio', words: 'Text message' },
        { kind: 'radio', words: 'Email' },
      ],
      chose: '',
      did: didNothing,
    })

    expect(line).toContain('offered: “Text message” (radio), “Email” (radio)')
    expect(line).not.toContain('chose:')
  })

  it('leaves out what the round did not have', () => {
    const line = trailLine({
      at: '',
      step: 'answer',
      state: '',
      url: '',
      title: '',
      form: { password: false, username: false, otp: false, sign_out_link: false },
      inputs: {},
      error: '',
      did: didNothing,
    })
    expect(line).toBe('answer → nothing')
  })
})

describe('a preference the menu did not offer', () => {
  it('is said on the line that showed the menu', () => {
    const line = trailLine({
      at: '',
      step: 'sign-in',
      state: 'factor',
      url: '',
      title: '',
      form: { password: false, username: false, otp: false, sign_out_link: false },
      inputs: {},
      error: '',
      did: { acted: false, pressed: '', words: '', waited: false, changed: false },
      note: 'preferred e-mail was not offered; chose a text',
    })
    expect(line).toContain('note: preferred e-mail was not offered; chose a text')
  })
})

describe('trailText', () => {
  const round = (step: string, snapshot?: string) => ({
    at: '',
    step,
    state: 'failed',
    url: '',
    title: '',
    form: { password: false, username: false, otp: false, sign_out_link: false },
    inputs: {},
    error: '',
    did: { acted: false, pressed: '', words: '', waited: false, changed: false },
    snapshot,
  })

  it('puts each page under the line that took it', () => {
    expect(
      trailText([
        round('sign-in'),
        round('read', 'headings: Billing Summary\ncontrols:\n  a href=/Portal/Billing/Details'),
        round('read'),
      ]),
    ).toBe(
      [
        'sign-in → failed',
        'read → failed',
        '    headings: Billing Summary',
        '    controls:',
        '      a href=/Portal/Billing/Details',
        'read → failed',
      ].join('\n'),
    )
  })

  it('is a line per round for a trail that carries no page', () => {
    expect(trailText([round('sign-in'), round('answer')])).toBe('sign-in → failed\nanswer → failed')
    expect(trailText([])).toBe('')
  })

  it('never goes into the one-line trail itself', () => {
    expect(trailLine(round('failed', 'controls:\n  button "Go"'))).not.toContain('controls')
  })
})
