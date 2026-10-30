/**
 * What a delete is about to do, in words. An unread count says so, because
 * saying nothing would read as "nothing carries it". The count is of reviewed
 * transactions, as the unused list's is; the delete itself reaches
 * unreviewed ones too, and the sentences say so.
 */

import { reviewedTransactionCount } from '@/lib/clients/categories'

export interface UsageState {
  isError: boolean
  /** Undefined while the count is still being read. */
  count: number | undefined
}

/** Transactions filed under a deleted category keep it, so past reports still resolve the name. */
export function categoryDeleteWarning(usage: UsageState): string {
  if (usage.isError) {
    return 'How many reviewed transactions are filed under it could not be read just now. Whatever the number is, every transaction filed under it keeps the category and goes on showing its name.'
  }
  if (usage.count === undefined) return 'Counting the reviewed transactions filed under it…'
  if (usage.count === 0) {
    return 'No reviewed transactions are filed under it today. Any unreviewed transaction filed under it keeps the category.'
  }

  const one = usage.count === 1
  return `${reviewedTransactionCount(usage.count)} ${one ? 'stays' : 'stay'} filed under it and ${
    one ? 'goes' : 'go'
  } on showing its name in reports and in the spending plan, as does any unreviewed transaction filed under it. Recategorizing ${
    one ? 'it' : 'them'
  } is a separate job.`
}

/** The delete does not cascade: a subcategory reappears at the top level. */
export function strandedSubcategoryWarning(names: readonly string[]): string | null {
  if (names.length === 0) return null
  if (names.length === 1) {
    return `Its subcategory — ${names[0]} — is not deleted with it. It keeps pointing at a category that is gone, so it moves to the top of the list until you file it somewhere else.`
  }
  return `Its ${names.length} subcategories — ${names.join(
    ', ',
  )} — are not deleted with it. They keep pointing at a category that is gone, so they move to the top of the list until you file them somewhere else.`
}

/** A tag is unlinked from every transaction and split first; nothing puts the links back. */
export function tagDeleteWarning(usage: UsageState): string {
  if (usage.isError) {
    return 'How many reviewed transactions carry it could not be read just now. Whatever the number is, the tag comes off every transaction carrying it and nothing here puts it back.'
  }
  if (usage.count === undefined) return 'Counting the reviewed transactions carrying it…'
  if (usage.count === 0) {
    return 'No reviewed transactions carry it. It comes off any unreviewed transaction that does.'
  }

  const one = usage.count === 1
  return `${reviewedTransactionCount(usage.count)} ${one ? 'carries' : 'carry'} it. ${
    one ? 'It loses' : 'All of them lose'
  } the chip, as does any unreviewed transaction carrying it, and nothing here puts it back — a saved view or a rule filtering on this tag stops matching ${one ? 'it' : 'them'}.`
}
