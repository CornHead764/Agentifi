import qrcode from 'qrcode-generator'
import { useMemo } from 'react'

export interface QrImageProps {
  /** What the code carries. Encoded as UTF-8 bytes. */
  value: string
  /** The accessible name. A code with no name is an image nobody can act on. */
  label: string
  /** Rendered size in CSS pixels, square. */
  size?: number
  className?: string
}

/**
 * A QR code as inline SVG. Black on white in both themes, since most cameras
 * refuse an inverted code; the white plate carries the quiet zone. Returns
 * nothing when the value cannot be encoded, leaving the caller's text fallback.
 */
export function QrImage({ value, label, size = 176, className }: QrImageProps) {
  const drawing = useMemo(() => {
    try {
      return draw(value, 4)
    } catch {
      return null
    }
  }, [value])

  if (drawing === null) return null

  return (
    <svg
      className={className}
      role="img"
      aria-label={label}
      width={size}
      height={size}
      viewBox={`0 0 ${drawing.span} ${drawing.span}`}
      shapeRendering="crispEdges"
    >
      <rect width={drawing.span} height={drawing.span} fill="#ffffff" />
      <path d={drawing.path} fill="#000000" />
    </svg>
  )
}

/** The dark modules as one SVG path, one unit per module, with `quietZone` units of margin. */
function draw(value: string, quietZone: number): { span: number; path: string } {
  const code = qrcode(0, 'M')
  // The library maps each UTF-16 unit to one byte, so hand it the UTF-8 bytes one per unit.
  code.addData(String.fromCharCode(...new TextEncoder().encode(value)), 'Byte')
  code.make()
  const size = code.getModuleCount()
  const parts: string[] = []
  for (let row = 0; row < size; row += 1) {
    for (let col = 0; col < size; col += 1) {
      if (code.isDark(row, col)) parts.push(`M${col + quietZone} ${row + quietZone}h1v1h-1z`)
    }
  }
  return { span: size + quietZone * 2, path: parts.join('') }
}
