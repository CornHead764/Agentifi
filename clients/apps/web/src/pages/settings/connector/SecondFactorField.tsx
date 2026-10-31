import { Link } from 'react-router-dom'

import { Callout, Field, Input, OptionSelect } from '@/components/ui'

import {
  asksForKey,
  mailboxMissing,
  SECOND_FACTOR_CHOICES,
  secondFactorChoice,
  type SecondFactorChoice,
} from './secondFactor'

/** The second-factor question below the password, shared by the bill and shop dialogs. */
export function SecondFactorField({
  provider,
  choice,
  onChoice,
  authKey,
  onAuthKey,
  hasMailbox,
}: {
  provider: string
  choice: SecondFactorChoice
  onChoice: (choice: SecondFactorChoice) => void
  authKey: string
  onAuthKey: (key: string) => void
  hasMailbox: boolean | undefined
}) {
  return (
    <>
      <Field
        label="Second factor"
        hint={
          'The only way the sign-in picks when asked. Emailed codes and authenticator keys let ' +
          'updates sign in unattended; a code sent by text is typed in at the sign-in.'
        }
      >
        <OptionSelect
          value={choice}
          onValueChange={(next) => onChoice(secondFactorChoice(next))}
          aria-label="Second factor"
          options={SECOND_FACTOR_CHOICES}
        />
      </Field>
      {mailboxMissing(choice, hasMailbox) ? (
        <Callout tone="warning">
          <p>
            No mailbox is connected to read {provider}&rsquo;s codes from.{' '}
            <Link to="/settings/email?add=mailbox">Connect a mailbox</Link>, or the code will be
            asked for here.
          </p>
        </Callout>
      ) : null}
      {asksForKey(choice) ? (
        <Field
          label="Authenticator setup key"
          hint={`Shown by ${provider} when adding an authenticator app; Agentifi makes the codes. Blank means a code is asked for here.`}
        >
          <Input
            value={authKey}
            onChange={(event) => onAuthKey(event.target.value)}
            autoComplete="off"
            spellCheck={false}
          />
        </Field>
      ) : null}
    </>
  )
}
