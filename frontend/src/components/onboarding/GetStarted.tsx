import { FileUp, Landmark, PenLine } from 'lucide-react'
import { useState } from 'react'
import { Link } from 'react-router-dom'

import { NewAccountDialog } from '@/components/shell/NewAccountDialog'
import { Button, Card } from '@/components/ui'
import { useVisibleSetupGuide } from '@/lib/clients/setupGuide'
import { useCurrentSpace } from '@/lib/clients/spaces'

import { CONNECT_PATH, IMPORT_PATH } from './paths'
import { SetupGuide } from './SetupGuide'
import { SimplifiImportDialog } from './SimplifiImportDialog'

/**
 * What a space with no accounts shows in place of a page of zeros: the setup
 * guide while it is shown, with the ways in it does not walk through, or else
 * every way in on one card.
 */
export function GetStarted() {
  const guide = useVisibleSetupGuide()

  if (guide) {
    return (
      <>
        <SetupGuide guide={guide} />
        <Card className="get-started" title="Other ways in">
          <WaysIn connect={false} />
        </Card>
      </>
    )
  }

  return (
    <Card className="get-started" title="Welcome to Agentifi">
      <p className="get-started__lede">
No accounts yet. Add them one of these ways.</p>
      <WaysIn connect />
      <MigrationNote />
    </Card>
  )
}

function WaysIn({ connect }: { connect: boolean }) {
  const [addingByHand, setAddingByHand] = useState(false)

  return (
    <>
      <ul className="get-started__ways">
        {connect ? (
          <li className="get-started__way">
            <Landmark size={18} aria-hidden="true" className="get-started__icon" />
            <p className="get-started__title">Connect your banks</p>
            <p className="get-started__body">
Via SimpleFIN Bridge. One connection covers every linked bank.</p>
            <Button asChild variant="primary" size="sm">
              <Link to={CONNECT_PATH}>Connect with SimpleFIN</Link>
            </Button>
          </li>
        ) : null}
        <li className="get-started__way">
          <FileUp size={18} aria-hidden="true" className="get-started__icon" />
          <p className="get-started__title">Import a statement</p>
          <p className="get-started__body">
OFX, QFX or CSV. Previewed before saving.</p>
          <Button asChild size="sm">
            <Link to={IMPORT_PATH}>Import a file</Link>
          </Button>
        </li>
        <li className="get-started__way">
          <PenLine size={18} aria-hidden="true" className="get-started__icon" />
          <p className="get-started__title">Add an account by hand</p>
          <p className="get-started__body">
Cash, a loan, a house: anything no bank reports.</p>
          <Button size="sm" onClick={() => setAddingByHand(true)}>
            Add an account
          </Button>
        </li>
      </ul>

      <NewAccountDialog open={addingByHand} onOpenChange={setAddingByHand} />
    </>
  )
}

/** The one way in to the Simplifi import, wherever an empty space is offered one. */
export function MigrationNote() {
  const space = useCurrentSpace()
  const [importing, setImporting] = useState(false)

  return (
    <div className="get-started__migrate">
      <p className="get-started__title">Moving from Quicken Simplifi?</p>
      {space.data?.is_owner ? (
        <>
          <p className="get-started__body">
            Bring your whole history into this space: accounts, transactions, categories, rules
            and the spending plan. You see what is in the file before anything is written.
          </p>
          <Button size="sm" onClick={() => setImporting(true)}>
            <FileUp size={13} aria-hidden="true" />
            Import from Simplifi
          </Button>
          <SimplifiImportDialog
            open={importing}
            onOpenChange={setImporting}
            matchPath={CONNECT_PATH}
          />
        </>
      ) : space.data ? (
        <p className="get-started__body">
          The space&rsquo;s owner or an admin can import a Simplifi export into it.
        </p>
      ) : null}
    </div>
  )
}
