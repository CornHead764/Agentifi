/**
 * The active-months toggles: shown for a rule that can be seasonal, carried
 * over a change of frequency, and dropped by a frequency that takes none.
 */

import { isValidElement, type ReactElement, type ReactNode } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'

import { recurrenceFor, type Recurrence } from '@/lib/recurrence'

import { SeriesSchedule, type SchedulePatch } from './SeriesSchedule'

const APRIL_12 = new Date('2026-04-12T00:00:00')
const LAWN = recurrenceFor('EVERY_MONTH', APRIL_12, { byMonth: [4, 5, 6, 7, 8, 9, 10] })

function schedule(recurrence: Recurrence, patches: SchedulePatch[] = []): ReactElement {
  return SeriesSchedule({
    recurrence,
    startOn: '2026-04-12',
    endOn: null,
    onChange: (patch) => patches.push(patch),
  }) as ReactElement
}

/** Every element in the tree whose props carry all of the named keys. */
function withProps(node: ReactNode, keys: readonly string[]): ReactElement<Record<string, unknown>>[] {
  if (Array.isArray(node)) return node.flatMap((child) => withProps(child, keys))
  if (!isValidElement<Record<string, unknown>>(node)) return []
  const own = keys.every((key) => key in node.props) ? [node] : []
  return [...own, ...withProps(node.props.children as ReactNode, keys)]
}

function pressed(markup: string): string[] {
  return [...markup.matchAll(/<button[^>]*>/g)]
    .map((match) => match[0])
    .filter((button) => button.includes('aria-pressed="true"'))
    .map((button) => /aria-label="(\w+)"/.exec(button)?.[1] ?? '')
}

describe('<SeriesSchedule> active months', () => {
  it('shows twelve month toggles on a monthly rule, the season pressed', () => {
    const markup = renderToStaticMarkup(schedule(LAWN))
    expect(markup).toContain('Active months')
    expect(pressed(markup)).toEqual([
      'April',
      'May',
      'June',
      'July',
      'August',
      'September',
      'October',
    ])
    expect(markup).toContain('Only in April to October.')
  })

  it('offers no months on a yearly or one-time rule', () => {
    expect(renderToStaticMarkup(schedule(recurrenceFor('EVERY_YEAR', APRIL_12)))).not.toContain(
      'Active months',
    )
    expect(renderToStaticMarkup(schedule(recurrenceFor('ONE_TIME', APRIL_12)))).not.toContain(
      'Active months',
    )
  })

  it('writes a toggled month and keeps the day of the month', () => {
    const patches: SchedulePatch[] = []
    const [toggles] = withProps(schedule(LAWN, patches), ['months', 'onChange'])
    ;(toggles.props.onChange as (months: number[]) => void)([4, 5, 6, 7, 8, 9])
    expect(patches[0].recurrence?.by_month).toEqual([4, 5, 6, 7, 8, 9])
    expect(patches[0].recurrence?.by_month_day).toEqual([12])
  })

  it('carries the season over a change of frequency, and a yearly rule drops it', () => {
    const patches: SchedulePatch[] = []
    const [frequency] = withProps(schedule(LAWN, patches), ['onValueChange', 'options'])
    const choose = frequency.props.onValueChange as (value: string) => void
    choose('EVERY_X_WEEKS')
    choose('EVERY_YEAR')
    expect(patches[0].recurrence?.by_month).toEqual([4, 5, 6, 7, 8, 9, 10])
    expect(patches[1].recurrence?.by_month).toEqual([])
  })
})
