/**
 * A body longer than this is cut to a few lines until somebody asks for the
 * rest. The ones that run long are a connector's error passed on whole, and
 * the first lines say what stopped; the rest is detail for whoever fixes it.
 */
export const LONG_NOTIFICATION = 160

export function isLongNotification(body: string | undefined): boolean {
  return (body?.length ?? 0) > LONG_NOTIFICATION
}
