/**
 * The debt-to-asset rating. `debt_to_asset = |debt| / assets`, from
 * `NetWorth.DebtToAsset` in the Go core, which reports `ok=false` with no
 * assets. The bands are display bands over it, read off Simplifi's gauge
 * (40% reads "Good").
 */

export type DebtRating = 'excellent' | 'good' | 'fair' | 'poor'

export interface DebtBand {
  rating: DebtRating
  label: string
  coaching: string
}

const BANDS: readonly (DebtBand & { ceiling: number })[] = [
  {
    ceiling: 0.3,
    rating: 'excellent',
    label: 'Excellent',
    coaching: 'Your debt is low compared to your assets. Keep it there and put the difference to work.',
  },
  {
    ceiling: 0.5,
    rating: 'good',
    label: 'Good',
    coaching:
      'Your debt is moderate compared to your assets. Consider strategies to increase your assets or decrease your debt.',
  },
  {
    ceiling: 0.7,
    rating: 'fair',
    label: 'Fair',
    coaching:
      'Your debt is a large share of what you own. Paying down the highest-rate balance first moves this fastest.',
  },
  {
    ceiling: Number.POSITIVE_INFINITY,
    rating: 'poor',
    label: 'Needs attention',
    coaching: 'Your debt outweighs a healthy share of your assets. Focus on reducing balances before adding any.',
  },
]

/** Null in, null out: a ratio with no assets to divide by has no rating. */
export function debtBand(ratio: number | null): DebtBand | null {
  if (ratio === null) return null
  return BANDS.find((band) => ratio < band.ceiling) ?? BANDS[BANDS.length - 1]
}

/** Where the marker sits on the gradient bar, clamped so it stays on the track. */
export function debtMarkerPosition(ratio: number | null): number {
  if (ratio === null) return 0
  return Math.min(100, Math.max(0, ratio * 100))
}
