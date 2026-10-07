import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'

import { Meter } from './Meter'
import { endsOnTint, filledLength, laySegments } from './meter-segments'

const widths = (markup: string) =>
  [...markup.matchAll(/class="meter__segment[^"]*" style="width:([\d.]+)%/g)].map((match) =>
    Number(match[1]),
  )

describe('laySegments', () => {
  it('stops the segments at the end of the track, and drops what is negative', () => {
    expect(laySegments([{ value: 70 }, { value: 50 }, { value: 10 }]).map((one) => one.value)).toEqual([
      70, 30, 0,
    ])
    expect(laySegments([{ value: -5 }, { value: Number.NaN }, { value: 40 }]).map((one) => one.value)).toEqual([
      0, 0, 40,
    ])
  })

  it('measures the filled length past a gap, and reads the tint at a point', () => {
    const laid = laySegments([
      { value: 30, tone: 'series' },
      { value: 20, tone: 'none' },
      { value: 50, tone: 'carried' },
    ])
    expect(filledLength(laid)).toBe(100)
    expect(filledLength(laySegments([{ value: 30 }, { value: 20, tone: 'none' }]))).toBe(30)
    expect(endsOnTint(laid, 30)).toBe(false)
    expect(endsOnTint(laySegments([{ value: 40 }, { value: 20, faded: true }]), 60)).toBe(true)
  })
})

describe('<Meter>', () => {
  it('draws each segment at its share, and announces the filled length', () => {
    const markup = renderToStaticMarkup(
      <Meter label="Progress toward Japan" segments={[{ value: 45 }, { value: 15, faded: true }]} />,
    )
    expect(markup).toContain('role="meter"')
    expect(markup).toContain('aria-label="Progress toward Japan"')
    expect(markup).toContain('aria-valuenow="60"')
    expect(markup).toContain('aria-valuemax="100"')
    expect(widths(markup)).toEqual([45, 15])
    expect(markup).toContain('meter__segment--faded')
  })

  it('is decoration without a label', () => {
    const markup = renderToStaticMarkup(<Meter segments={[{ value: 20 }]} />)
    expect(markup).toContain('aria-hidden="true"')
    expect(markup).not.toContain('role=')
  })

  it('prints a reading inside a fill wide enough to hold it, and past a short one', () => {
    const inside = renderToStaticMarkup(
      <Meter size="lg" segments={[{ value: 100, tone: 'overspent' }]} reading={{ text: '250%', at: 100 }} />,
    )
    expect(inside).toContain('class="meter__reading meter__reading--on-tint" style="right:calc(100% - 100%)"')
    expect(inside).toContain('>250%<')

    const outside = renderToStaticMarkup(
      <Meter size="lg" segments={[{ value: 8 }]} reading={{ text: '8%', at: 8 }} />,
    )
    expect(outside).toContain('class="meter__reading meter__reading--outside"')
  })
})
