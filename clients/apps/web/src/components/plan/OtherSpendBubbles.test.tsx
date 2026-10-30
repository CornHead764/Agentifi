import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'

import { moneyFromCents } from '@/lib/money'
import type { OtherSpendSlice } from '@/lib/spendingPlan'

import { OtherSpendBubbles } from './OtherSpendBubbles'
import { fitFrame } from './packCircles'

function slice(
  name: string,
  spentCents: number,
  children: OtherSpendSlice[] = [],
): OtherSpendSlice {
  return {
    category_id: `cat-${name}`,
    category_name: name,
    spent: moneyFromCents(spentCents),
    txn_ids: [],
    children,
  }
}

const electronics = slice('Electronics', 41_400)
const clothing = slice('Clothing', 25_800)
const shopping = slice('Shopping', 67_200, [electronics, clothing])
const travel = slice('Travel', 221_000)
const top = [shopping, travel]
const total = 67_200 + 221_000

const noop = () => {}

describe('<OtherSpendBubbles>', () => {
  it('shows every group solid, with shares of the whole, while nothing is open', () => {
    const html = renderToStaticMarkup(
      <OtherSpendBubbles
        slices={top}
        total={total}
        openKey={null}
        selected={null}
        onOpen={noop}
        onSelect={noop}
      />,
    )

    expect(html).toContain('Shopping')
    expect(html).toContain('Travel')
    expect(html).not.toContain('Electronics')
    expect(html).not.toContain('bubble--dim')
    expect(html).not.toContain('bubble-spoke')
    // 221,000 of 288,200.
    expect(html).toContain('77%')
  })

  it('keeps everything on screen when a group opens: children join it, siblings dim', () => {
    const html = renderToStaticMarkup(
      <OtherSpendBubbles
        slices={top}
        total={total}
        openKey="cat-Shopping"
        selected={null}
        onOpen={noop}
        onSelect={noop}
      />,
    )

    // The children are on the chart — and so, still, is the sibling.
    expect(html).toContain('Electronics')
    expect(html).toContain('Clothing')
    expect(html).toContain('Travel')
    // Exactly one dimmed bubble: the sibling, not the open family.
    expect(html.match(/bubble--dim/g)).toHaveLength(1)
    // One spoke per child, saying where they came from — and the open group
    // wears its ring.
    expect(html.match(/bubble-spoke/g)).toHaveLength(2)
    expect(html).toContain('bubble--open')
    // A child's share is of its parent: 41,400 of 67,200.
    expect(html).toContain('62%')
    // The open group announces how to close it.
    expect(html).toContain('press to close')
  })

  it('drops everything but the selection to an outline once a child is selected', () => {
    const html = renderToStaticMarkup(
      <OtherSpendBubbles
        slices={top}
        total={total}
        openKey="cat-Shopping"
        selected="cat-Electronics"
        onOpen={noop}
        onSelect={noop}
      />,
    )

    expect(html.match(/bubble--selected/g)).toHaveLength(1)
    // Everything else: the parent, the other child, and the sibling.
    expect(html.match(/bubble--hollow/g)).toHaveLength(3)
    expect(html).not.toContain('bubble--dim')
    // The spokes stay: the selection is still inside the open group.
    expect(html.match(/bubble-spoke/g)).toHaveLength(2)
  })

  it('colours a child as a shade of its parent, never a new hue', () => {
    const html = renderToStaticMarkup(
      <OtherSpendBubbles
        slices={top}
        total={total}
        openKey="cat-Shopping"
        selected={null}
        onOpen={noop}
        onSelect={noop}
      />,
    )

    // Shopping packs first (largest of its level ordering aside, its index in
    // the slice list is 0), so its children mix from --series-1.
    expect(html).toContain('color-mix(in oklab, var(--series-1)')
  })
})

describe('label fitting', () => {
  it('scales a long name down to its bubble instead of spilling or wrapping', () => {
    // "Veterinary" on a modest disc, the case that would overflow: the label
    // must shrink to fit, and it must stay one line — no wrap markup, ever.
    const veterinary = slice('Veterinary', 8_000)
    const pets = slice('Pets', 47_100, [veterinary])
    const html = renderToStaticMarkup(
      <OtherSpendBubbles
        slices={[pets, slice('Shopping', 300_000)]}
        total={347_100}
        openKey="cat-Pets"
        selected={null}
        onOpen={noop}
        onSelect={noop}
      />,
    )

    const match = /<text[^>]*font-size:([\d.]+)px[^>]*>(?:(?!<\/text>)[\s\S])*?Veterinary/.exec(
      html,
    )
    expect(match).not.toBeNull()
    // Base name size is 10.5; a ten-character name on this disc cannot hold it.
    expect(Number(match![1])).toBeLessThan(8)
    expect(Number(match![1])).toBeGreaterThanOrEqual(10.5 * 0.5)
  })

  it('retreats to the amount alone when the name cannot fit legibly', () => {
    const sliver = slice('Miscellaneous Subscriptions', 900)
    const pets = slice('Pets', 47_100, [sliver])
    const html = renderToStaticMarkup(
      <OtherSpendBubbles
        slices={[pets, slice('Shopping', 300_000)]}
        total={347_100}
        openKey="cat-Pets"
        selected={null}
        onOpen={noop}
        onSelect={noop}
      />,
    )
    // The name is dropped rather than wrapped or spilled — but the bubble is
    // not left blank: its amount still says what it costs.
    expect(html).not.toContain('Miscellaneous Subscriptions</tspan>')
    expect(html).toContain('>$9</tspan>')
  })
})

describe('the frame', () => {
  it('is the packed box while every bubble sits inside it', () => {
    expect(fitFrame([{ x: 210, y: 210, r: 100 }], 420)).toEqual({
      x: 0,
      y: 0,
      width: 420,
      height: 420,
    })
  })

  it('grows to take in a bubble that left the box, so nothing is off screen', () => {
    // A child that settled past the right edge and above the top.
    const frame = fitFrame(
      [
        { x: 210, y: 210, r: 100 },
        { x: 440, y: -20, r: 30 },
      ],
      420,
    )
    expect(frame.x).toBe(0)
    expect(frame.y).toBe(-56)
    expect(frame.width).toBe(476)
    expect(frame.height).toBe(476)
  })

  it('draws the chart in a frame at least the packed box, grown for what left it', () => {
    // Two groups this unequal leave the packer no in-bounds spot for the
    // smaller one.
    const html = renderToStaticMarkup(
      <OtherSpendBubbles
        slices={top}
        total={total}
        openKey={null}
        selected={null}
        onOpen={noop}
        onSelect={noop}
      />,
    )
    const match = /viewBox="(-?[\d.]+) (-?[\d.]+) ([\d.]+) ([\d.]+)"/.exec(html)
    expect(match).not.toBeNull()
    const [, x, y, width, height] = match!.map(Number)
    expect(x).toBeLessThanOrEqual(0)
    expect(y).toBeLessThanOrEqual(0)
    expect(width).toBeGreaterThanOrEqual(420)
    expect(height).toBeGreaterThanOrEqual(420)
  })
})
