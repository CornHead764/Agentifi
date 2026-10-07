/** The wording for the two exclusion flags, so each reads the same on every screen that offers it. */
export interface ExclusionCopy {
  /** Title Case, as the nav names the screen. */
  name: string
  verb: string
  state: string
  notState: string
  reach: string
}

export const EXCLUDED_FROM_REPORTS: ExclusionCopy = {
  name: 'Reports',
  verb: 'Exclude from reports',
  state: 'Excluded from reports',
  notState: 'Not excluded from reports',
  reach: 'Affects reports, watchlists and the Upcoming summary.',
}

export const EXCLUDED_FROM_SPENDING_PLAN: ExclusionCopy = {
  name: 'Spending Plan',
  verb: 'Exclude from the spending plan',
  state: 'Excluded from the spending plan',
  notState: 'Not excluded from the spending plan',
  reach: 'Affects the spending plan’s calculated amounts.',
}
