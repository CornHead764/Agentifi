import { useState, type ReactNode } from 'react'

import { useSignIns } from '@/components/signin/signInTasks-context'
import { billerById, connectionTitle } from '@/lib/billers'
import { billProviderOf, useBillAgent, type BillConnection } from '@/lib/clients/bills'

import { MailRuleDialog } from '../email/MailRuleDialog'
import { billMailRuleForm } from '../email/mailRule'

/**
 * What adding a bill provider leads to: a provider Agentifi signs in to opens
 * its sign-in; a company tracked from its e-mailed bills opens the mail rule
 * that will file them, already pointed at it.
 */
export function useProviderFollowUp(): {
  /** Run once a provider has been added. */
  next: (connection: BillConnection) => void
  /** Open the mail rule for an e-mailed provider on its own. */
  writeMailRule: (connection: BillConnection) => void
  /** Mount wherever the hook is used. */
  dialog: ReactNode
} {
  const signIns = useSignIns()
  const agent = useBillAgent()
  const [mailRuleFor, setMailRuleFor] = useState<BillConnection | null>(null)

  const next = (connection: BillConnection) => {
    const biller = billerById(connection.biller)
    if (biller?.generic === true) {
      setMailRuleFor(connection)
      return
    }
    const provider = billProviderOf(agent.data, connection.biller)
    const access = provider?.access ?? biller?.access ?? 'browser'
    if (access === 'api' || access === 'browser') {
      signIns.open({ kind: 'bill', connection, provider })
    }
  }

  const dialog =
    mailRuleFor === null ? null : (
      <MailRuleDialog
        rule={null}
        initial={billMailRuleForm(mailRuleFor, connectionTitle(mailRuleFor))}
        onClose={() => setMailRuleFor(null)}
      />
    )

  return { next, writeMailRule: setMailRuleFor, dialog }
}
