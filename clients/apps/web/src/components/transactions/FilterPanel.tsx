import { ListFilter } from 'lucide-react'
import { useState } from 'react'

import {
  Badge,
  Button,
  DialogActions,
  Popover,
  PopoverContent,
  PopoverTrigger,
} from '@/components/ui'
import { FilterFacets } from '@/components/transactions/FilterFacets'
import { REGISTER_FACETS } from '@/components/transactions/facets'
import { activeFacetCount, withoutFacets, type FilterDraft } from '@/lib/transactions/filter'
import type { Account, Category, Tag } from '@/lib/transactions/types'

export interface FilterPanelProps {
  draft: FilterDraft
  categories: readonly Category[]
  tags: readonly Tag[]
  accounts: readonly Account[]
  /** Every payee in the space, with the loaded rows as a loading fallback. */
  payees: readonly string[]
  onApply: (draft: FilterDraft) => void
}

/**
 * The facets, editing a copy; nothing is applied until Apply, since each change
 * refetches. Clear applies at once: it clears the register's filter, and with
 * it whichever quick filter set those facets.
 */
export function FilterPanel({
  draft,
  categories,
  tags,
  accounts,
  payees,
  onApply,
}: FilterPanelProps) {
  const [open, setOpen] = useState(false)
  const [working, setWorking] = useState(draft)

  const active = activeFacetCount(draft)

  const start = (next: boolean) => {
    if (next) setWorking(draft)
    setOpen(next)
  }

  return (
    <Popover open={open} onOpenChange={start}>
      <PopoverTrigger asChild>
        <Button size="sm" variant={active > 0 ? 'primary' : 'secondary'}>
          <ListFilter size={13} /> Filter
          {active > 0 ? <Badge count>{active}</Badge> : null}
        </Button>
      </PopoverTrigger>
      <PopoverContent flush className="filter-panel">
        <FilterFacets
          draft={working}
          categories={categories}
          tags={tags}
          accounts={accounts}
          payees={payees}
          facets={REGISTER_FACETS}
          onChange={setWorking}
        />

        <div className="filter-panel__footer">
          <DialogActions
            onCancel={() => setOpen(false)}
            start={
              <Button
                variant="ghost"
                onClick={() => {
                  onApply(withoutFacets(draft))
                  setOpen(false)
                }}
              >
                Clear facets
              </Button>
            }
          >
            <Button
              variant="primary"
              onClick={() => {
                onApply(working)
                setOpen(false)
              }}
            >
              Apply
            </Button>
          </DialogActions>
        </div>
      </PopoverContent>
    </Popover>
  )
}
