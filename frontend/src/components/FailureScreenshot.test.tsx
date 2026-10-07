import { renderToStaticMarkup } from 'react-dom/server'
import { afterEach, describe, expect, it, vi } from 'vitest'

vi.mock('@/components/ui', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/components/ui')>()),
  ...(await import('@/test/dialogStub')).dialogStub,
}))

import { ConnectorFailureNote } from './ConnectorFailureNote'
import {
  connectorFailureToast,
  showFailureScreenshot,
  useShownFailureScreenshot,
} from './failure-screenshot'
import { FailureScreenshotDialog } from './FailureScreenshot'

const PATH = '/bills/connections/c0ffee00-0000-4000-8000-000000000001/failure-screenshot'

afterEach(() => showFailureScreenshot(null))

describe('<ConnectorFailureNote> screenshot', () => {
  it('offers the page only when one was kept', () => {
    const raw = 'Something on the page covered the "Sign in" button'
    const shown = renderToStaticMarkup(
      <ConnectorFailureNote raw={raw} provider="Example Power" screenshot={{ path: PATH }} />,
    )
    expect(shown).toContain('<button type="button" class="link">Show screenshot</button>')

    const none = renderToStaticMarkup(<ConnectorFailureNote raw={raw} provider="Example Power" />)
    expect(none).not.toContain('Show screenshot')
  })

  it("offers a sign-in step's own page the same way", () => {
    const shown = renderToStaticMarkup(
      <ConnectorFailureNote
        raw="The password is incorrect"
        provider="Example Power"
        screenshot={{ image: 'iVBORw0KGgo=' }}
      />,
    )
    expect(shown).toContain('<button type="button" class="link">Show screenshot</button>')
    expect(shown).not.toContain('<img')
  })
})

describe('connectorFailureToast', () => {
  it('is an error toast whose action opens the page that was kept', () => {
    const toast = connectorFailureToast({
      title: 'Example Power was not updated',
      description: 'The statements page did not load.',
      provider: 'Example Power',
      screenshot: { path: PATH },
    })
    expect(toast).toMatchObject({ title: 'Example Power was not updated', tone: 'error' })
    expect(toast.action?.label).toBe('Show screenshot')

    toast.action?.onSelect()
    let shown: ReturnType<typeof useShownFailureScreenshot> = null
    function Probe() {
      shown = useShownFailureScreenshot()
      return null
    }
    renderToStaticMarkup(<Probe />)
    expect(shown).toEqual({ path: PATH, provider: 'Example Power' })
  })

  it('carries no action when no page was kept', () => {
    const toast = connectorFailureToast({
      title: 'Example Power was not updated',
      provider: 'Example Power',
      screenshot: null,
    })
    expect(toast.tone).toBe('error')
    expect(toast.action).toBeUndefined()
  })
})

describe('<FailureScreenshotDialog>', () => {
  it('is closed until a screenshot is asked for, and closes again on null', () => {
    expect(renderToStaticMarkup(<FailureScreenshotDialog />)).toBe('')

    showFailureScreenshot({ path: PATH, provider: 'Example Power' })
    const open = renderToStaticMarkup(<FailureScreenshotDialog />)
    expect(open).toContain('The page Example Power showed')
    expect(open).toContain('Anything typed into the page is blanked out.')

    showFailureScreenshot(null)
    expect(renderToStaticMarkup(<FailureScreenshotDialog />)).toBe('')
  })

  it("shows a sign-in step's own page without asking the server for it", () => {
    showFailureScreenshot({ image: 'iVBORw0KGgo=', provider: 'Example Power' })
    const open = renderToStaticMarkup(<FailureScreenshotDialog />)
    expect(open).toContain('src="data:image/png;base64,iVBORw0KGgo="')
  })
})
