import { Field, Input } from '@/components/ui'
import type { BillerSite } from '@/lib/billers'

/**
 * Which deployment of a per-customer provider a connection signs in to. The
 * server folds what is typed into the form it keeps, so nothing is folded here.
 */
export function SiteField({
  site,
  value,
  onChange,
  canWait = false,
}: {
  site: BillerSite
  value: string
  onChange: (value: string) => void
  /** Adding a connection: the site can still be named at its first sign-in. */
  canWait?: boolean
}) {
  return (
    <Field label={site.label} hint={canWait ? `${site.hint} Can wait until sign-in.` : site.hint}>
      <Input
        value={value}
        onChange={(event) => onChange(event.target.value)}
        placeholder={site.placeholder}
        inputMode={site.address ? 'url' : undefined}
        autoComplete="off"
        spellCheck={false}
      />
    </Field>
  )
}
