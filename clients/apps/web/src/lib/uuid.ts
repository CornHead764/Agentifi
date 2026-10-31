/** Hex blocks of 8-4-4-4-12, the shape every id in this app takes unless it names something else, like a drawer group. */
const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i

export function isUuid(value: string): boolean {
  return UUID.test(value)
}
