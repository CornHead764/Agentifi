import type { ShortcutGroup } from '@/lib/transactions/shortcuts'

/** A cheat sheet, shared by the register's *Shortcuts* popover and the header's *Help*. */
export function ShortcutSheet({
  groups,
  lead,
}: {
  groups: readonly ShortcutGroup[]
  lead?: { heading: string; body: React.ReactNode }
}) {
  return (
    <>
      {lead ? (
        <>
          <p className="shortcuts__heading">{lead.heading}</p>
          <p className="shortcuts__hint">{lead.body}</p>
        </>
      ) : null}
      {groups.map((group) => (
        <div key={group.heading} className="shortcuts__group">
          <p className="shortcuts__heading">{group.heading}</p>
          <p className="shortcuts__hint">{group.hint}</p>
          {group.rows.map(([label, token]) => (
            <div key={token} className="shortcuts__row">
              <span>{label}</span>
              <code>{token}</code>
            </div>
          ))}
        </div>
      ))}
    </>
  )
}
