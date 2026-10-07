import { QueryClient, QueryClientProvider, type QueryKey } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { MemoryRouter } from 'react-router-dom'

import { ToastProvider, TooltipProvider } from '@/components/ui'
import { PrivacyProvider } from '@/contexts/PrivacyProvider'

export interface RenderScreenOptions {
  /** Query data in the cache before the first render, as `[key, data]` pairs. */
  seed?: readonly (readonly [QueryKey, unknown])[]
  /** The router's one location. */
  route?: string
  /** A client the test goes on to read; one is made when absent. */
  client?: QueryClient
}

/** A query client that fails at once rather than retrying a request no server answers. */
export function testQueryClient(): QueryClient {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

/**
 * The markup of `node` under every provider a screen expects of the app: the
 * query cache, privacy mode, toasts, tooltips and a router.
 */
export function renderScreen(node: ReactNode, options: RenderScreenOptions = {}): string {
  const client = options.client ?? testQueryClient()
  for (const [key, data] of options.seed ?? []) client.setQueryData(key, data)
  return renderToStaticMarkup(
    <QueryClientProvider client={client}>
      <PrivacyProvider>
        <ToastProvider>
          <TooltipProvider>
            <MemoryRouter initialEntries={[options.route ?? '/']}>{node}</MemoryRouter>
          </TooltipProvider>
        </ToastProvider>
      </PrivacyProvider>
    </QueryClientProvider>,
  )
}
