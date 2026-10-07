/**
 * Which range a page opens on: the space's preference, else the page's own
 * fallback. A starting value, not a binding: a chip click changes only this page.
 */

import { useCallback, useState } from 'react'

import { useCurrentSpace } from '@/lib/clients/spaces'
import { RANGE_PRESETS, asRangePreset, type RangePreset } from '@/lib/dateRanges'

/** Derived rather than adopted by an effect: the preference arrives after mount, and the user's pick wins. */
export function usePreferredRange(
  fallback: RangePreset,
  allowed: readonly RangePreset[] = RANGE_PRESETS,
): [RangePreset, (preset: RangePreset) => void] {
  const { data: space } = useCurrentSpace()
  const [chosen, setChosen] = useState<RangePreset | null>(null)

  const preferred = asRangePreset(space?.default_date_range)
  // A preference this page does not offer would leave no chip highlighted.
  const opening = preferred && allowed.includes(preferred) ? preferred : fallback

  return [chosen ?? opening, useCallback((preset: RangePreset) => setChosen(preset), [])]
}
