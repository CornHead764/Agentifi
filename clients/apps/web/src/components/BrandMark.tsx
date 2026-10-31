const DOLLAR =
  'M 2.6,-3.4 C 1.3,-5.1 -2.8,-4.7 -2.6,-2.5 C -2.4,-0.6 2.4,-0.8 2.6,1.3 C 2.8,3.6 -1.5,4.3 -2.8,2.8'

/**
 * The Agentifi robot without its tile: whatever holds it supplies the rounded
 * square, in `--accent`, and the face is drawn in that same colour so it reads
 * as cut out. Same geometry as public/icon.svg and public/icon-maskable.svg —
 * change all three together.
 */
export function BrandMark({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 64 64" className={className} aria-hidden="true" focusable="false">
      <path
        d="M32,6.5 Q32.6,10.4 36.5,11 Q32.6,11.6 32,15.5 Q31.4,11.6 27.5,11 Q31.4,10.4 32,6.5 Z"
        fill="currentColor"
      />
      <line x1="32" y1="15" x2="32" y2="19.5" stroke="currentColor" strokeWidth="2.6" strokeLinecap="round" />
      <rect x="7.5" y="30" width="5" height="11" rx="2.5" fill="currentColor" />
      <rect x="51.5" y="30" width="5" height="11" rx="2.5" fill="currentColor" />
      <rect x="12" y="19.5" width="40" height="33" rx="10.5" fill="currentColor" />
      <g
        stroke="var(--accent)"
        strokeWidth="2.8"
        fill="none"
        strokeLinecap="round"
        strokeLinejoin="round"
      >
        {[24, 40].map((x) => (
          <g key={x} transform={`translate(${x} 33.5)`}>
            <path d={DOLLAR} />
            <line x1="0" y1="-5.9" x2="0" y2="5.9" />
          </g>
        ))}
        <path d="M 26.5,44.5 Q 32,47.5 37.5,44.5" />
      </g>
    </svg>
  )
}
