import { ChevronDown } from 'lucide-react'

import { AreaTrend, labeledPoints } from '@/components/charts'
import { ChangeBadge } from '@/components/figures'
import { Money } from '@/components/Money'
import { QueryBoundary } from '@/components/QueryBoundary'
import {
  Button,
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from '@/components/ui'
import { rangeLabel, type RangePreset } from '@/lib/dateRanges'
import { useNetWorth } from '@/lib/clients/networth'
import { usePreferredRange } from '@/lib/defaultRange'

const NET_WORTH_RANGES: readonly RangePreset[] = ['1M', '3M', '6M', '1Y', 'ALL']

export function NetWorthPanel() {
  const [range, setRange] = usePreferredRange('6M')
  const worth = useNetWorth(range)

  return (
    <>
      <div className="widget__controls">
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button size="sm">
              {rangeLabel(range)} <ChevronDown size={12} />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            <DropdownMenuRadioGroup
              value={range}
              onValueChange={(next) =>
                setRange(NET_WORTH_RANGES.find((one) => one === next) ?? '6M')
              }
            >
              {NET_WORTH_RANGES.map((one) => (
                <DropdownMenuRadioItem key={one} value={one}>
                  {rangeLabel(one)}
                </DropdownMenuRadioItem>
              ))}
            </DropdownMenuRadioGroup>
          </DropdownMenuContent>
        </DropdownMenu>
      </div>
      <QueryBoundary query={worth} rows={5}>
        {(data) => (
          <>
            <div className="widget__headline">
              <Money value={data.end.net} tone="neutral" className="figure--total" />
              <ChangeBadge amount={data.change} rate={data.change_pct} size="sm" />
              <span className="muted">
                {data.included_accounts}/{data.total_accounts} accounts
              </span>
            </div>
            <AreaTrend
              height={180}
              points={labeledPoints(data.points, (point) => point.net)}
            />
          </>
        )}
      </QueryBoundary>
    </>
  )
}
