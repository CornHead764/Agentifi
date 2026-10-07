/** The range chips: `1M 3M 6M 1Y 5Y QTD YTD All`, as a radio group. */

import { ChipGroup } from '@/components/ui'
import { RANGE_PRESETS, rangeLabel, type RangePreset } from '@/lib/dateRanges'

export function RangeChips({
  value,
  onChange,
  presets = RANGE_PRESETS,
  label = 'Date range',
}: {
  value: RangePreset
  onChange: (preset: RangePreset) => void
  presets?: readonly RangePreset[]
  label?: string
}) {
  return (
    <ChipGroup
      label={label}
      value={value}
      options={presets.map((preset) => ({ value: preset, label: rangeLabel(preset) }))}
      onChange={onChange}
    />
  )
}
