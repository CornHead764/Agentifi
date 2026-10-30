import { Zap } from 'lucide-react'

import {
  Button,
  Callout,
  Popover,
  PopoverContent,
  PopoverTrigger,
  SearchInput,
} from '@/components/ui'
import { SEARCH_SHORTCUTS } from '@/lib/transactions/shortcuts'

import { ShortcutSheet } from './ShortcutSheet'

export interface SearchBoxProps {
  value: string
  onChange: (value: string) => void
}

/** The register's search box, with its Shortcuts sheet. The parent debounces the parse. */
export function SearchBox({ value, onChange }: SearchBoxProps) {
  return (
    <SearchInput
      size="sm"
      value={value}
      placeholder="Search transactions"
      aria-label="Search transactions"
      onChange={onChange}
    >
      <Popover>
        <PopoverTrigger asChild>
          <Button variant="ghost" size="sm">
            <Zap size={12} /> Shortcuts
          </Button>
        </PopoverTrigger>
        <PopoverContent className="shortcuts">
          <ShortcutSheet
            groups={SEARCH_SHORTCUTS}
            lead={{
              heading: 'Pro tip',
              body: (
                <>
                  Add a space between searches to apply them together, e.g.{' '}
                  <code>date=this-month amount&gt;500</code>
                </>
              ),
            }}
          />
        </PopoverContent>
      </Popover>
    </SearchInput>
  )
}

/**
 * What the search could not do. A term the filter cannot express is reported
 * rather than answered with a wrong result set.
 */
export function SearchNotes({
  limitations,
  errors,
}: {
  /** Terms the parser understood but the stored filter cannot express. */
  limitations: readonly string[]
  errors: readonly string[]
}) {
  return (
    <>
      {errors.map((error) => (
        <Callout key={error} tone="expense">
          {error}
        </Callout>
      ))}
      {limitations.map((limitation) => (
        <Callout key={limitation} tone="warning">
          {limitation}
        </Callout>
      ))}
    </>
  )
}
