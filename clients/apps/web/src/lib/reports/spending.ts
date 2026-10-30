/**
 * Presentation over `GET /reports/spending`. Every figure arrives computed
 * (`backend/internal/domain/spendinganalysis.go`); this orders lines, names
 * periods and comparisons, and lays the bars, the table's shading and the
 * flow out in pixels.
 */

import type { CSSProperties } from 'react'

import type {
  SpendingChartPeriod,
  SpendingCompare,
  SpendingComparison,
  SpendingFlow,
  SpendingGrain,
  SpendingGroup,
  SpendingPeriod,
  SpendingRow,
  SpendingTableRow,
} from '@/lib/clients/reports'
import { formatDate, parseIsoDate } from '@/lib/format'
import { ZERO_MONEY, absMoney, moneyToNumber, subMoney, type Money } from '@/lib/money'
import { matchesSearch } from '@/lib/search'

export const GRAINS: readonly { value: SpendingGrain; label: string }[] = [
  { value: 'month', label: 'Month' },
  { value: 'quarter', label: 'Quarter' },
  { value: 'year', label: 'Year' },
]

export const GROUPS: readonly { value: SpendingGroup; label: string; noun: string }[] = [
  { value: 'tag', label: 'Tags', noun: 'tags' },
  { value: 'payee', label: 'Payees', noun: 'payees' },
  { value: 'category', label: 'Categories', noun: 'categories' },
]

/** The column heading for a line: "Category", "Payee", "Tag". */
export function groupHeading(group: SpendingGroup): string {
  return group === 'category' ? 'Category' : group === 'payee' ? 'Payee' : 'Tag'
}

const UNIT: Record<SpendingGrain, string> = { month: 'month', quarter: 'quarter', year: 'year' }

/** The Compare menu's words for one option under one grain. */
export function compareLabel(compare: SpendingCompare, grain: SpendingGrain): string {
  switch (compare) {
    case 'same_last_year':
      return grain === 'quarter' ? 'Same quarter last year' : 'Same period last year'
    case 'prior':
      return `Prior ${UNIT[grain]}`
    case 'ytd_average':
      return 'Year to date average'
    case 'none':
      return 'Don’t compare'
    default:
      return `${compare.replace('average_', '')}-${UNIT[grain]} average`
  }
}

function quarterOf(iso: string): number {
  return Math.floor(parseIsoDate(iso).getMonth() / 3) + 1
}

/** The selected period's name: "October 2026", "Q4 2026", "2026". */
export function periodTitle(period: Pick<SpendingPeriod, 'from'>, grain: SpendingGrain): string {
  const year = parseIsoDate(period.from).getFullYear()
  if (grain === 'year') return String(year)
  if (grain === 'quarter') return `Q${quarterOf(period.from)} ${year}`
  return formatDate(period.from, 'monthLong')
}

/**
 * A bar's label, starred while the period is still running. Months name
 * their year only at January, so twelve labels stay short and distinct.
 */
export function periodTick(period: Pick<SpendingPeriod, 'from' | 'partial'>, grain: SpendingGrain): string {
  const star = period.partial ? '*' : ''
  const on = parseIsoDate(period.from)
  if (grain === 'year') return `${on.getFullYear()}${star}`
  if (grain === 'quarter') return `Q${quarterOf(period.from)} ${on.getFullYear()}${star}`
  const month = formatDate(period.from, on.getMonth() === 0 ? 'month' : 'monthShort')
  return `${month}${star}`
}

/** "Sep 1 – 3, 2026", "Jan 1 – Oct 3, 2026", "Dec 1, 2025 – Jan 3, 2026". */
export function dayRange(from: string, through: string): string {
  const start = parseIsoDate(from)
  const end = parseIsoDate(through)
  if (from === through) return formatDate(from, 'full')
  if (start.getFullYear() !== end.getFullYear()) {
    return `${formatDate(from, 'full')} – ${formatDate(through, 'full')}`
  }
  if (start.getMonth() === end.getMonth()) {
    return `${formatDate(from, 'short')} – ${end.getDate()}, ${end.getFullYear()}`
  }
  return `${formatDate(from, 'short')} – ${formatDate(through, 'full')}`
}

/** What the comparison column is against: a range, or the average's name. */
export function comparisonCaption(comparison: SpendingComparison, grain: SpendingGrain): string {
  if (comparison.average) return compareLabel(comparison.compare, grain)
  const only = comparison.periods[0]
  return only ? dayRange(only.from, only.through) : compareLabel(comparison.compare, grain)
}

/** The Compare chip's tooltip: both sides of the comparison as dates. */
export function comparisonSentence(
  period: Pick<SpendingPeriod, 'from' | 'through'>,
  comparison: SpendingComparison,
): string {
  const selected = dayRange(period.from, period.through)
  const windows = comparison.periods.map((one) => dayRange(one.from, one.through))
  if (!comparison.average) return `Comparing ${selected} to ${windows[0] ?? ''}`
  return `Comparing ${selected} to the average of ${windows.length} periods: ${windows.join('; ')}`
}

/** Spend as a positive figure: the stored sign flipped, so a credit is negative spend. */
export function spendOf(amount: Money): Money {
  return subMoney(ZERO_MONEY, amount)
}

export type SpendingSort = 'largest' | 'smallest' | 'az' | 'za'

export const SORTS: readonly { value: SpendingSort; label: string }[] = [
  { value: 'largest', label: 'Largest spent' },
  { value: 'smallest', label: 'Smallest spent' },
  { value: 'az', label: 'Alphabetical A–Z' },
  { value: 'za', label: 'Alphabetical Z–A' },
]

/** The lines in a sort's order; spend ties fall back to the comparison, then the name. */
export function sortRows<T extends Pick<SpendingRow, 'label' | 'amount'> & { comparison?: Money }>(
  rows: readonly T[],
  sort: SpendingSort,
): T[] {
  const byName = (a: T, b: T) => a.label.localeCompare(b.label)
  return [...rows].sort((a, b) => {
    switch (sort) {
      case 'az':
        return byName(a, b)
      case 'za':
        return byName(b, a)
      case 'smallest':
        return b.amount - a.amount || (b.comparison ?? 0) - (a.comparison ?? 0) || byName(a, b)
      default:
        return a.amount - b.amount || (a.comparison ?? 0) - (b.comparison ?? 0) || byName(a, b)
    }
  })
}

/** The difference column's own order: the largest increase in spend first. */
export function sortByDifference<T extends Pick<SpendingRow, 'label' | 'difference'>>(
  rows: readonly T[],
  direction: 'asc' | 'desc',
): T[] {
  const sign = direction === 'desc' ? -1 : 1
  return [...rows].sort(
    (a, b) => sign * (a.difference.amount - b.difference.amount) || a.label.localeCompare(b.label),
  )
}

export function searchRows<T extends { label: string }>(rows: readonly T[], search: string): T[] {
  return rows.filter((row) => matchesSearch(search, row.label))
}

interface BarStyle extends CSSProperties {
  '--bar': string
}

/** A line's colour, which its two bars read as `--bar`. */
export const barStyle = (color: string): BarStyle => ({ '--bar': color })

/** The scale the bars share: the largest net on any line, spend or credit, either side of the comparison. */
export function barScale(rows: readonly Pick<SpendingRow, 'amount' | 'comparison'>[]): Money {
  let most = ZERO_MONEY
  for (const row of rows) {
    for (const value of [absMoney(row.amount), absMoney(row.comparison)]) {
      if (value > most) most = value
    }
  }
  return most
}

/** A bar's length as a percent of the scale, a net credit by its size; nothing draws no bar. */
export function barPercent(amount: Money, scale: Money): number {
  const size = absMoney(amount)
  if (size <= 0 || scale <= 0) return 0
  return Math.min(100, (size / scale) * 100)
}

/** Which of the table's four shades a cell takes: none for no spend or a credit. */
export function heatLevel(amount: Money, most: Money): number {
  const spend = spendOf(amount)
  if (spend <= 0 || most <= 0) return 0
  return Math.min(4, Math.max(1, Math.ceil((spend / most) * 4)))
}

/** The most spent in any one cell of the table, which the shading is relative to. */
export function heatScale(rows: readonly Pick<SpendingTableRow, 'cells'>[]): Money {
  let most = ZERO_MONEY
  for (const row of rows) {
    for (const cell of row.cells) {
      const spend = spendOf(cell)
      if (spend > most) most = spend
    }
  }
  return most
}

/** A table column to sort by: a period's index, the total, the difference or the name. */
export type TableColumn = number | 'total' | 'difference' | 'name'

export function sortTable(
  rows: readonly SpendingTableRow[],
  column: TableColumn,
  direction: 'asc' | 'desc',
): SpendingTableRow[] {
  const sign = direction === 'desc' ? -1 : 1
  const value = (row: SpendingTableRow): number => {
    if (column === 'total') return spendOf(row.total)
    if (column === 'difference') return row.difference.amount
    if (column === 'name') return 0
    return spendOf(row.cells[column] ?? ZERO_MONEY)
  }
  return [...rows].sort((a, b) =>
    column === 'name'
      ? sign * a.label.localeCompare(b.label)
      : sign * (value(a) - value(b)) || a.label.localeCompare(b.label),
  )
}

/** A sparkline's points across a box, spend upward; a flat line when nothing moves. */
export function sparkPoints(cells: readonly Money[], width: number, height: number): string {
  if (cells.length === 0) return ''
  const values = cells.map((cell) => moneyToNumber(spendOf(cell)))
  const low = Math.min(...values)
  const high = Math.max(...values)
  const span = high - low
  const step = cells.length > 1 ? width / (cells.length - 1) : 0
  return values
    .map((value, index) => {
      const y = span === 0 ? height / 2 : height - ((value - low) / span) * height
      return `${round(index * step)},${round(y)}`
    })
    .join(' ')
}

function round(value: number): number {
  return Math.round(value * 10) / 10
}

/** The period chart's bars, plotted as net spend, with what the tooltip reads. */
export interface ChartBar {
  key: string
  from: string
  label: string
  /** Net spend: above zero for spending, below it for a net credit. */
  plot: number
  credit: boolean
  period: SpendingChartPeriod
}

export function chartBars(periods: readonly SpendingChartPeriod[], grain: SpendingGrain): ChartBar[] {
  return periods.map((period) => ({
    key: period.key,
    from: period.from,
    label: periodTick(period, grain),
    plot: moneyToNumber(spendOf(period.spent)),
    credit: period.spent > 0,
    period,
  }))
}

/* ---- Flow ------------------------------------------------------------------ */

/** The flow's bands in a sort's order. Its amounts are magnitudes, so largest is the biggest band. */
export function sortFlow(flow: SpendingFlow, sort: SpendingSort): SpendingFlow {
  const order = <T extends { label: string; amount: Money }>(nodes: readonly T[]): T[] =>
    sortRows(
      nodes.map((node) => ({ node, label: node.label, amount: spendOf(node.amount) })),
      sort,
    ).map((one) => one.node)
  return {
    ...flow,
    income: order(flow.income),
    credits: order(flow.credits),
    spending: order(flow.spending),
  }
}

export type FlowTone = 'income' | 'credit' | 'total' | 'spend' | 'left'

export interface FlowBox {
  key: string
  label: string
  amount: Money
  share: string | number | null
  tone: FlowTone
  /** The series index of a spend line, for its colour. */
  series: number
  column: 0 | 1 | 2 | 3
  x: number
  y: number
  height: number
}

export interface FlowLink {
  key: string
  path: string
  tone: FlowTone
  series: number
}

export interface FlowLayout {
  width: number
  height: number
  boxes: FlowBox[]
  links: FlowLink[]
}

export const FLOW_BAR = 8
const FLOW_SPAN = 320
const FLOW_SLOT = 34
const FLOW_GAP = 6

/**
 * The flow in four columns: income and credits, total income, total spent
 * (with what income left unspent beneath it), and the spend lines. Band
 * heights share one scale; a small line still takes a slot tall enough for its
 * label, which is what makes a long tail of lines readable.
 */
export function layoutFlow(flow: SpendingFlow, remaining: Money, width: number): FlowLayout {
  const value = (amount: Money) => Math.max(0, moneyToNumber(amount))
  const total = (nodes: readonly { amount: Money }[]) => nodes.reduce((sum, node) => sum + value(node.amount), 0)
  const income = value(flow.income_total)
  const credits = total(flow.credits)
  const gross = total(flow.spending)
  // What income still has once it has paid for what was spent: the summary's
  // remaining, when it is positive.
  const left = value(remaining)
  const fromIncome = Math.max(0, income - left)
  const scale = FLOW_SPAN / Math.max(1, total(flow.income) + credits, income, gross)

  const columnX = [0, width * 0.36, width * 0.62, width - FLOW_BAR]
  const boxes: FlowBox[] = []
  const links: FlowLink[] = []

  const stack = (
    nodes: readonly { key: string; label: string; amount: Money; share: string | number | null }[],
    column: 0 | 3,
    tone: (index: number) => FlowTone,
    start: number,
  ) => {
    let y = start
    return nodes.map((node, index) => {
      const height = Math.max(1, value(node.amount) * scale)
      const box: FlowBox = {
        ...node,
        tone: tone(index),
        series: index,
        column,
        x: columnX[column]!,
        y,
        height,
      }
      y += Math.max(height, FLOW_SLOT) + FLOW_GAP
      boxes.push(box)
      return box
    })
  }

  const sources = stack(
    [...flow.income, ...flow.credits],
    0,
    (index) => (index < flow.income.length ? 'income' : 'credit'),
    0,
  )
  const totalIncome: FlowBox = {
    key: 'total-income',
    label: 'Total income',
    amount: flow.income_total,
    share: flow.income_total > 0 ? 1 : null,
    tone: 'total',
    series: 0,
    column: 1,
    x: columnX[1]!,
    y: 0,
    height: Math.max(1, income * scale),
  }
  const totalSpent: FlowBox = {
    key: 'total-spent',
    label: 'Total spent',
    amount: flow.spent,
    share: flow.spent_share,
    tone: 'total',
    series: 0,
    column: 2,
    x: columnX[2]!,
    y: 0,
    height: Math.max(1, gross * scale),
  }
  boxes.push(totalIncome, totalSpent)
  const spends = stack(flow.spending, 3, () => 'spend', 0)

  let incomeOut = totalIncome.y
  let spentIn = totalSpent.y
  sources.forEach((source) => {
    const band = value(source.amount) * scale
    if (source.tone === 'income') {
      links.push(link(source, source.y, totalIncome, incomeOut, band))
      incomeOut += band
    }
  })
  const intoSpent = fromIncome * scale
  if (intoSpent > 0) links.push(link(totalIncome, totalIncome.y, totalSpent, spentIn, intoSpent))
  spentIn += intoSpent
  sources.forEach((source) => {
    if (source.tone !== 'credit') return
    const band = value(source.amount) * scale
    links.push(link(source, source.y, totalSpent, spentIn, band))
    spentIn += band
  })

  if (left > 0 && income > 0) {
    const rest: FlowBox = {
      key: 'left',
      label: 'Not spent',
      amount: remaining,
      share: null,
      tone: 'left',
      series: 0,
      column: 2,
      x: columnX[2]!,
      y: totalSpent.y + Math.max(totalSpent.height, FLOW_SLOT) + FLOW_GAP * 3,
      height: Math.max(1, left * scale),
    }
    boxes.push(rest)
    links.push(link(totalIncome, totalIncome.y + intoSpent, rest, rest.y, left * scale))
  }

  let spentOut = totalSpent.y
  spends.forEach((target) => {
    links.push({ ...link(totalSpent, spentOut, target, target.y, target.height), series: target.series })
    spentOut += target.height
  })

  const height = Math.max(...boxes.map((box) => box.y + Math.max(box.height, FLOW_SLOT)))
  return { width, height, boxes, links }
}

/** A band from one box's right edge to another's left, as wide as `band` at both ends. */
function link(from: FlowBox, fromY: number, to: FlowBox, toY: number, band: number): FlowLink {
  const x0 = from.x + FLOW_BAR
  const x1 = to.x
  const middle = (x0 + x1) / 2
  const path = [
    `M${round(x0)},${round(fromY)}`,
    `C${round(middle)},${round(fromY)} ${round(middle)},${round(toY)} ${round(x1)},${round(toY)}`,
    `L${round(x1)},${round(toY + band)}`,
    `C${round(middle)},${round(toY + band)} ${round(middle)},${round(fromY + band)} ${round(x0)},${round(fromY + band)}`,
    'Z',
  ].join(' ')
  return { key: `${from.key}>${to.key}`, path, tone: from.tone === 'total' ? to.tone : from.tone, series: 0 }
}
