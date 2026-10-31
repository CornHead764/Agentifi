import { ArrowRightLeft, Check, X } from 'lucide-react'
import { useState } from 'react'
import { useNavigate } from 'react-router-dom'

import { Badge, Button, Card, RowActions, Table, Td, Th } from '@/components/ui'
import { useActiveSpace } from '@/contexts/space'
import {
  dayOf,
  ROLE_LABELS,
  useAcceptInvitation,
  useCurrentSpace,
  useDeclineInvitation,
  useInvitations,
  type Space,
} from '@/lib/clients/spaces'
import { describeApiError } from '@/lib/transactions/queries'

/**
 * What somebody has been asked to join, and the only place they can take it.
 * Until accepted, the space is absent from their listing and its pages refuse
 * them, so this hangs in the shell above every page and renders nothing until
 * there is an invitation.
 */
export function PendingInvitations() {
  const invitations = useInvitations()
  const accept = useAcceptInvitation()
  const decline = useDeclineInvitation()

  // Accepting grants access; it does not go there. Offered only while the
  // accepted space is not already the active one: for an invitee with no
  // space at all, the server resolves their only membership on its own.
  const [accepted, setAccepted] = useState<Space | null>(null)
  const current = useCurrentSpace()
  const { setActiveSpace } = useActiveSpace()
  const navigate = useNavigate()
  const offer = accepted && current.data?.id !== accepted.id ? accepted : null

  const rows = invitations.data ?? []
  if (rows.length === 0 && offer === null) return null

  // Said here rather than in a toast: somebody with no space has nothing else
  // on screen to read a failure against.
  const failure = accept.error ?? decline.error
  const busy = accept.isPending || decline.isPending

  const switchTo = (space: Space) => {
    setActiveSpace(space.id)
    setAccepted(null)
    // The page underneath is scoped to the space being left — an account
    // register, a month of a plan — and none of it exists in the new one.
    navigate('/')
  }

  const switchOffer = offer ? (
    <Card
      title={`You are now in ${offer.name}`}
      subtitle="Every screen is still reading your other space."
      actions={
        <Button variant="primary" size="sm" onClick={() => switchTo(offer)}>
          <ArrowRightLeft size={13} /> Switch to {offer.name}
        </Button>
      }
    />
  ) : null

  if (rows.length === 0) return switchOffer

  return (
    <>
      {switchOffer}
      <Card
        title={rows.length === 1 ? 'You have been invited' : `${rows.length} invitations`}
        subtitle={
          failure ? describeApiError(failure) : 'An invitation grants nothing until you accept it.'
        }
        flush
      >
        <Table density="sm">
          <thead>
            <tr>
              <Th>Space</Th>
              <Th>Role</Th>
              <Th />
            </tr>
          </thead>
          <tbody>
            {rows.map((invitation) => (
              <tr key={invitation.id}>
                <Td>
                  <span className="series__name">{invitation.space_name}</span>
                  <span className="muted">{sentOn(invitation.invited_at)}</span>
                </Td>
                <Td>
                  <Badge>{ROLE_LABELS[invitation.role]}</Badge>
                </Td>
                <Td numeric>
                  <RowActions>
                    <Button
                      variant="primary"
                      size="sm"
                      disabled={busy}
                      onClick={() =>
                        accept.mutate(invitation.id, { onSuccess: (space) => setAccepted(space) })
                      }
                    >
                      <Check size={13} /> Accept
                    </Button>
                    <Button
                      size="sm"
                      disabled={busy}
                      onClick={() => decline.mutate(invitation.id)}
                    >
                      <X size={13} /> Decline
                    </Button>
                  </RowActions>
                </Td>
              </tr>
            ))}
          </tbody>
        </Table>
      </Card>
    </>
  )
}

function sentOn(invitedAt: string | null): string {
  const day = dayOf(invitedAt)
  return day ? `Invited ${day}` : 'Invited'
}
