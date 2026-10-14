import type { ReactNode } from 'react'

/** The last rendered dialog's submit, which a static render cannot reach by a click. */
export const dialogSubmit: { current?: () => void } = {}

/**
 * Dialog parts that render in place rather than through a portal, so a static
 * render shows what an open dialog holds. A test spreads them over the real
 * module:
 *
 *     vi.mock('@/components/ui', async (importOriginal) => ({
 *       ...(await importOriginal<typeof import('@/components/ui')>()),
 *       ...(await import('@/test/dialogStub')).dialogStub,
 *     }))
 */
export const dialogStub = {
  Dialog: ({ open, children }: { open?: boolean; children?: ReactNode }) =>
    open === false ? null : <div>{children}</div>,
  DialogActions: ({ start, children }: { start?: ReactNode; children?: ReactNode }) => (
    <>
      {start}
      {children}
    </>
  ),
  DialogClose: ({ children }: { children?: ReactNode }) => <div>{children}</div>,
  DialogContent: ({
    title,
    description,
    headerActions,
    footer,
    children,
    onSubmit,
  }: {
    title?: ReactNode
    description?: ReactNode
    headerActions?: ReactNode
    footer?: ReactNode
    children?: ReactNode
    onSubmit?: () => void
  }) => {
    dialogSubmit.current = onSubmit
    return (
      <section>
        <h2>{title}</h2>
        {headerActions}
        {description ? <p>{description}</p> : null}
        {children}
        {footer ? <footer>{footer}</footer> : null}
      </section>
    )
  },
}

/**
 * A hover card that renders its content beside its trigger, as a static render
 * cannot open one; spread over the real module as `dialogStub` is.
 */
export const hoverCardStub = {
  HoverCard: ({ content, children }: { content?: ReactNode; children?: ReactNode }) => (
    <>
      {children}
      <aside>{content}</aside>
    </>
  ),
}
