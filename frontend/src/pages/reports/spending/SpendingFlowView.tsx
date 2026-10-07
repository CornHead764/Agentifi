import { useEffect, useState } from 'react'

import { ACCENT_COLOR, INCOME_COLOR, seriesColor } from '@/components/charts'
import { useMoneyText } from '@/components/moneyText'
import { EmptyState } from '@/components/ui'
import type { SpendingFlow } from '@/lib/clients/reports'
import { formatPercent, parseRate } from '@/lib/format'
import type { Money } from '@/lib/money'
import { FLOW_BAR, layoutFlow, type FlowBox, type FlowLink } from '@/lib/reports/spending'

/** Narrower than this the labels would collide, so the flow scrolls instead. */
const NARROWEST = 640
const LABEL_GAP = 6
/** A label longer than this would run across the bands beside it; it is cut, and whole on hover. */
const LABEL_CHARACTERS = 28

function clipLabel(label: string): string {
  return label.length > LABEL_CHARACTERS ? `${label.slice(0, LABEL_CHARACTERS - 1).trimEnd()}…` : label
}

/**
 * Income into total income, and total income with any credits into total
 * spent, which fans out to where it went. Every share is of income.
 */
export function SpendingFlowView({
  flow,
  remaining,
  showCents,
  colorOf,
}: {
  flow: SpendingFlow
  /** The summary's remaining: what income has left once spending is paid. */
  remaining: Money
  showCents: boolean
  colorOf: (key: string) => number
}) {
  const [box, width] = useWidth()
  const money = useMoneyText()

  if (flow.income.length === 0 && flow.spending.length === 0 && flow.credits.length === 0) {
    return (
      <div className="spend-flow" ref={box}>
        <EmptyState compact title="No income or spending in this period." />
      </div>
    )
  }

  const layout = layoutFlow(flow, remaining, Math.max(NARROWEST, width))
  const color = (tone: FlowBox['tone'] | FlowLink['tone'], key: string) =>
    tone === 'spend' ? seriesColor(colorOf(key)) : tone === 'total' ? ACCENT_COLOR : tone === 'left' ? 'var(--text-faint)' : INCOME_COLOR

  const caption = (one: FlowBox) => {
    const share = parseRate(one.share)
    const amount = money(one.amount, { signs: 'absolute', showCents })
    return share === null ? amount : `${amount} (${formatPercent(share, { digits: 1 })})`
  }

  return (
    <div className="spend-flow" ref={box}>
      <svg
        width={layout.width}
        height={layout.height}
        viewBox={`0 0 ${layout.width} ${layout.height}`}
        role="img"
        aria-label="Income flowing into spending"
      >
        {layout.links.map((link) => (
          <path
            key={link.key}
            d={link.path}
            className="spend-flow__band"
            fill={color(link.tone, link.key.split('>')[1] ?? '')}
          />
        ))}
        {layout.boxes.map((one) => {
          const right = one.column === 3
          const x = right ? one.x - LABEL_GAP : one.x + FLOW_BAR + LABEL_GAP
          return (
            <g key={one.key}>
              <title>{`${one.label}: ${caption(one)}`}</title>
              <rect
                x={one.x}
                y={one.y}
                width={FLOW_BAR}
                height={one.height}
                rx={2}
                fill={color(one.tone, one.key)}
              />
              <text
                x={x}
                y={one.y + 12}
                textAnchor={right ? 'end' : 'start'}
                className="spend-flow__label"
              >
                <tspan>{clipLabel(one.tone === 'credit' ? `${one.label} (credit)` : one.label)}</tspan>
                <tspan x={x} dy={14} className="spend-flow__figure">
                  {caption(one)}
                </tspan>
              </text>
            </g>
          )
        })}
      </svg>
    </div>
  )
}

/** A ref to measure and the element's width, followed as it changes; the widest layout until measured. */
function useWidth(): [(node: HTMLDivElement | null) => void, number] {
  const [node, setNode] = useState<HTMLDivElement | null>(null)
  const [width, setWidth] = useState(960)
  useEffect(() => {
    if (!node) return
    const observer = new ResizeObserver(([entry]) => {
      if (entry) setWidth(Math.floor(entry.contentRect.width))
    })
    observer.observe(node)
    return () => observer.disconnect()
  }, [node])
  return [setNode, width]
}
