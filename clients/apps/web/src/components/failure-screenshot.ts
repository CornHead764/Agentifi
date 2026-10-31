import { useSyncExternalStore } from 'react'

import type { ToastOptions } from '@/components/ui'
import { createListenerSet } from '@/lib/listenerSet'

/**
 * Where the page a connector stopped on is: kept by the server and served at
 * `path`, or a sign-in step's own `image` (base64 PNG), which nothing keeps.
 */
export type ScreenshotSource = { path: string } | { image: string }

/** The page a connector stopped on, and whose page it was. */
export type FailureScreenshot = ScreenshotSource & { provider: string }

let showing: FailureScreenshot | null = null
const listeners = createListenerSet()

function readShowing(): FailureScreenshot | null {
  return showing
}

/**
 * Opens the one screenshot dialog, or closes it with null. A toast's action
 * calls it as well as a card's link, and a toast outlives the screen that
 * raised it, so the dialog belongs to the shell rather than to either.
 */
export function showFailureScreenshot(shot: FailureScreenshot | null): void {
  showing = shot
  listeners.notify()
}

export function useShownFailureScreenshot(): FailureScreenshot | null {
  return useSyncExternalStore(listeners.subscribe, readShowing, readShowing)
}

/** A toast's "Show screenshot", for a failure that left a page behind. */
export function failureScreenshotAction(
  shot: ScreenshotSource | null,
  provider: string,
): ToastOptions['action'] {
  if (shot === null) return undefined
  return { label: 'Show screenshot', onSelect: () => showFailureScreenshot({ ...shot, provider }) }
}

/**
 * Every toast that reports a connector stopping: an error, and the page it
 * stopped on when one was kept, as the connector's card offers it.
 */
export function connectorFailureToast({
  title,
  description,
  provider,
  screenshot,
}: {
  title: string
  description?: string
  provider: string
  screenshot: ScreenshotSource | null
}): ToastOptions {
  return {
    title,
    description,
    tone: 'error',
    action: failureScreenshotAction(screenshot, provider),
  }
}
