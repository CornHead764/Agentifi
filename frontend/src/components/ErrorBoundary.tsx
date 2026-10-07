import { Component, type ErrorInfo, type ReactNode } from 'react'

/**
 * Where the boundary sits decides what a crash costs.
 *
 * - `app` wraps the providers. Nothing above it can draw, so its only way out
 *   is a reload.
 * - `page` wraps the routed page inside the shell, so the rail and header
 *   survive; its caller passes the path as `resetKey`, so navigating away
 *   clears the error without remounting a page that did not crash.
 * - `panel` wraps one dashboard widget or one self-contained region, so a crash
 *   costs that region and the rest of the page keeps working.
 *
 * The exception text is never rendered: it can carry field values. It goes to
 * the console.
 */
export type ErrorBoundaryScope = 'app' | 'page' | 'panel'

interface Props {
  children: ReactNode
  scope?: ErrorBoundaryScope
  /** A change to this clears a caught error. */
  resetKey?: unknown
}

interface State {
  error: Error | null
}

// A lazily loaded route whose chunk was replaced by a deploy fails this way;
// retrying in place asks for the same missing file, so only a reload helps.
function isStaleChunk(error: Error): boolean {
  return /dynamically imported module|Importing a module script failed|error loading dynamically imported/i.test(
    error.message,
  )
}

export class ErrorBoundary extends Component<Props, State> {
  override state: State = { error: null }

  static getDerivedStateFromError(error: Error): State {
    return { error }
  }

  override componentDidUpdate(previous: Props) {
    if (this.state.error !== null && previous.resetKey !== this.props.resetKey) {
      this.setState({ error: null })
    }
  }

  override componentDidCatch(error: Error, info: ErrorInfo) {
    console.error('Unhandled render failure:', error, info.componentStack)
  }

  private retry = () => {
    const { error } = this.state
    if (this.scope === 'app' || (error && isStaleChunk(error))) {
      window.location.reload()
      return
    }
    this.setState({ error: null })
  }

  private get scope(): ErrorBoundaryScope {
    return this.props.scope ?? 'app'
  }

  override render() {
    if (this.state.error === null) return this.props.children
    const scope = this.scope

    if (scope === 'panel') {
      return (
        <div role="alert" className="error-boundary error-boundary--panel">
          <p>This panel could not be shown.</p>
          <button type="button" onClick={this.retry}>
            Try again
          </button>
        </div>
      )
    }

    return (
      <div role="alert" className="error-boundary">
        <h1>Something went wrong</h1>
        <p>
          {scope === 'page'
            ? 'This page failed to load. Your data is safe on the server, and the rest of the app still works.'
            : 'The app failed to load. Your data is safe on the server.'}
        </p>
        <button type="button" onClick={this.retry}>
          {scope === 'app' ? 'Reload' : 'Try again'}
        </button>
      </div>
    )
  }
}
