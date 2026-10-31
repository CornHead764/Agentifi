/** CSV cells, rows and the download, for every export the app writes. */

import { saveBlob } from '@/lib/saveFile'

/**
 * Neutralize spreadsheet formula injection in one text cell (payees come off
 * bank feeds). Text cells only, as the server does: prefixing a negative
 * amount would make the column unparseable.
 */
export function csvText(cell: string): string {
  return /^[=+\-@\t\r]/.test(cell) ? `'${cell}` : cell
}

export function csvRow(cells: readonly string[]): string {
  return cells.map(escape).join(',')
}

/** RFC 4180: quote anything with a comma, a quote or a newline; double the quotes. */
function escape(cell: string): string {
  if (!/[",\r\n]/.test(cell)) return cell
  return `"${cell.replace(/"/g, '""')}"`
}

/** A no-op without a DOM. */
export function downloadCsv(filename: string, content: string): void {
  saveBlob(new Blob([content], { type: 'text/csv;charset=utf-8' }), filename)
}
