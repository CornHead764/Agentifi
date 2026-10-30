export type MeterTone =
  | 'accent'
  | 'series'
  | 'income'
  | 'expense'
  | 'warning'
  /** Striped expense: past the end of the budget. */
  | 'overspent'
  /** Money carried in from an earlier month. */
  | 'carried'
  /** The good-to-bad gradient a marker is read against. */
  | 'scale'
  /** Track showing through, to start the next segment further along. */
  | 'none'

export interface MeterSegment {
  /** Percent of the track, laid after the segment before it. */
  value: number
  tone?: MeterTone
  /** Drawn at a third of its strength: a share that was there and has gone. */
  faded?: boolean
  /** A series colour, in place of the tone's. */
  color?: string
}

/**
 * The segments as drawn: each clamped so none is negative and together they
 * stop at the end of the track, which the figures beside a bar need not.
 */
export function laySegments(segments: readonly MeterSegment[]): MeterSegment[] {
  let used = 0
  return segments.map((segment) => {
    const value = Number.isFinite(segment.value)
      ? Math.min(Math.max(segment.value, 0), 100 - used)
      : 0
    used += value
    return { ...segment, value }
  })
}

/** How far the segments that draw something reach, as a percent of the track. */
export function filledLength(segments: readonly MeterSegment[]): number {
  let end = 0
  let at = 0
  for (const segment of segments) {
    at += segment.value
    if (segment.tone !== 'none' && segment.value > 0) end = at
  }
  return end
}

/**
 * Whether the segment that ends at `at` is a tint, on which the light reading
 * printed over a solid fill would disappear.
 */
export function endsOnTint(segments: readonly MeterSegment[], at: number): boolean {
  let start = 0
  let last: MeterSegment | undefined
  for (const segment of segments) {
    if (segment.value > 0 && start < at) last = segment
    start += segment.value
  }
  if (last === undefined) return false
  return last.faded === true || last.tone === 'overspent' || last.tone === 'carried'
}
