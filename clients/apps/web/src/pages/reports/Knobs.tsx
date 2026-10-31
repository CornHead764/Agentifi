import { ChevronDown } from 'lucide-react'

import {
  Button,
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui'
import type { ReportConfig } from '@/lib/clients/reports'
import {
  COLUMN_DIMENSIONS,
  DIMENSION_LABELS,
  GRAIN_LABELS,
  TIME_GRAINS,
  asColumnDimension,
  asGrain,
  asRowDimension,
  rowDimensionOptions,
} from '@/lib/reports/labels'

/**
 * The report shell's controls: type, rows, and columns in Summary mode only.
 * The sign convention belongs to the preset and is not a knob.
 */
export function Knobs({
  config,
  onConfig,
}: {
  config: ReportConfig
  onConfig: (next: Partial<ReportConfig>) => void
}) {
  return (
    <>
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button size="sm">
            {config.mode === 'summary' ? 'Summary' : 'Transaction'} <ChevronDown size={13} />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent>
          <DropdownMenuLabel>Report type</DropdownMenuLabel>
          <DropdownMenuItem onSelect={() => onConfig({ mode: 'transaction' })}>
            Transaction — transactions organized by groups
          </DropdownMenuItem>
          <DropdownMenuItem onSelect={() => onConfig({ mode: 'summary' })}>
            Summary — totals grouped into rows and columns
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>

      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button size="sm">
            {DIMENSION_LABELS[config.rows]} <ChevronDown size={13} />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent>
          <DropdownMenuLabel>Rows</DropdownMenuLabel>
          <DropdownMenuRadioGroup
            value={config.rows}
            onValueChange={(next) => onConfig({ rows: asRowDimension(next) })}
          >
            {rowDimensionOptions(config.rows).map((dimension) => (
              <DropdownMenuRadioItem key={dimension} value={dimension}>
                {DIMENSION_LABELS[dimension]}
              </DropdownMenuRadioItem>
            ))}
          </DropdownMenuRadioGroup>
        </DropdownMenuContent>
      </DropdownMenu>

      {config.mode === 'summary' ? (
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button size="sm">
              {config.columns === 'time'
                ? GRAIN_LABELS[config.time_grain]
                : DIMENSION_LABELS[config.columns]}
              <ChevronDown size={13} />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent>
            <DropdownMenuLabel>Columns</DropdownMenuLabel>
            <DropdownMenuRadioGroup
              value={config.columns}
              onValueChange={(next) => onConfig({ columns: asColumnDimension(next) })}
            >
              {COLUMN_DIMENSIONS.map((dimension) => (
                <DropdownMenuRadioItem key={dimension} value={dimension}>
                  {DIMENSION_LABELS[dimension]}
                </DropdownMenuRadioItem>
              ))}
            </DropdownMenuRadioGroup>
            {config.columns === 'time' ? (
              <>
                <DropdownMenuSeparator />
                <DropdownMenuLabel>Time grain</DropdownMenuLabel>
                <DropdownMenuRadioGroup
                  value={config.time_grain}
                  onValueChange={(next) => onConfig({ time_grain: asGrain(next) })}
                >
                  {TIME_GRAINS.map((grain) => (
                    <DropdownMenuRadioItem key={grain} value={grain}>
                      {GRAIN_LABELS[grain]}
                    </DropdownMenuRadioItem>
                  ))}
                </DropdownMenuRadioGroup>
              </>
            ) : null}
          </DropdownMenuContent>
        </DropdownMenu>
      ) : null}
    </>
  )
}
