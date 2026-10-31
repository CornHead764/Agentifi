import { clsx } from 'clsx'

import { endsOnTint, filledLength, laySegments, type MeterSegment } from './meter-segments'

/** Below this share a reading printed inside the fill would be clipped to its first digit. */
const READING_FITS_FROM = 12

export interface MeterProps {
  /** Laid end to end from the left, each a percent of the track. */
  segments: readonly MeterSegment[]
  /**
   * Names the meter for a screen reader. Without one the meter is decoration
   * beside figures that already say the same.
   */
  label?: string
  /** The reading announced; the filled percent unless given. */
  value?: number
  max?: number
  valueText?: string
  /** `xs` is a hairline, `sm` and `md` sit in a row, `lg` holds a printed reading. */
  size?: 'xs' | 'sm' | 'md' | 'lg'
  /**
   * A percentage printed at `at`, the end of the length it counts: inside the
   * fill when it fits, at the far end of the track when it does not. The text
   * need not be `at`, since a fill stops at 100 and a reading may not.
   */
  reading?: { text: string; at: number }
  /** A tick at a percent of the track, for a scale rather than a fill. */
  marker?: number
  className?: string
}

/** A bar of one or more coloured segments over a track. */
export function Meter({
  segments,
  label,
  value,
  max = 100,
  valueText,
  size = 'md',
  reading,
  marker,
  className,
}: MeterProps) {
  const laid = laySegments(segments)
  const filled = filledLength(laid)

  return (
    <div
      className={clsx('meter', `meter--${size}`, className)}
      role={label === undefined ? undefined : 'meter'}
      aria-label={label}
      aria-valuemin={label === undefined ? undefined : 0}
      aria-valuemax={label === undefined ? undefined : max}
      aria-valuenow={label === undefined ? undefined : Math.round(value ?? filled)}
      aria-valuetext={label === undefined ? undefined : valueText}
      aria-hidden={label === undefined ? true : undefined}
    >
      <span className="meter__track">
        {laid.map((segment, index) =>
          segment.value > 0 ? (
            <span
              key={index}
              className={clsx(
                'meter__segment',
                `meter__segment--${segment.tone ?? 'accent'}`,
                segment.faded && 'meter__segment--faded',
              )}
              style={{ width: `${segment.value}%`, background: segment.color }}
            />
          ) : null,
        )}
      </span>
      {marker === undefined ? null : (
        <span className="meter__marker" style={{ left: `${Math.min(Math.max(marker, 0), 100)}%` }} />
      )}
      {reading === undefined ? null : reading.at > READING_FITS_FROM ? (
        <span
          className={clsx('meter__reading', endsOnTint(laid, reading.at) && 'meter__reading--on-tint')}
          style={{ right: `calc(100% - ${Math.min(reading.at, 100)}%)` }}
        >
          {reading.text}
        </span>
      ) : (
        <span className="meter__reading meter__reading--outside">{reading.text}</span>
      )}
    </div>
  )
}
