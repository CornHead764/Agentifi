/**
 * A connector's failure as a person reads it: known shapes become a sentence
 * naming the next step, with the engine's raw text kept for Details. A message
 * the provider's own site wrote is already a sentence and is kept.
 */

import { asSentence } from '@/lib/format'

export interface ConnectorFailure {
  message: string
  /** The engine's own words, when the message is not them. */
  detail: string | null
}

const RULES: readonly { match: RegExp; say: (provider: string) => string }[] = [
  {
    match: /did not accept the kept password/i,
    say: (p) =>
      `${p} did not accept the kept password. It is still kept but is not tried again on its own: sign in again, or press Update now to try it once more.`,
  },
  {
    match: /sign in to (answer|approve) it/i,
    say: (p) =>
      `${p} asked for a code. Sign in again to answer it; until then updates do not sign in on their own.`,
  },
  {
    match: /^there is no page to sign in on/i,
    say: (p) => `${p}'s sign-in page did not open. Try again in a few minutes.`,
  },
  { match: /^no password given/i, say: (p) => `Enter your ${p} password and try again.` },
  {
    match: /sign-in loop|sign-in form came back/i,
    say: (p) =>
      `${p} kept returning to its sign-in form. Check the email and password, then try again.`,
  },
  {
    match: /\b403\b|akamai|cloudflare|turnstile|access denied/i,
    say: (p) =>
      `${p}'s website refused the sign-in. Try again later.`,
  },
  {
    match: /^unrecognised factor page|^unanswerable state/i,
    say: (p) =>
      `${p} asked for a kind of verification this app cannot answer yet. Sign in on ${p}'s own site to check the account, then try again.`,
  },
  {
    match: /^unrecognised page/i,
    say: (p) =>
      `${p} showed a page this app does not recognise, so it stopped. Try again; if it keeps happening, ${p}'s site has probably changed.`,
  },
  {
    match: /deadline exceeded|timed? ?out/i,
    say: (p) => `${p} took too long to answer. Try again in a few minutes.`,
  },
  {
    match: /no such host|connection refused|connection reset|dial tcp|\bEOF\b/i,
    say: (p) =>
      `The server could not reach ${p}. Try again later; if it persists, whoever runs Agentifi should check the server's connection.`,
  },
  {
    match: /camoufox|playwright|websocket|browser.*(unavailable|not running|closed)/i,
    say: (p) =>
      `The browser that signs in to ${p} is not available on the server. Whoever runs Agentifi should check it.`,
  },
]

/** Plain words written for a person, rather than an error chain, an address or code. */
function readsAsSentence(raw: string): boolean {
  return !/https?:\/\//.test(raw) && !/\w: \w/.test(raw) && !/[_{}[\]<>=]/.test(raw) && raw.length < 160
}


export function explainConnectorFailure(raw: string, provider: string): ConnectorFailure {
  const text = raw.trim()
  if (text === '') return { message: `Something went wrong talking to ${provider}.`, detail: null }
  const rule = RULES.find((one) => one.match.test(text))
  if (rule) return { message: rule.say(provider), detail: text }
  if (readsAsSentence(text)) return { message: asSentence(text), detail: null }
  return {
    message: `Something went wrong talking to ${provider}. Try again; if it keeps failing, the details help a bug report.`,
    detail: text,
  }
}
