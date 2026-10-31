import { Plus, ShieldOff } from 'lucide-react'
import { useState } from 'react'

import { Button, Card, EmptyState, PageHeader } from '@/components/ui'
import { useAuth } from '@/contexts/auth'

import { BackupsCard } from './admin/BackupsCard'
import { ServerCard } from './admin/ServerCard'
import { SettingsCard } from './admin/SettingsCard'
import { SignInCard } from './admin/SignInCard'
import { SpacesCard } from './admin/SpacesCard'
import { UsersCard } from './admin/UsersCard'

/**
 * Running the server from the app. The guard is not access control (the API
 * refuses every `/admin` route to an ordinary account); it only replaces a
 * page of failed queries with a sentence.
 */
export function AdminSettings() {
  const { user } = useAuth()
  const [adding, setAdding] = useState(false)

  // Null while `/auth/me` answers; refusing over a guess would flash the
  // refusal at an administrator.
  if (user === null) return null

  if (!user.is_superuser) {
    return (
      <>
        <PageHeader title="Server admin" />
        <Card>
          <EmptyState
            icon={<ShieldOff size={20} aria-hidden="true" />}
            title="Not available"
            body="For server administrators only."
          />
        </Card>
      </>
    )
  }

  return (
    <>
      <PageHeader
        title="Server admin"
        actions={
          <Button variant="primary" size="sm" onClick={() => setAdding(true)}>
            <Plus size={13} aria-hidden="true" /> Add user
          </Button>
        }
      />
      <UsersCard me={user} adding={adding} onAddingChange={setAdding} />
      <SpacesCard />
      <SettingsCard />
      <SignInCard />
      <BackupsCard />
      <ServerCard />
    </>
  )
}
