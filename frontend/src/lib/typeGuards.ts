/** Parsed JSON or a stored blob, narrowed before a field on it is read. */

/** A non-null object, arrays included: JSON's whole "not a primitive" shape. */
export function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null
}

/** A JSON object specifically, excluding an array a model or a stored blob might send instead. */
export function isPlainObject(value: unknown): value is Record<string, unknown> {
  return isRecord(value) && !Array.isArray(value)
}
