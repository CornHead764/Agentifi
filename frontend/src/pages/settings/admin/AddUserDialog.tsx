import { useState } from 'react'

import {
  Button,
  Checkbox,
  DialogActions,
  DialogContent,
  Field,
  FormDialog,
  Input,
  OptionSelect,
  Radio,
  RadioGroup,
} from '@/components/ui'
import { asPlacement, type AdminSpace, type NewUserForm } from '@/lib/clients/admin'
import { asRole, ROLE_LABELS, ROLES } from '@/lib/clients/spaces'
import { chosenPasswordReady, MIN_PASSWORD_LENGTH } from '@/lib/passwords'

interface AddUserProps {
  spaces: AdminSpace[]
  currency: string
  pending: boolean
  onCancel: () => void
  onConfirm: (form: NewUserForm) => void
}

/**
 * Making somebody an account. A space is required: an account with no
 * membership authenticates and then sees nothing. A blank password lets the
 * server mint one, shown once.
 */
export function AddUserDialog({ open, ...props }: AddUserProps & { open: boolean }) {
  return (
    <FormDialog
      open={open}
      onOpenChange={(next) => {
        if (!next) props.onCancel()
      }}
    >
      <AddUserForm {...props} />
    </FormDialog>
  )
}

function AddUserForm({ spaces, currency, pending, onCancel, onConfirm }: AddUserProps) {
  const [form, setForm] = useState<NewUserForm>({
    email: '',
    full_name: '',
    password: '',
    must_change_password: true,
    is_superuser: false,
    placement: 'new',
    space_name: '',
    currency,
    space_id: spaces[0]?.id ?? '',
    role: 'member',
  })
  const [generate, setGenerate] = useState(true)

  const change = (patch: Partial<NewUserForm>) => setForm({ ...form, ...patch })
  const ready =
    form.email.trim() !== '' &&
    (form.placement === 'new' ? form.space_name.trim() !== '' : form.space_id !== '') &&
    chosenPasswordReady(generate, form.password)

  return (
    <DialogContent
      title="New user"
      description="Nothing is emailed; hand the password over yourself."
      onSubmit={(event) => {
        event.preventDefault()
        if (!ready || pending) return
        onConfirm({ ...form, password: generate ? '' : form.password })
      }}
      footer={
        <DialogActions onCancel={onCancel}>
          <Button type="submit" variant="primary" disabled={!ready || pending}>
            {pending ? 'Creating…' : 'Create user'}
          </Button>
        </DialogActions>
      }
    >
      <Field label="Email">
        <Input
          type="email"
          value={form.email}
          placeholder="them@example.com"
          autoComplete="off"
          onChange={(event) => change({ email: event.target.value })}
        />
      </Field>

      <Field label="Name" hint="Left blank, the part of the address before the @ is used.">
        <Input
          value={form.full_name}
          placeholder="Their name"
          onChange={(event) => change({ full_name: event.target.value })}
        />
      </Field>

      <Field
        label="Password"
        hint={
          generate
            ? 'Generated and shown once; it cannot be read back.'
            : `At least ${MIN_PASSWORD_LENGTH} characters.`
        }
      >
        <Checkbox
          checked={generate}
          onCheckedChange={(next) => setGenerate(next === true)}
          label="Generate a temporary one"
        />
      </Field>

      {!generate ? (
        <Field label="Their password">
          <Input
            type="password"
            value={form.password}
            autoComplete="new-password"
            onChange={(event) => change({ password: event.target.value })}
          />
        </Field>
      ) : null}

      <Field label="First sign-in">
        <Checkbox
          checked={form.must_change_password}
          onCheckedChange={(next) => change({ must_change_password: next === true })}
          label="Must choose their own password before they can read anything"
        />
      </Field>

      <Field
        label="Rights"
        hint="Admins add accounts, change single sign-on and see every space."
      >
        <Checkbox
          checked={form.is_superuser}
          onCheckedChange={(next) => change({ is_superuser: next === true })}
          label="Let this account administer the server"
        />
      </Field>

      <Field label="Where they land" as="group">
        <RadioGroup
          value={form.placement}
          onValueChange={(value) => change({ placement: asPlacement(value) })}
        >
          <Radio value="new" label="A new space, which they will own" />
          <Radio
            value="existing"
            label="A space that already exists"
            disabled={spaces.length === 0}
          />
        </RadioGroup>
      </Field>

      {form.placement === 'new' ? (
        <>
          <Field label="Space name">
            <Input
              value={form.space_name}
              placeholder="Household"
              onChange={(event) => change({ space_name: event.target.value })}
            />
          </Field>
          <Field label="Currency" hint="Every figure in that space is reported in it.">
            <Input
              value={form.currency}
              maxLength={3}
              onChange={(event) => change({ currency: event.target.value.toUpperCase() })}
            />
          </Field>
        </>
      ) : (
        <>
          <Field label="Space">
            <OptionSelect
              value={form.space_id}
              onValueChange={(value) => change({ space_id: value })}
              placeholder="Select space"
              options={spaces.map((space) => ({ value: space.id, label: space.name }))}
            />
          </Field>
          <Field label="Role">
            <OptionSelect
              value={form.role}
              onValueChange={(value) => change({ role: asRole(value) })}
              options={ROLES.map((role) => ({ value: role, label: ROLE_LABELS[role] }))}
            />
          </Field>
        </>
      )}
    </DialogContent>
  )
}
