import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  type PointerEvent,
} from 'react'

import { seriesColor } from '@/components/charts'
import { useMoneyText } from '@/components/moneyText'
import {
  expandCircles,
  fitFrame,
  packCircles,
  type PackedCircle,
} from '@/components/plan/packCircles'
import { formatPercent } from '@/lib/format'
import { sliceKey, type OtherSpendSlice } from '@/lib/spendingPlan'

import { bodyAtHome, stepBubbles, type BubbleBody } from './bubblePhysics'
import { canvasWidth, centredScroll, classifyPointer } from './panGesture'

const SIZE = 420
/** The smallest scale the chart is drawn at; a narrower viewport pans instead of shrinking it further. */
const MIN_SCALE = 0.85

/**
 * The packed-circle chart. Nothing leaves the screen: pressing a group opens
 * its children beside it and dims the others; pressing it again folds it, and
 * a dimmed sibling switches to it. Pressing a child, or an empty group,
 * selects it and the panel lists its rows; again unselects.
 *
 * Layout comes from `packCircles`/`expandCircles` and motion from
 * `bubblePhysics`. Children are born at the parent's centre. The loop stops
 * when the simulation reports rest. A viewport too narrow for the bubbles at
 * `MIN_SCALE` clips the chart and pans it sideways by drag, starting on the
 * largest bubble.
 */
export interface OtherSpendBubblesProps {
  /** The top-level groups, always all of them. */
  slices: readonly OtherSpendSlice[]
  /** Positive; the denominator for a top-level bubble's share. */
  total: number
  /** The key of the open group, or null. */
  openKey: string | null
  /** The key of the selected bubble, or null. */
  selected: string | null
  /** Open a group, switch to another, or close (null). */
  onOpen: (slice: OtherSpendSlice | null) => void
  onSelect: (slice: OtherSpendSlice | null) => void
}

interface BubbleItem {
  key: string
  slice: OtherSpendSlice
  /** The open group this bubble came out of; null for a top-level one. */
  parentKey: string | null
  circle: PackedCircle
  color: string
  /** A fraction of the parent's spend, or of the whole for a top-level bubble. */
  share: number
}

export function OtherSpendBubbles({
  slices,
  total,
  openKey,
  selected,
  onSelect,
  onOpen,
}: OtherSpendBubblesProps) {
  const moneyText = useMoneyText()

  const open = useMemo(
    () => (openKey === null ? null : (slices.find((one) => sliceKey(one) === openKey) ?? null)),
    [slices, openKey],
  )

  const items = useMemo<BubbleItem[]>(() => {
    const base = packCircles(
      slices.map((slice) => slice.spent),
      SIZE,
    )
    const openIndex = open === null ? -1 : slices.indexOf(open)
    const expanded =
      open !== null && open.children.length > 0 && openIndex >= 0
        ? expandCircles(
            base,
            openIndex,
            open.children.map((child) => child.spent),
            open.spent,
            SIZE,
          )
        : { parents: base, children: [] }

    const tops = expanded.parents.map((circle) => {
      const slice = slices[circle.index]
      return {
        key: sliceKey(slice),
        slice,
        parentKey: null,
        circle,
        color: seriesColor(circle.index),
        share: total === 0 ? 0 : slice.spent / total,
      }
    })
    const kids =
      open === null
        ? []
        : expanded.children.map((circle) => {
            const slice = open.children[circle.index]
            return {
              key: sliceKey(slice),
              slice,
              parentKey: openKey,
              circle,
              color: childShade(seriesColor(openIndex), circle.index),
              share: open.spent === 0 ? 0 : slice.spent / open.spent,
            }
          })
    return [...tops, ...kids]
  }, [slices, open, openKey, total])

  const frame = useRef<number | null>(null)
  const lastAt = useRef<number | null>(null)

  // The ref is the loop's authoritative copy, stepped synchronously so "still
  // moving" is a plain return value: reading it from a setState updater, which
  // React may defer, would come back stale and freeze the loop. The state
  // copy exists only to be rendered.
  const sim = useRef<{ items: BubbleItem[] | null; bodies: BubbleBody[] }>({
    items: null,
    bodies: [],
  })
  const [bodies, setBodies] = useState<BubbleBody[]>([])

  const run = useCallback(() => {
    if (frame.current !== null) return
    const tick = (now: number) => {
      const elapsed = lastAt.current === null ? 1 / 60 : (now - lastAt.current) / 1000
      lastAt.current = now
      const result = stepBubbles(sim.current.bodies, elapsed)
      sim.current.bodies = result.bodies
      setBodies(result.bodies)
      if (result.moving) {
        frame.current = requestAnimationFrame(tick)
        return
      }
      frame.current = null
      lastAt.current = null
    }
    lastAt.current = null
    frame.current = requestAnimationFrame(tick)
  }, [])

  // A layout effect, so the incoming layout is never painted with the bodies
  // still on the outgoing one. Bodies carry their positions across; guarded on
  // identity so StrictMode's second pass reconciles nothing.
  useLayoutEffect(() => {
    if (sim.current.items !== items) {
      sim.current.bodies = reconcile(sim.current.items ?? [], sim.current.bodies, items)
      sim.current.items = items
      setBodies(sim.current.bodies)
    }
    // Idempotent while a loop runs; the first step reports rest when nothing
    // moved.
    run()
  }, [items, run])

  // The refs are cleared, not just the frame cancelled: StrictMode runs this
  // cleanup between its mount passes, and a stale id leaves run()'s "already
  // running" guard true forever.
  useEffect(
    () => () => {
      if (frame.current !== null) cancelAnimationFrame(frame.current)
      frame.current = null
      lastAt.current = null
    },
    [],
  )

  const press = (item: BubbleItem) => {
    if (item.parentKey === null && item.slice.children.length > 0) {
      // A group: open it, switch to it, or — if it is the open one — close it.
      onOpen(item.key === openKey ? null : item.slice)
      return
    }
    onSelect(item.key === selected ? null : item.slice)
  }

  const mode = selected !== null ? 'selection' : open !== null ? 'drill' : 'plain'
  const openItemIndex = openKey === null ? -1 : items.findIndex((item) => item.key === openKey)
  // Fallback to the layout spot, as every bubble below does: the state copy
  // is empty until the layout effect runs, and on the server it always is.
  const openItem = openItemIndex === -1 ? null : items[openItemIndex]
  const openBody =
    openItem === null
      ? null
      : (bodies[openItemIndex] ??
        bodyAtHome(openItem.circle.x, openItem.circle.y, openItem.circle.r))

  const box = fitFrame(
    items.map(
      (item, index) => bodies[index] ?? bodyAtHome(item.circle.x, item.circle.y, item.circle.r),
    ),
    SIZE,
  )

  const pan = useRef<HTMLDivElement>(null)
  const drag = useRef<{ x: number; y: number; scroll: number; panning: boolean } | null>(null)
  const swallowClick = useRef(false)
  const centred = useRef(false)
  const [viewport, setViewport] = useState(0)

  useLayoutEffect(() => {
    const el = pan.current
    if (el === null) return
    const measure = () => setViewport(el.clientWidth)
    measure()
    if (typeof ResizeObserver === 'undefined') return
    const observer = new ResizeObserver(measure)
    observer.observe(el)
    return () => observer.disconnect()
  }, [])

  const width = canvasWidth(viewport, box.width, MIN_SCALE)
  const largest = items.reduce<BubbleItem | null>(
    (best, item) => (best === null || item.circle.r > best.circle.r ? item : best),
    null,
  )

  useLayoutEffect(() => {
    const el = pan.current
    if (centred.current || el === null || viewport === 0 || largest === null) return
    centred.current = true
    el.scrollLeft = centredScroll(((largest.circle.x - box.x) * width) / box.width, viewport, width)
  }, [viewport, largest, box.x, box.width, width])

  const onPointerDown = (event: PointerEvent<HTMLDivElement>) => {
    if (event.button !== 0 || pan.current === null) return
    drag.current = {
      x: event.clientX,
      y: event.clientY,
      scroll: pan.current.scrollLeft,
      panning: false,
    }
  }
  const onPointerMove = (event: PointerEvent<HTMLDivElement>) => {
    const start = drag.current
    const el = pan.current
    if (start === null || el === null) return
    const dx = event.clientX - start.x
    if (!start.panning) {
      if (classifyPointer(dx, event.clientY - start.y) === 'tap') return
      start.panning = true
      el.setPointerCapture(event.pointerId)
    }
    el.scrollLeft = start.scroll - dx
  }
  const onPointerEnd = () => {
    if (drag.current?.panning) {
      // The click that follows a drag's release is not a press on a bubble.
      swallowClick.current = true
      setTimeout(() => {
        swallowClick.current = false
      }, 0)
    }
    drag.current = null
  }

  return (
    <div
      ref={pan}
      className="bubbles-pan"
      onPointerDown={onPointerDown}
      onPointerMove={onPointerMove}
      onPointerUp={onPointerEnd}
      onPointerCancel={onPointerEnd}
      onClickCapture={(event) => {
        if (!swallowClick.current) return
        event.stopPropagation()
        event.preventDefault()
      }}
    >
    <svg
      viewBox={`${box.x} ${box.y} ${box.width} ${box.height}`}
      className="bubbles"
      style={viewport === 0 ? undefined : { width }}
      role="group"
      aria-label="Other Spend by category"
    >
      {/* Spokes under everything: the visible statement of where the children came from. */}
      {openItem === null || openBody === null
        ? null
        : items.map((item, index) => {
            if (item.parentKey === null) return null
            const body = bodies[index] ?? bodyAtHome(item.circle.x, item.circle.y, item.circle.r)
            return (
              <line
                key={`spoke-${item.key}`}
                className="bubble-spoke"
                x1={openBody.x}
                y1={openBody.y}
                x2={body.x}
                y2={body.y}
                stroke={seriesColor(openItem.circle.index)}
                aria-hidden="true"
              />
            )
          })}

      {items.map((item, index) => {
        const body = bodies[index] ?? bodyAtHome(item.circle.x, item.circle.y, item.circle.r)
        const isSelected = item.key === selected
        const isOpenGroup = item.key === openKey
        const inFamily = isOpenGroup || item.parentKey !== null
        const classes = ['bubble']
        if (mode === 'selection') classes.push(isSelected ? 'bubble--selected' : 'bubble--hollow')
        else if (mode === 'drill' && !inFamily) classes.push('bubble--dim')

        const group = item.parentKey === null && item.slice.children.length > 0
        if (isOpenGroup && mode === 'drill') classes.push('bubble--open')
        const label = `${item.slice.category_name}, ${moneyText(item.slice.spent, {
          signs: 'absolute',
        })}, ${formatPercent(item.share, { digits: 0 })}${group ? (isOpenGroup ? ', open — press to close' : ', opens') : ''}`
        const amountText = moneyText(item.slice.spent, { signs: 'absolute', showCents: false })
        const text = labelTier(item.slice.category_name, amountText, item.circle.r)

        return (
          <g
            key={item.key}
            className={classes.join(' ')}
            transform={`translate(${body.x} ${body.y})`}
            role="button"
            tabIndex={0}
            aria-pressed={group ? undefined : isSelected}
            aria-expanded={group ? isOpenGroup : undefined}
            aria-label={label}
            onClick={() => press(item)}
            onKeyDown={(event) => {
              if (event.key !== 'Enter' && event.key !== ' ') return
              event.preventDefault()
              press(item)
            }}
          >
            <circle
              r={item.circle.r}
              fill={item.color}
              stroke={
                isOpenGroup && mode === 'drill'
                  ? `color-mix(in oklab, ${item.color} 55%, white)`
                  : item.color
              }
              className="bubble__disc"
            />
            {text.tier === 'none' ? null : text.tier === 'amount' ? (
              <text className="other-spend__label">
                <tspan
                  x={0}
                  dy="0.35em"
                  className="other-spend__amount"
                  style={{ fontSize: AMOUNT_SIZE * text.scale }}
                >
                  {amountText}
                </tspan>
              </text>
            ) : (
              <text className="other-spend__label" style={{ fontSize: NAME_SIZE * text.scale }}>
                <tspan x={0} dy="-0.4em">
                  {item.slice.category_name}
                </tspan>
                <tspan
                  x={0}
                  dy="1.15em"
                  className="other-spend__amount"
                  style={{ fontSize: AMOUNT_SIZE * text.scale }}
                >
                  {amountText}
                </tspan>
                {item.circle.r < 30 ? null : (
                  <tspan
                    x={0}
                    dy="1.2em"
                    className="other-spend__share"
                    style={{ fontSize: SHARE_SIZE * text.scale }}
                  >
                    {formatPercent(item.share, { digits: 0 })}
                  </tspan>
                )}
              </text>
            )}
          </g>
        )
      })}
    </svg>
    </div>
  )
}

/**
 * Carry the bodies across a change of arrangement: an existing bubble keeps
 * its position and velocity; a new one starts at rest at its spot, except a
 * child of the open group, born at its parent's centre. Reduced motion starts
 * everything where it ends.
 */
function reconcile(
  prevItems: readonly BubbleItem[],
  prevBodies: readonly BubbleBody[],
  nextItems: readonly BubbleItem[],
): BubbleBody[] {
  const before = new Map<string, BubbleBody>()
  prevItems.forEach((item, index) => {
    const body = prevBodies[index]
    if (body) before.set(item.key, body)
  })

  const still = reducedMotion()
  return nextItems.map((item) => {
    const { x, y, r } = item.circle
    const prev = before.get(item.key)
    if (prev) return { ...prev, homeX: x, homeY: y, r }
    if (still || item.parentKey === null) return bodyAtHome(x, y, r)
    const parent = before.get(item.parentKey)
    if (!parent) return bodyAtHome(x, y, r)
    return { homeX: x, homeY: y, r, x: parent.x, y: parent.y, vx: 0, vy: 0 }
  })
}

/** The label's base sizes, in viewBox units. */
const NAME_SIZE = 10.5
const AMOUNT_SIZE = 11.5
const SHARE_SIZE = 8.5
/** Approximate advance width of one character, as a fraction of font size. */
const CHAR_WIDTH = 0.62
/** How much of the diameter the text band may use — a chord, not the equator. */
const TEXT_ROOM = 1.7

/**
 * How much to shrink a bubble's whole label: the smaller of a size that tracks
 * the radius and one that fits the longest word, since a name must never
 * spill past the rim. Width is estimated from character count, erring wide.
 */
function labelScale(name: string, amount: string, r: number): number {
  const widest = Math.max(name.length * NAME_SIZE, amount.length * AMOUNT_SIZE) * CHAR_WIDTH
  const fit = (TEXT_ROOM * r) / widest
  const size = Math.max(Math.min(r / 64, 1), 0.78)
  return Math.min(1, size, fit)
}

/**
 * What a bubble can carry: its full label, its amount, or nothing. Below half
 * size the name is dropped rather than wrapped; only a bubble too small for
 * its amount goes bare.
 */
function labelTier(
  name: string,
  amount: string,
  r: number,
): { tier: 'full' | 'amount' | 'none'; scale: number } {
  const full = labelScale(name, amount, r)
  if (r >= 18 && full >= 0.5) return { tier: 'full', scale: full }
  const alone = labelScale(amount, amount, r)
  if (r >= 14 && alone >= 0.6) return { tier: 'amount', scale: alone }
  return { tier: 'none', scale: 0 }
}

/** A child's colour is a shade of its parent's; each child leans lighter or darker in turn. */
const CHILD_TONES = [18, -22, 40, -38, 8, -10, 28]

function childShade(base: string, index: number): string {
  const tone = CHILD_TONES[index % CHILD_TONES.length]
  return tone >= 0
    ? `color-mix(in oklab, ${base} ${100 - tone}%, white)`
    : `color-mix(in oklab, ${base} ${100 + tone}%, black)`
}

/**
 * Whether to skip the glide. Read at transition time so a changed preference
 * applies without a reload; guarded because jsdom has no `matchMedia`.
 */
function reducedMotion(): boolean {
  return (
    typeof window !== 'undefined' &&
    typeof window.matchMedia === 'function' &&
    window.matchMedia('(prefers-reduced-motion: reduce)').matches
  )
}
