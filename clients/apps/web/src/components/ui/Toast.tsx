import { clsx } from 'clsx'
import { X } from 'lucide-react'
import { Toast as Radix } from 'radix-ui'
import {
  useCallback,
  useContext,
  useEffect,
  useEffectEvent,
  useMemo,
  useRef,
  useState,
  type CSSProperties,
  type ReactNode,
} from 'react'
import { createPortal } from 'react-dom'

import { MotionContext } from '@/contexts/motion'
import { useRoamingPreferences } from '@/lib/preferences'
import { DEFAULT_TOAST_MS } from '@/lib/toastDuration'

import { Spinner } from './Spinner'
import {
  replaceToast,
  toastDuration,
  ToastContext,
  type QueuedToast,
  type ToastId,
  type ToastOptions,
} from './toast-context'
import { holdOnHover, startCountdown } from './toast-countdown'

interface DrainStyle extends CSSProperties {
  '--toast-ms': string
}

/** A toast on screen, or fading off it once dismissed. */
interface Shown extends QueuedToast {
  closing?: boolean
}

export function ToastProvider({ children }: { children: ReactNode }) {
  const [toasts, setToasts] = useState<Shown[]>([])
  const nextId = useRef(0)
  const preferredMs = useRoamingPreferences()?.toastMs ?? DEFAULT_TOAST_MS
  const exitMs = useContext(MotionContext)?.duration ?? 0
  const animates = exitMs > 0
  // Portalled to the body: `#root` isolates itself, so a viewport inside it
  // stacks under the dialogs Radix portals there. Null where there is no
  // document, in the tests.
  const outside = typeof document === 'undefined' ? null : document.body

  const show = useCallback((toast: ToastOptions) => {
    nextId.current += 1
    const id = nextId.current
    setToasts((current) => [...current, { ...toast, id, version: 0 }])
    return id
  }, [])

  const update = useCallback((id: ToastId, toast: ToastOptions) => {
    setToasts((current) => replaceToast(current, id, toast))
  }, [])

  // A closed toast fades out and is dropped on a timer rather than at the end
  // of the fade, which a hidden tab never runs. An update that arrives
  // meanwhile shows it again, and the timer leaves it be.
  const dismiss = useCallback(
    (id: ToastId) => {
      setToasts((current) =>
        current.map((toast) => (toast.id === id ? { ...toast, closing: true } : toast)),
      )
      setTimeout(() => {
        setToasts((current) => current.filter((toast) => !(toast.id === id && toast.closing)))
      }, exitMs)
    },
    [exitMs],
  )

  const value = useMemo(() => ({ show, update, dismiss }), [show, update, dismiss])

  const viewport = <Radix.Viewport className="toast-viewport" />

  return (
    <ToastContext value={value}>
      <Radix.Provider swipeDirection="right">
        {children}
        {toasts.map((toast) => (
          <ToastItem
            key={`${toast.id}:${toast.version}`}
            toast={toast}
            duration={toastDuration(toast, preferredMs)}
            animates={animates}
            onClose={() => dismiss(toast.id)}
          />
        ))}
        {outside ? createPortal(viewport, outside) : viewport}
      </Radix.Provider>
    </ToastContext>
  )
}

/**
 * One toast. Radix is given no duration: its timer stops for the whole
 * viewport on a window blur and starts none for a toast that arrives while
 * stopped, so a toast shown in a background tab would stay. The countdown is the
 * toast's own, and the bar is redrawn from it each time it stops or starts,
 * so the two cannot drift apart.
 */
function ToastItem({
  toast,
  duration,
  animates,
  onClose,
}: {
  toast: Shown
  duration: number
  animates: boolean
  onClose: () => void
}) {
  const ref = useRef<HTMLLIElement>(null)
  const [bar, setBar] = useState({ segment: 0, elapsed: 0, running: true })
  const close = useEffectEvent(onClose)
  const timed = Number.isFinite(duration) && !toast.closing

  useEffect(() => {
    const element = ref.current
    if (!timed || !element) return
    const countdown = startCountdown(duration, () => close())
    const stop = holdOnHover(countdown, element, () =>
      setBar((last) => ({
        segment: last.segment + 1,
        elapsed: countdown.elapsed(),
        running: countdown.running(),
      })),
    )
    return () => {
      stop()
      countdown.cancel()
    }
  }, [timed, duration])

  const drain: DrainStyle = {
    '--toast-ms': `${duration}ms`,
    animationDelay: `-${bar.elapsed}ms`,
  }

  return (
    <Radix.Root
      ref={ref}
      className={clsx('toast', toast.tone && toast.tone !== 'neutral' && `toast--${toast.tone}`)}
      open={!toast.closing}
      duration={Infinity}
      data-paused={bar.running ? undefined : true}
      onOpenChange={(open) => {
        if (!open) onClose()
      }}
    >
      <Radix.Title className="toast__title">
        {toast.tone === 'loading' ? (
          <Spinner />
        ) : null}
        {toast.title}
      </Radix.Title>
      {toast.description ? (
        <Radix.Description className="toast__description">
          {toast.description}
        </Radix.Description>
      ) : null}
      {toast.action ? (
        <Radix.Action
          className="toast__action"
          altText={toast.action.label}
          onClick={toast.action.onSelect}
        >
          {toast.action.label}
        </Radix.Action>
      ) : null}
      <Radix.Close className="toast__close" aria-label="Close">
        <X size={14} />
      </Radix.Close>
      {animates && Number.isFinite(duration) ? (
        <span
          key={bar.segment}
          className="toast__progress"
          style={drain}
          aria-hidden
        />
      ) : null}
    </Radix.Root>
  )
}
