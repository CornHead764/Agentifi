import { axisLabels } from '@/lib/format'
import type { Money } from '@/lib/money'

export interface AxisPoint {
  key: string
  label: string
}

/** One axis point per ISO date, keyed by the date and labelled so every label is unique (see axisLabels). */
export function labeledAxis(isos: readonly string[]): AxisPoint[] {
  const labels = axisLabels(isos)
  return isos.map((key, index) => ({ key, label: labels[index] }))
}

/** Dated rows as chart points: keyed by `on`, labelled by `labeledAxis`. */
export function labeledPoints<P extends { on: string }>(
  points: readonly P[],
  value: (point: P) => Money,
): (AxisPoint & { value: Money })[] {
  const axis = labeledAxis(points.map((point) => point.on))
  return points.map((point, index) => ({ ...axis[index], value: value(point) }))
}
