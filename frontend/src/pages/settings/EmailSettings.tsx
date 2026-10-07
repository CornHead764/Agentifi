import { Plus } from 'lucide-react'
import { useState } from 'react'
import { useSearchParams } from 'react-router-dom'

import { Button, PageHeader } from '@/components/ui'
import { useEmailConnections } from '@/lib/clients/email'

import { MailboxCard } from './email/MailboxCard'
import { MailboxDialog } from './email/MailboxDialog'
import { MailRulesSection } from './email/MailRulesSection'
import { RecentMail } from './email/RecentMail'

/**
 * Email: the mailboxes, what arrived in them, and the household's own mail
 * rules.
 */
export function EmailSettings() {
  const connections = useEmailConnections()

  // `?add=mailbox` arrives from a sign-in that wanted a mailbox to read its
  // codes from, and lands on the form rather than the list.
  const [params, setParams] = useSearchParams()
  const [adding, setAddingState] = useState(() => params.get('add') === 'mailbox')
  const setAdding = (open: boolean) => {
    setAddingState(open)
    if (!open && params.has('add')) {
      const next = new URLSearchParams(params)
      next.delete('add')
      setParams(next, { replace: true })
    }
  }

  return (
    <>
      <PageHeader
        title="Email"
        actions={
          <Button variant="primary" size="sm" onClick={() => setAdding(true)}>
            <Plus size={13} aria-hidden="true" /> Add mailbox
          </Button>
        }
      />
      <MailboxCard />
      <RecentMail connections={connections.data} />
      <MailRulesSection />

      {adding ? (
        <MailboxDialog connection={null} onClose={() => setAdding(false)} />
      ) : null}
    </>
  )
}
