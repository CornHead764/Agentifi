import type { DescMessage } from '@bufbuild/protobuf'

/**
 * A procedure's fixture, keyed `POST /agentifi.v1.Service/Method`: `answer`
 * takes the request message's JSON and returns the response in the REST wire's
 * shape, which this turns into the message's JSON (each Money a `{ amount }`).
 */
export function procedure(schema: DescMessage, answer: (request: Record<string, unknown>) => unknown) {
  return (_query: URLSearchParams, body: unknown) => messageJson(schema, answer(isRecord(body) ? body : {}))
}

function messageJson(schema: DescMessage, value: unknown): unknown {
  if (!isRecord(value)) return value
  if (schema.typeName === 'agentifi.v1.Money' || schema.typeName === 'agentifi.v1.NullableMoney') {
    return value
  }
  const out: Record<string, unknown> = {}
  for (const field of schema.fields) {
    const entry = value[field.name]
    if (entry === undefined || entry === null) continue
    const message = field.message
    const one = (item: unknown) => {
      if (message?.typeName === 'agentifi.v1.Money' || message?.typeName === 'agentifi.v1.NullableMoney') {
        return { amount: item }
      }
      return message ? messageJson(message, item) : item
    }
    out[field.name] = Array.isArray(entry) ? entry.map(one) : one(entry)
  }
  return out
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}
