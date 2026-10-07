/**
 * The API client, and the single coercion point for money: every response
 * passes through `coerceMoney` with a shape naming its money fields, and
 * nothing downstream calls `parseMoney` itself. A money field left out of a
 * shape is caught only at runtime, by `auditUndeclaredMoney` in dev.
 */

import { activeSpaceId, forgetActiveSpace, SPACE_HEADER } from './activeSpace'
import { parseMoney, type Money } from './money'
import { accessToken, clearAccessToken } from './session'
import { isRecord } from './typeGuards'

export { formatMoney, type Money } from './money'

/** Exported because the OIDC login is a real navigation built by hand, and must use the same base. */
export const API_BASE: string = import.meta.env.VITE_API_BASE ?? '/api'

/**
 * Which fields of `T` carry money. `never` on scalars so declaring a plain
 * `number` or `string` as `'money'` fails to compile.
 */
export type MoneyShape<T> = T extends readonly (infer E)[]
  ? // An array of bare amounts is declared `'money'`, like a single amount.
    E extends Money
    ? 'money'
    : MoneyShape<E>
  : T extends object
    ? {
        [K in keyof T]?: NonNullable<T[K]> extends Money
          ? 'money'
          : MoneyShape<NonNullable<T[K]>>
      }
    : never

type AnyShape = { [field: string]: 'money' | AnyShape | undefined }

/**
 * Replace declared string fields with `Money`, in place on the parsed JSON.
 * Mutating is deliberate: a copy would double the allocation on large registers.
 */
export function coerceMoney<T>(body: unknown, shape?: MoneyShape<T>): T {
  if (shape) walk(body, shape as AnyShape)
  return body as T
}

function walk(value: unknown, shape: AnyShape): void {
  if (Array.isArray(value)) {
    for (const element of value) walk(element, shape)
    return
  }
  if (!isRecord(value)) return

  for (const [field, fieldShape] of Object.entries(shape)) {
    const current = value[field]
    if (current === null || current === undefined) continue

    if (fieldShape === 'money') {
      // An array here is a column of amounts; passing it whole to parseMoney throws.
      value[field] = Array.isArray(current)
        ? current.map((entry) => parseMoney(entry as string))
        : parseMoney(current as string)
    } else if (fieldShape) {
      walk(current, fieldShape)
    }
  }
}

/** `-1900.00`, `0.00` — the exact shape a quantized `Decimal` serializes to. */
const LOOKS_LIKE_MONEY = /^-?\d+\.\d{2}$/

/**
 * Warn about strings that survived coercion still looking like money. A
 * warning, not a throw: rates and percentages are `Decimal` on the server too.
 */
export function auditUndeclaredMoney(body: unknown, path = '$'): void {
  if (typeof body === 'string') {
    if (LOOKS_LIKE_MONEY.test(body)) {
      console.warn(
        `[api] ${path} is ${JSON.stringify(body)} — a money field missing from its MoneyShape?`,
      )
    }
    return
  }
  if (Array.isArray(body)) {
    body.forEach((element, index) => auditUndeclaredMoney(element, `${path}[${index}]`))
    return
  }
  if (isRecord(body)) {
    for (const [field, value] of Object.entries(body)) {
      auditUndeclaredMoney(value, `${path}.${field}`)
    }
  }
}

export class ApiError extends Error {
  readonly status: number
  readonly path: string
  readonly body: unknown
  /** From `X-Request-Id`: what a user can read off a failure screen to find the log line. */
  readonly requestId: string | null

  constructor(status: number, path: string, body: unknown, requestId: string | null = null) {
    super(`${status} on ${path}`)
    this.name = 'ApiError'
    this.status = status
    this.path = path
    this.body = body
    this.requestId = requestId
  }

  /** The server's sentence. A 422's detail is a list of field errors; this returns the first. */
  get detail(): string | null {
    if (!isRecord(this.body)) return null
    const detail = this.body.detail
    if (typeof detail === 'string') return detail
    if (Array.isArray(detail)) {
      const first = detail[0]
      if (isRecord(first) && typeof first.msg === 'string') return first.msg
    }
    return null
  }

  /** The machine-readable reason, e.g. `password_change_required`, which routes. */
  get code(): string | null {
    if (!isRecord(this.body)) return null
    return typeof this.body.code === 'string' ? this.body.code : null
  }
}

/** Parsed if JSON, verbatim if not: a proxy can answer a 502 with an HTML page. */
function parseBody(text: string): unknown {
  if (!text) return null
  try {
    return JSON.parse(text)
  } catch {
    return text
  }
}

/** Read per request, not at module load, so a fetch after a sign-in or space switch carries the new values. */
function sessionHeaders(token: string | null, space: string | null): Record<string, string> {
  return {
    ...(token ? { Authorization: `Bearer ${token}` } : {}),
    ...(space ? { [SPACE_HEADER]: space } : {}),
  }
}

/** Optional-chained because test stand-ins for `Response` carry no headers. */
function requestIdOf(response: Response): string | null {
  return response.headers?.get('X-Request-Id') ?? null
}

/**
 * Whether the named space is gone. The sentence is the contract (only
 * `auth.ErrNoSpace` produces it); any other 404 must not drop the choice.
 */
function spaceIsGone(status: number, body: unknown): boolean {
  return status === 404 && isRecord(body) && body.detail === 'Space not found'
}

interface SendOptions {
  /** False sends no token or space, so a failure cannot sign out or drop the space. */
  session?: boolean
  /** False for a blob: an attachment may be HTML in its own right. */
  json?: boolean
}

/**
 * The one fetch. It adds the session headers and, on a failure, applies the
 * sign-out and forgotten-space rules before throwing an `ApiError`.
 */
async function send(
  path: string,
  init: Omit<RequestInit, 'headers'> & { headers?: Record<string, string> },
  { session = true, json = true }: SendOptions = {},
): Promise<Response> {
  const token = session ? accessToken() : null
  const space = session ? activeSpaceId() : null
  const response = await fetch(`${API_BASE}${path}`, {
    ...init,
    credentials: 'same-origin',
    headers: {
      ...(json ? { Accept: 'application/json' } : {}),
      ...sessionHeaders(token, space),
      ...init.headers,
    },
  })

  if (!response.ok) {
    if (response.status === 401 && token) clearAccessToken()
    const parsed = parseBody(await response.text())
    if (space && spaceIsGone(response.status, parsed)) forgetActiveSpace()
    throw new ApiError(response.status, path, parsed, requestIdOf(response))
  }

  // An older server answers an unknown path with index.html and a 200.
  if (json && response.headers?.get('Content-Type')?.startsWith('text/html')) {
    throw new ApiError(404, path, null, requestIdOf(response))
  }
  return response
}

async function readJson<T>(response: Response, path: string, shape?: MoneyShape<T>): Promise<T> {
  const coerced = coerceMoney<T>(parseBody(await response.text()), shape)
  if (import.meta.env.DEV) auditUndeclaredMoney(coerced, path)
  return coerced
}

interface RequestOptions<T> {
  shape?: MoneyShape<T>
  signal?: AbortSignal
  body?: unknown
}

async function request<T>(
  method: string,
  path: string,
  { shape, signal, body }: RequestOptions<T> = {},
): Promise<T> {
  const response = await send(path, {
    method,
    signal,
    headers: body === undefined ? {} : { 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  return readJson(response, path, shape)
}

/**
 * Post a file as multipart. Content-Type is left to the browser so the
 * boundary matches the body. `fileField` is the part's name (bills read `document`).
 */
export async function upload<T>(
  path: string,
  file: File,
  fields: Record<string, string> = {},
  fileField = 'file',
  shape?: MoneyShape<T>,
): Promise<T> {
  return uploadFiles(path, { [fileField]: file }, fields, shape)
}

/** `upload` for a form that carries several files, keyed by field name. */
export async function uploadFiles<T>(
  path: string,
  files: Record<string, File>,
  fields: Record<string, string> = {},
  shape?: MoneyShape<T>,
): Promise<T> {
  const form = new FormData()
  for (const [field, file] of Object.entries(files)) form.append(field, file)
  for (const [key, value] of Object.entries(fields)) form.append(key, value)
  return readJson(await send(path, { method: 'POST', body: form }), path, shape)
}

/** Fetch binary content as a Blob: the request needs the bearer token, which an `<img src>` cannot send. */
export async function blob(path: string, signal?: AbortSignal): Promise<Blob> {
  return (await send(path, { signal }, { json: false })).blob()
}

/** Post a form-encoded body; only `/auth/token` (the OAuth2 password grant) takes one. */
async function postForm<T>(path: string, values: Record<string, string>): Promise<T> {
  const response = await send(
    path,
    {
      method: 'POST',
      headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      body: new URLSearchParams(values).toString(),
    },
    { session: false },
  )
  return readJson(response, path)
}

export const api = {
  form: postForm,
  upload,
  uploadFiles,
  blob,
  get: <T>(path: string, shape?: MoneyShape<T>, signal?: AbortSignal) =>
    request<T>('GET', path, { shape, signal }),
  post: <T>(path: string, body?: unknown, shape?: MoneyShape<T>) =>
    request<T>('POST', path, { body, shape }),
  patch: <T>(path: string, body?: unknown, shape?: MoneyShape<T>) =>
    request<T>('PATCH', path, { body, shape }),
  put: <T>(path: string, body?: unknown, shape?: MoneyShape<T>) =>
    request<T>('PUT', path, { body, shape }),
  delete: <T>(path: string, shape?: MoneyShape<T>) => request<T>('DELETE', path, { shape }),
}
