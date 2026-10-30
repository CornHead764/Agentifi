/**
 * Messages to and from the shapes the app already works in, directed by the
 * schema rather than by a list of fields: every `agentifi.v1.Money` becomes
 * `Money` (integer cents) on the way in and a decimal string on the way out,
 * so an amount cannot be left undeclared the way a `MoneyShape` field can.
 *
 * The app shape is the REST wire's: proto field names, null for an unset
 * field, an enum as its lower-case suffix, a 64-bit integer as a number.
 */

import {
  create,
  fromJson,
  ScalarType,
  toJson,
  type DescEnum,
  type DescField,
  type DescMessage,
  type JsonValue,
  type Message,
  type MessageShape,
} from '@bufbuild/protobuf'
import { FieldMaskSchema } from '@bufbuild/protobuf/wkt'

import { amountToWire, parseMoney, type Money } from '@/lib/money'

const MONEY = 'agentifi.v1.Money'
const NULLABLE_MONEY = 'agentifi.v1.NullableMoney'

type Plain = Record<string, unknown>

function isMoney(desc: DescMessage): boolean {
  return desc.typeName === MONEY || desc.typeName === NULLABLE_MONEY
}

/** A google.protobuf type, whose JSON form (a timestamp string, arbitrary JSON) is already the app's. */
function isWellKnown(desc: DescMessage): boolean {
  return desc.typeName.startsWith('google.protobuf.')
}

/** A response message in the app's shape. Unchecked, like `coerceMoney`: `T` must describe the message. */
export function fromWire<T>(schema: DescMessage, message: Message): T {
  return plainMessage(schema, message) as T
}

function plainMessage(desc: DescMessage, message: Message): unknown {
  const value = message as unknown as Plain
  if (message.$typeName === MONEY || message.$typeName === NULLABLE_MONEY) {
    return parseMoney(String(value.amount))
  }
  if (isWellKnown(desc)) return toJson(desc, message)
  const out: Plain = {}
  for (const field of desc.fields) {
    out[field.name] = plainField(field, memberOf(value, field))
  }
  return out
}

/** A real oneof keeps its members under the oneof's name, as `{ case, value }`. */
function memberOf(message: Plain, field: DescField): unknown {
  if (field.oneof === undefined) return message[field.localName]
  const chosen = message[field.oneof.localName] as { case?: string; value?: unknown } | undefined
  return chosen?.case === field.localName ? chosen.value : undefined
}

function plainField(field: DescField, value: unknown): unknown {
  if (value === undefined || value === null) return null
  switch (field.fieldKind) {
    case 'list':
      return (value as unknown[]).map((entry) => plainSingle(field.message, field.enum, entry))
    case 'map':
      return Object.fromEntries(
        Object.entries(value as Plain).map(([key, entry]) => [key, plainSingle(field.message, field.enum, entry)]),
      )
    default:
      return plainSingle(field.message, field.enum, value)
  }
}

function plainSingle(message: DescMessage | undefined, enumDesc: DescEnum | undefined, value: unknown): unknown {
  if (message) return plainMessage(message, value as Message)
  if (enumDesc) return enumName(enumDesc, value as number)
  if (typeof value === 'bigint') return Number(value)
  return value
}

function enumName(desc: DescEnum, number: number): string | null {
  const value = desc.values.find((candidate) => candidate.number === number)
  if (!value || value.number === 0) return null
  return value.name.slice(desc.sharedPrefix?.length ?? 0).toLowerCase()
}

/** A request message from the app's shape. A null or absent field is left unset. */
export function toWire<Desc extends DescMessage>(schema: Desc, plain: object): MessageShape<Desc> {
  return create(schema, initOf(schema, plain as Plain) as never)
}

/**
 * An Update request: `ids` names the row, and every key of `patch` that is
 * present goes in `update_mask`, a null one to clear the field.
 */
export function toPatch<Desc extends DescMessage>(schema: Desc, ids: object, patch: object): MessageShape<Desc> {
  const paths = Object.entries(patch)
    .filter(([, value]) => value !== undefined)
    .map(([key]) => key)
  const message = toWire(schema, { ...ids, ...patch })
  ;(message as unknown as Plain).updateMask = create(FieldMaskSchema, { paths })
  return message
}

function initOf(desc: DescMessage, plain: Plain): Plain {
  const init: Plain = {}
  for (const field of desc.fields) {
    const value = plain[field.name]
    if (value === undefined || value === null) continue
    const converted = initField(field, value)
    if (field.oneof === undefined) init[field.localName] = converted
    else init[field.oneof.localName] = { case: field.localName, value: converted }
  }
  return init
}

function initField(field: DescField, value: unknown): unknown {
  switch (field.fieldKind) {
    case 'list':
      return (value as unknown[]).map((entry) => initSingle(field, entry))
    case 'map':
      return Object.fromEntries(Object.entries(value as Plain).map(([key, entry]) => [key, initSingle(field, entry)]))
    default:
      return initSingle(field, value)
  }
}

function initSingle(field: DescField, value: unknown): unknown {
  const message = field.message
  if (message) {
    if (isMoney(message)) return { amount: amountToWire(value as Money) }
    if (isWellKnown(message)) return fromJson(message, value as JsonValue)
    return initOf(message, value as Plain)
  }
  if (field.enum) return enumNumber(field.enum, String(value))
  if (field.scalar !== undefined && INT64.has(field.scalar) && typeof value === 'number') return BigInt(value)
  return value
}

const INT64: ReadonlySet<ScalarType> = new Set([
  ScalarType.INT64,
  ScalarType.UINT64,
  ScalarType.FIXED64,
  ScalarType.SFIXED64,
  ScalarType.SINT64,
])

function enumNumber(desc: DescEnum, text: string): number {
  const name = (desc.sharedPrefix ?? '') + text.toUpperCase()
  const value = desc.values.find((candidate) => candidate.name === name)
  if (!value) throw new TypeError(`${JSON.stringify(text)} is not a value of ${desc.typeName}`)
  return value.number
}
