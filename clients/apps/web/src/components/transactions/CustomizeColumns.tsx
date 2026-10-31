import { ArrowDown, ArrowUp, Eye, EyeOff, Lock, Settings2 } from 'lucide-react'

import {
  ChipGroup,
  IconButton,
  Popover,
  PopoverContent,
  PopoverTrigger,
} from '@/components/ui'
import {
  customizableColumns,
  defaultPrefs,
  hiddenReason,
  isDefaultOrder,
  moveColumn,
  type ColumnId,
  type ColumnPrefs,
} from '@/lib/transactions/columns'
import type { RowHeight } from '@/lib/transactions/rows'

const HEIGHTS: readonly { id: RowHeight; label: string }[] = [
  { id: 'sm', label: 'SM' },
  { id: 'md', label: 'MD' },
  { id: 'lg', label: 'LG' },
]

/**
 * Customize Columns. Date, Payee and Category show a lock rather than being
 * absent. A column switched on but dropped by `fitColumns` says so here.
 */
export function CustomizeColumns({
  prefs,
  shown,
  only,
  onChange,
}: {
  prefs: ColumnPrefs
  /** What the register is drawing; everything else on is hidden by the width. */
  shown?: readonly ColumnId[]
  /** The columns this tab can draw at all. Omitted on the register, which
   *  draws every one of them. */
  only?: readonly ColumnId[]
  onChange: (prefs: ColumnPrefs) => void
}) {
  const listed = customizableColumns(only, prefs.order)
  const listedIds = listed.map((column) => column.id)
  const move = (id: ColumnId, direction: 'up' | 'down') =>
    onChange({ ...prefs, order: moveColumn(prefs.order, id, direction, listedIds) })
  return (
    <Popover>
      <PopoverTrigger asChild>
        <IconButton label="Customize columns" variant="ghost" size="sm">
          <Settings2 size={14} />
        </IconButton>
      </PopoverTrigger>
      <PopoverContent className="customize">
        <p className="menu__label eyebrow">Customize columns</p>
        {listed.map((column, index) => {
          const visible = prefs.visible[column.id]
          // Only once the grid has reported its first measurement.
          const absent = visible && shown !== undefined ? hiddenReason(column.id, shown) : null
          return (
            <div key={column.id} className="customize__row" data-locked={column.locked === true}>
              <span className="customize__name">
                {column.label}
                {absent === null ? null : <span className="hint hint--faint">{absent}</span>}
              </span>
              <span className="customize__controls">
                <IconButton
                  size="sm"
                  variant="ghost"
                  disabled={index === 0}
                  label={`Move ${column.label} up`}
                  onClick={() => move(column.id, 'up')}
                >
                  <ArrowUp size={13} />
                </IconButton>
                <IconButton
                  size="sm"
                  variant="ghost"
                  disabled={index === listed.length - 1}
                  label={`Move ${column.label} down`}
                  onClick={() => move(column.id, 'down')}
                >
                  <ArrowDown size={13} />
                </IconButton>
              {column.locked ? (
                <Lock size={13} aria-label="Always shown" />
              ) : (
                <IconButton
                  size="sm"
                  variant="ghost"
                  aria-pressed={visible}
                  label={visible ? `Hide ${column.label}` : `Show ${column.label}`}
                  onClick={() =>
                    onChange({
                      ...prefs,
                      visible: { ...prefs.visible, [column.id]: !visible },
                    })
                  }
                >
                  {visible ? <Eye size={13} /> : <EyeOff size={13} />}
                </IconButton>
              )}
              </span>
            </div>
          )
        })}

        <div className="customize__section">
          <span>Row height</span>
          <ChipGroup
            label="Row height"
            layout="tight"
            value={prefs.height}
            options={HEIGHTS.map((height) => ({ value: height.id, label: height.label }))}
            onChange={(height) => onChange({ ...prefs, height })}
          />
        </div>

        <div className="customize__section">
          <button type="button" className="chip" onClick={() => onChange(defaultPrefs())}>
            Reset columns to defaults
          </button>
          <button
            type="button"
            className="chip"
            disabled={isDefaultOrder(prefs.order)}
            onClick={() => onChange({ ...prefs, order: defaultPrefs().order })}
          >
            Reset order
          </button>
        </div>
      </PopoverContent>
    </Popover>
  )
}
