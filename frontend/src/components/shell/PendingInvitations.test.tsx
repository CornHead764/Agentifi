import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { renderToStaticMarkup } from 'react-dom/server'
import { MemoryRouter } from 'react-router-dom'
import { describe, expect, it } from 'vitest'

import { INVITATIONS_KEY, type Invitation } from '@/lib/clients/spaces'

import { PendingInvitations } from './PendingInvitations'

function render(invitations: Invitation[]): string {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  client.setQueryData(INVITATIONS_KEY, invitations)
  // Accepting an invitation offers to switch to the space it was into, and
  // switching leaves the page behind, so the card needs a router.
  return renderToStaticMarkup(
    <QueryClientProvider client={client}>
      <MemoryRouter>
        <PendingInvitations />
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

const INVITATION: Invitation = {
  id: 'm-1',
  space_id: 's-1',
  space_name: 'Household',
  role: 'member',
  invited_at: '2026-08-21T22:15:00Z',
}

describe('<PendingInvitations>', () => {
  it('draws nothing when there is nothing to answer', () => {
    // It hangs above every page in the application, so an empty one has to
    // cost the screen nothing.
    expect(render([])).toBe('')
  })

  it('names the space and offers both answers', () => {
    // Until it is accepted the invitee cannot read that space, so nothing else
    // in the app will name it.
    const markup = render([INVITATION])
    expect(markup).toContain('Household')
    expect(markup).toContain('Member')
    expect(markup).toContain('Invited Aug 21, 2026')
    expect(markup).toContain('Accept')
    expect(markup).toContain('Decline')
    expect(markup).toContain('An invitation grants nothing until you accept it.')
  })

  it('counts them when there is more than one', () => {
    const markup = render([INVITATION, { ...INVITATION, id: 'm-2', space_name: 'Cabin' }])
    expect(markup).toContain('2 invitations')
    expect(markup).toContain('Cabin')
  })
})
