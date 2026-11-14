/**
 * The app's Connect client: `lib/rpc` bound to this browser's session, with a
 * refusal thrown as the `ApiError` every screen already reads.
 */

import type { DescMethod, DescService } from '@bufbuild/protobuf'
import { createClient, type Client } from '@connectrpc/connect'

import { activeSpaceId, forgetActiveSpace } from './activeSpace'
import { API_BASE, API_CREDENTIALS } from './api'
import { toApiError } from './rpc/errors'
import { createRpcTransport } from './rpc/transport'
import { currentSession, markSignedOut } from './session'

const transport = createRpcTransport({
  baseUrl: API_BASE,
  credentials: API_CREDENTIALS,
  session: {
    signedIn: () => currentSession() !== null,
    space: activeSpaceId,
    signedOut: markSignedOut,
    spaceGone: forgetActiveSpace,
  },
})

export function rpcClient<Service extends DescService>(service: Service): Client<Service> {
  return createClient(service, transport)
}

/** One call to `method`, a refusal thrown as an `ApiError` whose path is the procedure. */
export async function unary<T>(method: DescMethod, run: () => Promise<T>): Promise<T> {
  try {
    return await run()
  } catch (error) {
    throw toApiError(error, `/${method.parent.typeName}/${method.name}`)
  }
}
