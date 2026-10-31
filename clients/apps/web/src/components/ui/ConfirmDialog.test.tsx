/**
 * The in-app confirmation, and the guard that keeps it the only one. Radix
 * portals the dialog body outside `renderToStaticMarkup`'s reach, so what is
 * asserted is that no screen uses `window.confirm`, which is unstyled in dark
 * mode, suppressed in an installed PWA, and blocks the event loop.
 */

import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'

import { ConfirmDialog } from './ConfirmDialog'

/** Every source file in the app, read as text by the bundler. */
const SOURCES = import.meta.glob('/src/**/*.{ts,tsx}', {
  query: '?raw',
  import: 'default',
  eager: true,
}) as Record<string, string>

describe('ConfirmDialog', () => {
  it('renders without crashing, open or closed', () => {
    for (const open of [true, false]) {
      expect(() =>
        renderToStaticMarkup(
          <ConfirmDialog
            open={open}
            title="Delete this transaction?"
            description="Its transfer partner is released."
            onConfirm={() => {}}
            onCancel={() => {}}
          />,
        ),
      ).not.toThrow()
    }
  })

  it('is the only confirmation in the app — nothing calls window.confirm', () => {
    // A glob that matched nothing would pass this test for the wrong reason.
    expect(Object.keys(SOURCES).length).toBeGreaterThan(100)
    const offenders = Object.entries(SOURCES)
      .filter(([path]) => !path.endsWith('ConfirmDialog.test.tsx'))
      .filter(([, source]) => /\bwindow\.confirm\s*\(|[^.\w]confirm\s*\(/.test(source))
      .map(([path]) => path)
    expect(offenders).toEqual([])
  })
})
