import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'

import { Button } from './Button'
import { Dialog, DialogActions, DialogContent } from './Dialog'
import { submitOwnForm } from './dialog-submit'

/**
 * `DialogContent`'s `onSubmit` wraps the body and footer in a `<form>`. Radix
 * portals the content outside this harness, so the `<form>` cannot be asserted
 * here; this guards against a crash from the branch, which changes the element
 * tree of every dialog in the app.
 */
function renderDialog(onSubmit?: () => void): string {
  return renderToStaticMarkup(
    <Dialog open onOpenChange={() => {}}>
      <DialogContent title="Test" onSubmit={onSubmit} footer={<button type="submit">Save</button>}>
        <input />
      </DialogContent>
    </Dialog>,
  )
}

describe('DialogContent', () => {
  it('renders without crashing when onSubmit wraps the body and footer in a form', () => {
    expect(() => renderDialog(() => {})).not.toThrow()
  })

  it('renders without crashing when onSubmit is omitted', () => {
    expect(() => renderDialog(undefined)).not.toThrow()
  })
})

describe('<DialogActions>', () => {
  it('puts the aside first, then a secondary Cancel, then the answer', () => {
    const markup = renderToStaticMarkup(
      <DialogActions onCancel={() => {}} start={<Button variant="danger">Delete</Button>}>
        <Button variant="primary">Save</Button>
      </DialogActions>,
    )
    expect(markup).toBe(
      '<button type="button" class="btn btn--danger">Delete</button>' +
        '<span class="dialog__footer-gap"></span>' +
        '<button type="button" class="btn btn--secondary">Cancel</button>' +
        '<button type="button" class="btn btn--primary">Save</button>',
    )
  })

  it('names the dismissal and can leave it out', () => {
    expect(renderToStaticMarkup(<DialogActions cancel="Close" onCancel={() => {}} />)).toBe(
      '<button type="button" class="btn btn--secondary">Close</button>',
    )
    expect(renderToStaticMarkup(<DialogActions cancel={false} />)).toBe('')
  })
})

describe('submitOwnForm', () => {
  it('keeps a nested dialog’s submit from reaching the form that opened it', () => {
    const calls: string[] = []
    const event = {
      preventDefault: () => calls.push('prevent'),
      stopPropagation: () => calls.push('stop'),
    }
    submitOwnForm(event, () => calls.push('submit'))
    expect(calls).toEqual(['prevent', 'stop', 'submit'])
  })
})
