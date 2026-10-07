/**
 * Reading an exported value history (date, value). Forgiving about the header,
 * strict about values: an unparseable row is reported, never skipped.
 */

import { parseIsoDay } from './format'
import { parseAmountInput } from './money'

export interface ValuePoint {
  on: string
  value: string
}

export interface ParsedHistory {
  points: ValuePoint[]
  /** 1-based line numbers. */
  rejected: number[]
}

const DATE_HEADERS = ['date', 'on', 'as_of', 'as of', 'day']
const VALUE_HEADERS = ['value', 'amount', 'balance', 'worth', 'estimate']

/** A recognised header is skipped; without one, columns are date then value. */
export function parseValueHistory(text: string): ParsedHistory {
  const lines = text.split(/\r?\n/)
  const points: ValuePoint[] = []
  const rejected: number[] = []

  let dateAt = 0
  let valueAt = 1
  let start = 0

  const first = splitRow(lines[0] ?? '')
  const headers = first.map((cell) => cell.trim().toLowerCase())
  const headerDate = headers.findIndex((cell) => DATE_HEADERS.includes(cell))
  const headerValue = headers.findIndex((cell) => VALUE_HEADERS.includes(cell))
  if (headerDate !== -1 && headerValue !== -1) {
    dateAt = headerDate
    valueAt = headerValue
    start = 1
  }

  for (let i = start; i < lines.length; i++) {
    const line = lines[i]
    if (line.trim() === '') continue
    const cells = splitRow(line)
    const on = isoDate(cells[dateAt] ?? '')
    const value = money(cells[valueAt] ?? '')
    if (on === null || value === null) {
      rejected.push(i + 1)
      continue
    }
    points.push({ on, value })
  }
  return { points, rejected }
}

/** Split one row, respecting quotes, so `"$310,000.55"` stays one cell. */
function splitRow(line: string): string[] {
  const cells: string[] = []
  let cell = ''
  let quoted = false

  for (let i = 0; i < line.length; i++) {
    const char = line[i]
    if (quoted) {
      if (char !== '"') {
        cell += char
      } else if (line[i + 1] === '"') {
        cell += '"'
        i++
      } else {
        quoted = false
      }
      continue
    }
    if (char === '"') {
      quoted = true
    } else if (char === ',' || char === '\t' || char === ';') {
      cells.push(cell.trim())
      cell = ''
    } else {
      cell += char
    }
  }
  cells.push(cell.trim())
  return cells
}

/** Only the unambiguous forms. `03/04/2025` is two dates and neither is safe. */
function isoDate(cell: string): string | null {
  return parseIsoDay(cell) !== null ? cell.trim() : null
}

/** A cell's figure, never through a float; grouping, a currency symbol and accounting parens are all `parseAmountInput`'s job. */
function money(cell: string): string | null {
  return parseAmountInput(cell)?.wire ?? null
}
