import { useEffect, useState } from 'react'

import { Callout, Spinner } from '@/components/ui'
import { fetchBillTrail, type BillTrailEntry } from '@/lib/clients/bills'
import type { Uuid } from '@/lib/clients/entities'

import { trailText } from './signIn'

/**
 * What the provider showed, one line per round or page, folded under its
 * summary: in the sign-in dialog for the sign-in on screen, and on a
 * connection's card for its last update or the last sign-in that never
 * landed. Null is a trail still loading.
 */
export function ProviderTrail({
  trail,
  onOpen,
  gone = false,
}: {
  trail: BillTrailEntry[] | null
  onOpen?: () => void
  gone?: boolean
}) {
  return (
    <details
      className="bill-trail"
      onToggle={(event) => {
        if (event.currentTarget.open) onOpen?.()
      }}
    >
      <summary>What the provider showed</summary>
      {gone ? (
        <Callout tone="warning">
          This trail has gone: a sign-in or an update since has taken its place.
        </Callout>
      ) : trail === null ? (
        <Spinner size={14} />
      ) : (
        <>
          <p className="muted">
            One line per round or page, without anything you typed; under a page&rsquo;s line, its
            structure without typed text or long numbers. Safe to share for help.
          </p>
          <pre>{trailText(trail)}</pre>
        </>
      )}
    </details>
  )
}

/** The trail a connection kept from its last update or unfinished sign-in, read when unfolded. */
export function KeptTrail({ connectionId }: { connectionId: Uuid }) {
  const [open, setOpen] = useState(false)
  const [trail, setTrail] = useState<BillTrailEntry[] | null>(null)
  const [gone, setGone] = useState(false)
  useEffect(() => {
    if (!open || trail !== null || gone) return
    let dropped = false
    void fetchBillTrail(connectionId)
      .then((rounds) => {
        if (!dropped) setTrail(rounds)
      })
      .catch(() => {
        if (!dropped) setGone(true)
      })
    return () => {
      dropped = true
    }
  }, [open, trail, gone, connectionId])
  return <ProviderTrail trail={trail} gone={gone} onOpen={() => setOpen(true)} />
}
