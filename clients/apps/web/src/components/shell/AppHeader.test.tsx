/**
 * The header, and the pages that borrow it through `contexts/headerOverride`.
 * A borrowed bar replaces the row rather than joining it. The handover runs in
 * an effect this static suite never reaches, so the context is driven directly.
 */

import { renderToStaticMarkup } from 'react-dom/server'
import { MemoryRouter } from 'react-router-dom'
import { describe, expect, it } from 'vitest'

import { TooltipProvider } from '@/components/ui'
import { AuthContext, type AuthValue } from '@/contexts/auth'
import { HeaderOverrideContext, useHeaderOverrideNode } from '@/contexts/headerOverride'
import { ThemeContext, type ThemeValue } from '@/contexts/theme'

import { AppHeader } from './AppHeader'

const noop = () => undefined
const never = () => Promise.resolve()

const AUTH = {
  user: null,
  status: 'authenticated',
  signIn: () => Promise.resolve({ mfaToken: null, methods: [] }),
  completeSecondFactor: never,
  adoptToken: never,
  signOut: never,
  changePassword: never,
} as unknown as AuthValue

const THEME: ThemeValue = { preference: 'dark', resolved: 'dark', setPreference: noop }

function render(node: React.ReactNode): string {
  return renderToStaticMarkup(
    <MemoryRouter>
      <AuthContext value={AUTH}>
        <ThemeContext value={THEME}>
          <TooltipProvider>{node}</TooltipProvider>
        </ThemeContext>
      </AuthContext>
    </MemoryRouter>,
  )
}

/** The shell's one line: whatever the page put there, the header draws. */
function ShellHeader() {
  return <AppHeader title="Transactions" override={useHeaderOverrideNode()} />
}

describe('the app header', () => {
  it('draws its title and its actions when no page has taken it over', () => {
    const html = render(<AppHeader title="Transactions" />)

    expect(html).toContain('Transactions')
    expect(html).toContain('Notifications')
    expect(html).toContain('Account and preferences')
  })

  // One control in the row, not also an item in the account menu.
  it('offers exactly one way into settings', () => {
    const html = render(<AppHeader title="Transactions" />)

    expect(html.match(/href="\/settings\/general"/g)).toHaveLength(1)
    expect(html).toContain('aria-label="Settings"')
  })

  it('draws a page bar in place of the whole row, not over it', () => {
    const html = render(
      <AppHeader title="Transactions" override={<div className="selection-bar">2 selected</div>} />,
    )

    expect(html).toContain('2 selected')
    expect(html).not.toContain('Transactions')
    expect(html).not.toContain('Notifications')
    expect(html).not.toContain('Account and preferences')
  })

  // The register's column headings stick underneath the header and are placed
  // from `--header-height`, so the bar has to keep the header's own box.
  it('keeps the header element the rest of the app is positioned against', () => {
    const html = render(<AppHeader title="Transactions" override={<span>1 selected</span>} />)

    expect(html).toContain('<header class="header">')
  })

  it('takes the bar from the context, which is how a page reaches it', () => {
    const taken = render(
      <HeaderOverrideContext value={{ node: <span>3 selected</span>, set: noop }}>
        <ShellHeader />
      </HeaderOverrideContext>,
    )
    expect(taken).toContain('3 selected')

    // And gives the row back the moment the page stops asking.
    const given = render(
      <HeaderOverrideContext value={{ node: null, set: noop }}>
        <ShellHeader />
      </HeaderOverrideContext>,
    )
    expect(given).toContain('Transactions')
  })
})
