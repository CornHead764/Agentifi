/**
 * The Connect transport, with no tie to React or the browser's storage, so
 * the same stack serves another client of this API: the session is whatever
 * the caller hands in. JSON with the proto's own field names, the wire the
 * server writes.
 */

import type { Transport } from '@connectrpc/connect'
import { createConnectTransport } from '@connectrpc/connect-web'

import { sessionFailures, sessionHeaders, type Session } from './interceptors'

export type { Session } from './interceptors'

export interface RpcTransportOptions {
  /** Where the API is mounted: `/api`, or a whole origin and path. */
  baseUrl: string
  session: Session
  /** Defaults to the global `fetch`, looked up per call so a stand-in installed later is used. */
  fetch?: typeof globalThis.fetch
}

export function createRpcTransport({ baseUrl, session, fetch }: RpcTransportOptions): Transport {
  return createConnectTransport({
    baseUrl,
    useBinaryFormat: false,
    jsonOptions: { useProtoFieldName: true },
    interceptors: [sessionHeaders(session), sessionFailures(session)],
    fetch: fetch ?? ((input, init) => globalThis.fetch(input, init)),
  })
}
