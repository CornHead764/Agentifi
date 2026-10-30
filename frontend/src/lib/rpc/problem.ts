import { Code, ConnectError } from '@connectrpc/connect'

import { ProblemSchema, type Problem } from '@/gen/agentifi/v1/common_pb'

/** The Problem detail every refusal from this server carries. */
export function problemOf(error: ConnectError): Problem | undefined {
  return error.findDetails(ProblemSchema)[0]
}

/**
 * The status the REST API answers with for a refusal: the Problem's when it
 * carries one, else the usual mapping (a body the schema refused before any
 * handler ran is a 422).
 */
export function statusOf(error: ConnectError): number {
  const status = problemOf(error)?.status
  if (status) return status
  switch (error.code) {
    case Code.InvalidArgument:
      return 422
    case Code.Unauthenticated:
      return 401
    case Code.PermissionDenied:
      return 403
    case Code.NotFound:
    case Code.Unimplemented:
      return 404
    case Code.FailedPrecondition:
    case Code.AlreadyExists:
    case Code.Aborted:
      return 409
    case Code.ResourceExhausted:
      return 429
    case Code.Unavailable:
      return 503
    case Code.Canceled:
      return 499
    default:
      return 500
  }
}
