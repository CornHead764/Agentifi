/**
 * The bill providers, mirroring `domain.Billers` (the source of truth); this
 * side holds only what a screen renders.
 *
 * Keep each display name on the same line as its id: line-based tooling reads
 * the pair together.
 */
export type BillerId =
  | 'alliant'
  | 'erie'
  | 'spectrum'
  | 'we-energies'
  | 't-mobile'
  | 'apple'
  | 'rsync-net'
  | 'community-connect'
  | 'northwestern-mutual'
  | 'trugreen'
  | 'mychart'
  | 'email-only'

/** Plain HTTP with a kept token, Chromium with a kept profile, or the mailbox alone. */
export type BillerAccess = 'api' | 'browser' | 'email'

export interface Biller {
  id: BillerId
  name: string
  access: BillerAccess
  /** Empty for a provider with no customer portal. */
  homeUrl: string
  /** Whether a pull can fetch the statement file itself (not whether the portal has one). */
  hasDocuments: boolean
  /** One product deployed per customer: a connection must name its site before it can sign in. */
  site?: BillerSite
  /**
   * Its statements are filed as receipts on the card payment that settled each
   * one, and its pull reads the payments; it feeds no recurring reminder.
   */
  medical?: boolean
  /** The email-only entry for any company: the connection's (required) name is the company. */
  generic?: boolean
}

/** How a connection names its deployment, in the words of the field that asks. */
export interface BillerSite {
  label: string
  hint: string
  placeholder: string
  /** A whole web address (the server keeps the portal's root), not one hostname label. */
  address: boolean
}

export const BILLERS: readonly Biller[] = [
  {
    id: 'alliant', name: 'Alliant Energy',
    access: 'api',
    homeUrl: 'https://myaccount.alliantenergy.com',
    hasDocuments: true,
  },
  {
    id: 'erie', name: 'Erie Insurance',
    access: 'browser',
    homeUrl: 'https://www.erieinsurance.com',
    hasDocuments: true,
  },
  {
    id: 'spectrum', name: 'Spectrum',
    access: 'browser',
    homeUrl: 'https://www.spectrum.net',
    hasDocuments: true,
  },
  {
    id: 'we-energies', name: 'We Energies',
    access: 'browser',
    homeUrl: 'https://www.we-energies.com',
    hasDocuments: true,
  },
  {
    id: 't-mobile', name: 'T-Mobile',
    access: 'browser',
    homeUrl: 'https://www.t-mobile.com',
    hasDocuments: true,
  },
  {
    id: 'apple', name: 'Apple',
    access: 'email',
    homeUrl: 'https://reportaproblem.apple.com',
    hasDocuments: false,
  },
  {
    id: 'rsync-net', name: 'rsync.net',
    access: 'browser',
    homeUrl: 'https://www.rsync.net',
    hasDocuments: true,
  },
  {
    // Deployed once per municipality; the community is set on the connection.
    id: 'community-connect', name: 'Our Community Connect',
    access: 'browser',
    homeUrl: 'https://www.ourcommunityconnect.com',
    hasDocuments: true,
    site: {
      label: 'Which community?',
      hint: 'The part before .ourcommunityconnect.com, e.g. "exampletown".',
      placeholder: 'exampletown',
      address: false,
    },
  },
  {
    // The portal offers no statement document; payments and the next due are read.
    id: 'northwestern-mutual', name: 'Northwestern Mutual',
    access: 'browser',
    homeUrl: 'https://www.northwesternmutual.com',
    hasDocuments: false,
  },
  {
    id: 'trugreen', name: 'TruGreen',
    access: 'browser',
    homeUrl: 'https://www.trugreen.com',
    hasDocuments: true,
  },
  {
    // Every health system runs its own; the connection keeps its address.
    id: 'mychart', name: 'MyChart',
    access: 'browser',
    homeUrl: 'https://www.mychart.org',
    hasDocuments: true,
    medical: true,
    site: {
      label: 'MyChart address',
      hint: 'Copy it from the address bar while signed in to your health system’s MyChart; any page will do.',
      placeholder: 'https://mychart.example.org/MyChart',
      address: true,
    },
  },
  {
    id: 'email-only', name: 'Emailed bills',
    access: 'email',
    homeUrl: '',
    hasDocuments: false,
    generic: true,
  },
]

export function billerById(id: string): Biller | undefined {
  return BILLERS.find((one) => one.id === id)
}

/** Falls back to the raw id, which is what identifies a biller this build does not know. */
export function billerName(id: string): string {
  return billerById(id)?.name ?? id
}

/** The order every list of providers is shown in: by name, ignoring case. */
export function compareProviderNames(a: string, b: string): number {
  return a.localeCompare(b, undefined, { sensitivity: 'base' })
}

/** The connection's name when it was given one, else the provider's. */
export function connectionName(connection: { biller: string; label: string }): string {
  const named = connection.label.trim()
  return named === '' ? billerName(connection.biller) : named
}

/**
 * For a heading, with the provider: "Our Community Connect · Water". The
 * email-only provider's heading is the connection's name alone.
 */
export function connectionTitle(connection: { biller: string; label: string }): string {
  const named = connection.label.trim()
  const provider = billerName(connection.biller)
  if (named !== '' && billerById(connection.biller)?.generic === true) return named
  return named === '' ? provider : `${provider} · ${named}`
}

/** Connections in the order the add-provider catalogue lists providers. */
export function sortedByTitle<T extends { biller: string; label: string }>(rows: readonly T[]): T[] {
  return [...rows].sort((a, b) => compareProviderNames(connectionTitle(a), connectionTitle(b)))
}
