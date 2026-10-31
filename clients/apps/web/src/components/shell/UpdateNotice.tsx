import { ExternalLink, X } from 'lucide-react'
import { useState } from 'react'

import { Button, Callout, IconButton } from '@/components/ui'
import { useAuth } from '@/contexts/auth'
import { useUpdateStatus } from '@/lib/clients/admin'
import { readStored, writeStored } from '@/lib/storage'

/** The release last waved away; a newer one shows again. */
const DISMISSED_KEY = 'update.dismissed'

/**
 * Said above every page to an administrator, the only person who can act on
 * it, while a newer release is out and until it is dismissed for that release.
 */
export function UpdateNotice() {
  const { user } = useAuth()
  const superuser = user?.is_superuser ?? false
  const status = useUpdateStatus(superuser)
  const [dismissed, setDismissed] = useState(() => readStored(DISMISSED_KEY))

  const update = status.data
  if (!superuser || !update?.update_available || dismissed === update.latest_version) return null

  const dismiss = () => {
    writeStored(DISMISSED_KEY, update.latest_version)
    setDismissed(update.latest_version)
  }

  return (
    <Callout
      className="update-notice"
      title={`Agentifi ${update.latest_version} is out`}
      actions={
        <>
          {update.url ? (
            <Button asChild size="sm">
              <a href={update.url} target="_blank" rel="noreferrer noopener">
                <ExternalLink size={13} aria-hidden="true" /> What’s new
              </a>
            </Button>
          ) : null}
          <IconButton size="sm" variant="ghost" label="Dismiss until the next release" onClick={dismiss}>
            <X size={13} />
          </IconButton>
        </>
      }
    >
      This server runs {update.current_version}. To update, run{' '}
      <code>docker compose pull && docker compose up -d</code> beside the compose file.
    </Callout>
  )
}
