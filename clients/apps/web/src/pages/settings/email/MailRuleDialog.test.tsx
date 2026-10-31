/**
 * A bill rule for a company that is not a provider yet: the dialog adds the
 * company itself rather than sending the reader to the Bills screen first.
 */

import { describe, expect, it, vi } from 'vitest'

vi.mock('@/components/ui', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/components/ui')>()),
  ...(await import('@/test/dialogStub')).dialogStub,
}))

import { renderScreen } from '@/test/renderScreen'

import { MailRuleDialog } from './MailRuleDialog'
import { billMailRuleForm } from './mailRule'

function render(): string {
  return renderScreen(
    <MailRuleDialog
      rule={null}
      initial={billMailRuleForm({ id: 'conn-water' }, 'Example Water')}
      onClose={() => {}}
    />,
  )
}

describe('MailRuleDialog for a bill', () => {
  it('adds a company without a connector in place', () => {
    const html = render()
    expect(html).toContain('Add a company')
    expect(html).not.toContain('Settings → Bill providers')
  })

  it('opens already named for the provider it files on', () => {
    expect(render()).toContain('Example Water bills')
  })
})
