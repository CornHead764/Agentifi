/**
 * Asking the assistant about one row. `subjectLine` is for the person and may
 * carry figures; `questionText` is for the model and carries **no figures at
 * all** — only the row's identity and paths for `read_endpoint` — so every
 * number in an answer comes from a tool call against the ledger, never from
 * the screen.
 */

import { formatDate } from '@/lib/format'
import { formatMoney, type Money, type MoneyFormatter } from '@/lib/money'

export type AskKind =
  | 'transaction'
  | 'holding'
  | 'account'
  | 'series'
  | 'rule'
  | 'guidance'
  | 'goal'
  | 'watchlist'
  | 'bucket'
  | 'bill'
  | 'order'
  | 'asset'
  | 'category'
  | 'tag'
  | 'networth'
  | 'portfolio'

/** `reads` is the only context that reaches the prompt; the rest is for the summary line. */
export interface AskSubject {
  kind: AskKind
  id?: string | null
  name: string
  /** Shown to the person, never sent. */
  amount?: Money | null
  /** As the wire carries it. Shown to the person, never sent. */
  date?: string | null
  detail?: readonly (string | null | undefined)[]
  /** Paths for `read_endpoint`, most specific first. */
  reads?: readonly string[]
}

const KIND_NOUN: Record<AskKind, string> = {
  transaction: 'transaction',
  holding: 'investment holding',
  account: 'account',
  series: 'recurring item',
  rule: 'rule',
  guidance: 'guidance note',
  goal: 'savings goal',
  watchlist: 'watchlist',
  bucket: 'spending plan bucket',
  bill: 'upcoming bill',
  order: 'merchant order',
  asset: 'asset',
  category: 'category',
  tag: 'tag',
  networth: 'net worth summary',
  portfolio: 'investment portfolio',
}

export function kindNoun(kind: AskKind): string {
  return KIND_NOUN[kind]
}

/** The row in one line; a date outside the current year keeps its year. */
export function subjectLine(
  subject: AskSubject,
  today: Date = new Date(),
  money: MoneyFormatter = formatMoney,
): string {
  const parts: string[] = [subject.name.trim()]
  if (subject.amount !== null && subject.amount !== undefined) {
    parts.push(money(subject.amount))
  }
  if (subject.date) parts.push(formatDate(subject.date, dateStyleFor(subject.date, today)))
  for (const one of subject.detail ?? []) {
    const text = one?.trim()
    if (text) parts.push(text)
  }
  return parts.filter((part) => part !== '').join(', ')
}

function dateStyleFor(iso: string, today: Date): 'short' | 'full' {
  return iso.slice(0, 4) === String(today.getFullYear()) ? 'short' : 'full'
}

/**
 * Questions about what the ledger shows, never about what to do: the system
 * prompt forbids recommendations.
 */
const SUGGESTIONS: Record<AskKind, readonly string[]> = {
  transaction: [
    'Why is this categorized the way it is?',
    'How much do I usually spend here?',
    'Has this payee changed its prices?',
    'Is there anything unusual about this charge?',
  ],
  holding: [
    'What is this position worth, and what has the day done to it?',
    'How does its gain compare with its cost basis?',
    'How much of my portfolio is this?',
  ],
  account: [
    'What has moved through this account lately?',
    'Where does most of its spending go?',
    'How has its balance changed this year?',
  ],
  series: [
    'Has the amount of this changed over time?',
    'When is it next due, and has it ever been missed?',
    'What have I paid for this in the last year?',
  ],
  rule: ['What does this rule catch?', 'Does anything overlap with this rule?'],
  guidance: ['What has this guidance changed?', 'When was it last applied?'],
  goal: ['How far along is this goal?', 'At this rate, when would it be funded?'],
  watchlist: ['What is in this watchlist this month?', 'How does that compare with last month?'],
  bucket: [
    'What is in this bucket this month?',
    'How does this month compare with the last few?',
    'What is the biggest thing in it?',
  ],
  bill: ['What was this last time?', 'Has this bill gone up?'],
  order: ['What was in this order?', 'Which charge does this match?'],
  asset: ['How has this been valued over time?', 'What is it worth now?'],
  category: ['What have I spent in this category?', 'What are the biggest items in it?'],
  tag: ['What is tagged with this?', 'How much does it add up to?'],
  networth: [
    'What is my debt-to-asset ratio, and what sits on each side of it?',
    'How fast is my net worth growing?',
    'What changed this month, and which accounts moved it most?',
    'Which accounts matter most to the total?',
    'What is the trend in my credit card debt?',
    'Where would my net worth be in 10 years if recent growth continued?',
  ],
  portfolio: [
    'Summarize my portfolio: what is it worth, and what has the day done to it?',
    'Which positions account for most of it?',
    'How do my gains compare with my cost basis?',
  ],
}

export function suggestedQuestions(kind: AskKind): readonly string[] {
  return SUGGESTIONS[kind]
}

/** Identity and paths, then the person's words; every figure must come from `read_endpoint`. */
export function questionText(subject: AskSubject, question: string): string {
  const asked = question.trim()
  const lines: string[] = []
  const noun = kindNoun(subject.kind)
  lines.push(`I am looking at one ${noun} in my ledger: ${subject.name.trim()}.`)
  if (subject.id) lines.push(`Its id is ${subject.id}.`)

  const reads = (subject.reads ?? []).filter((path) => path.trim() !== '')
  if (reads.length > 0) {
    lines.push(
      `Read it before answering — call read_endpoint on ${joinPaths(reads)} — and read ` +
        'whatever else you need from there. Every figure you give me must come from a ' +
        'tool call, not from this message.',
    )
  } else {
    lines.push(
      'Find it with the tools before answering. Every figure you give me must come from a ' +
        'tool call, not from this message.',
    )
  }
  lines.push(
    'Answer only what I ask, describe what the figures show, and do not recommend anything.',
  )
  lines.push('')
  lines.push(`My question: ${asked}`)
  return lines.join('\n')
}

function joinPaths(paths: readonly string[]): string {
  if (paths.length === 1) return paths[0]
  return `${paths.slice(0, -1).join(', ')} and ${paths[paths.length - 1]}`
}

export function askTitle(kind: AskKind): string {
  return `Ask about this ${kindNoun(kind)}`
}
