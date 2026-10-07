/**
 * Your name and the space's reporting currency.
 *
 * Changing the currency restamps the ledger: every stored conversion was made
 * against the old currency and the stamping pass only fills blanks, so the
 * server clears and remakes them.
 */

import { useState } from 'react'

import {
  Button,
  Card,
  Field,
  Input,
  useToast,
} from '@/components/ui'
import { useAuth } from '@/contexts/auth'
import { api } from '@/lib/api'
import { useInvalidatingMutation } from '@/lib/queryClient'
import { authKeys } from '@/lib/session'

/**
 * The shape of a BCP 47 tag, the same rule as the server's `isLanguageTag`
 * (authroutes.go). There is deliberately no list of accepted tags.
 */
function isLanguageTag(tag: string): boolean {
  return tag.length <= 35 && /^[A-Za-z]{2,8}(-[A-Za-z0-9]{1,8})*$/.test(tag)
}

export function PersonalDetailsCard() {
  const { user } = useAuth()
  const { show } = useToast()

  const [name, setName] = useState(user?.full_name ?? '')
  const [locale, setLocale] = useState(user?.locale ?? '')

  const trimmedLocale = locale.trim()
  const localeValid = trimmedLocale === '' || isLanguageTag(trimmedLocale)
  const changed =
    (user?.full_name ?? '') !== name.trim() || (user?.locale ?? '') !== trimmedLocale

  // The header reads the same query, so it has to be told rather than left
  // showing the old name until the next reload.
  const saving = useInvalidatingMutation(
    () =>
      api.patch('/auth/me', {
        full_name: name.trim() || null,
        // Null rather than "": the server reads a cleared locale as its own
        // default, and an empty string would be a tag that never validates.
        locale: trimmedLocale || null,
      }),
    [authKeys.me],
    {
      failure: 'Your details were not saved',
      onSuccess: () => show({ title: 'Details saved', tone: 'success' }),
    },
  )

  const saveDetails = () => {
    if (saving.isPending || !localeValid) return
    saving.mutate()
  }


  return (
    <Card title="Personal details" subtitle="How you appear to everyone else in this space.">
        <Field
          label="Display name"
          hint="Blank shows your email."
        >
          <Input
            value={name}
            onChange={(event) => setName(event.target.value)}
            placeholder={user?.email ?? ''}
            autoComplete="name"
          />
        </Field>
        <Field
          label="Email"
          hint="Your login. It cannot be changed."
        >
          <Input value={user?.email ?? ''} readOnly disabled />
        </Field>
        <Field
          label="Locale"
          hint="Formats your dates and numbers, e.g. en-US. Blank uses the server's."
          error={localeValid ? undefined : `"${trimmedLocale}" is not a language tag`}
        >
          <Input
            value={locale}
            onChange={(event) => setLocale(event.target.value)}
            placeholder="en-US"
            autoComplete="language"
            spellCheck={false}
          />
        </Field>
        <Button
          variant="primary"
          disabled={saving.isPending || !changed || !localeValid}
          onClick={saveDetails}
        >
          Save details
        </Button>
      </Card>

  )
}
