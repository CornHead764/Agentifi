/**
 * A notice above the register when two sources have recorded what looks like
 * one charge, linking to the review list. Absent when nothing waits, and while
 * the count is unknown, so the register does not jump.
 */

import { CopyCheck } from 'lucide-react'
import { Link } from 'react-router-dom'

import { Button, Callout } from '@/components/ui'
import { useDuplicates } from '@/lib/clients/duplicates'
import { plural } from '@/lib/format'

export const DUPLICATES_PATH = '/settings/duplicates'

export function DuplicatesStrip() {
  const duplicates = useDuplicates()
  const count = duplicates.data?.count ?? 0
  if (count === 0) return null

  return (
    <Callout
      tone="warning"
      role="status"
      icon={<CopyCheck size={14} />}
      actions={
        <Button asChild size="sm">
          <Link to={DUPLICATES_PATH}>Review</Link>
        </Button>
      }
    >
      {plural(count, 'possible duplicate')} found: the same amount on one account, a day or two
      apart, from different sources. Both are counted until you decide.
    </Callout>
  )
}
