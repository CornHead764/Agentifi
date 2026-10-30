import { describeBulk, type BulkResult, type BulkToast } from '@/components/plan/bulkEnvelope'
import type { Occurrence } from '@/lib/clients/upcoming'
import { plural } from '@/lib/format'

/** What the toast says after a run: how many were skipped, and which were not and why. */
export function skippedToast(result: BulkResult<Occurrence>): BulkToast {
  const attempted = result.done.length + result.failed.length
  if (result.failed.length === 0) {
    return {
      title: `Skipped ${plural(attempted, 'reminder')}`,
      description: 'No payments recorded. The series carry on.',
      tone: 'success',
    }
  }
  return {
    title: `Skipped ${result.done.length} of ${plural(attempted, 'reminder')}`,
    description: describeBulk(result, (one) => `${one.label} (${one.due_on})`, 'Skipped')
      .description,
    tone: 'error',
  }
}
