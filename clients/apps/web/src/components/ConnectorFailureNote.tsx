import type { ReactNode } from 'react'

import type { ScreenshotSource } from '@/components/failure-screenshot'
import { FailureScreenshotLink } from '@/components/FailureScreenshot'
import { Callout } from '@/components/ui'
import { explainConnectorFailure } from '@/lib/connectorFailure'

/**
 * A connector's failure: the plain sentence, the page it stopped on when one
 * was kept, and the engine's words folded under it.
 */
export function ConnectorFailureNote({
  raw,
  provider,
  screenshot = null,
  children,
}: {
  raw: string
  provider: string
  /** The page the failure stopped on, when one was kept. */
  screenshot?: ScreenshotSource | null
  /** More of the account under the page: a connector's kept trail. */
  children?: ReactNode
}) {
  const { message, detail } = explainConnectorFailure(raw, provider)
  return (
    <Callout tone="warning" role="alert">
      <p>{message}</p>
      {screenshot === null ? null : (
        <p>
          <FailureScreenshotLink {...screenshot} provider={provider} />
        </p>
      )}
      {children}
      {detail === null ? null : (
        <details className="connector-failure__details">
          <summary>Details</summary>
          <code>{detail}</code>
        </details>
      )}
    </Callout>
  )
}
