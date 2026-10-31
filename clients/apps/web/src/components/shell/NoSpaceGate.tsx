import { Plus, Users } from 'lucide-react'
import { useState } from 'react'

import { PendingInvitations } from '@/components/shell/PendingInvitations'
import { Button, Card, EmptyState, NameDialog } from '@/components/ui'
import { useCreateSpace } from '@/lib/clients/spaces'

/**
 * What the application is for somebody not in a space yet. Every screen reads
 * a space, so without one each panel would answer "Space not found"; an
 * invitation grants nothing until it is accepted. The shell renders this
 * instead of the page until there is a space.
 */
export function NoSpaceGate() {
  const [creating, setCreating] = useState(false)
  const create = useCreateSpace()

  return (
    <main className="app__page">
      <PendingInvitations />
      <Card className="no-space">
        <EmptyState
          icon={<Users size={20} />}
          title="You are not in a space yet"
          body="A space holds one set of accounts, budgets and history. Accept an invitation, or start your own."

          action={
            <Button variant="primary" onClick={() => setCreating(true)}>
              <Plus size={13} /> New space
            </Button>
          }
        />
      </Card>

      <NameDialog
        title="New space"
        submitLabel="Create space"
        description="Your own set of finances. You will be its owner."
        placeholder="Household"
        open={creating}
        onOpenChange={setCreating}
        onSubmit={(name) => create.mutateAsync(name)}
      />
    </main>
  )
}
