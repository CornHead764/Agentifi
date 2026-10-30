/** Hand the browser a file to save under `filename`. A no-op without a DOM. */
export function saveBlob(blob: Blob, filename: string): void {
  if (typeof document === 'undefined') return
  const url = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url
  link.download = filename
  link.click()
  URL.revokeObjectURL(url)
}
