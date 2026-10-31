import { clsx } from 'clsx'
import { Paperclip } from 'lucide-react'
import { useEffect, useRef, type ComponentProps, type ReactNode } from 'react'

import { useFieldControl } from './field-context'

export interface FileInputProps extends Omit<ComponentProps<'input'>, 'type' | 'value' | 'placeholder'> {
  /** What the button says. "Choose file" unless the form means something else. */
  action?: string
  /** The button's glyph; a paperclip unless the source is something else, a camera say. */
  icon?: ReactNode
  /**
   * What stands in for a filename before anything is picked. Null draws the
   * button alone, for a caller that takes the file the moment it is picked.
   */
  placeholder?: string | null
    /**
     * The chosen file's name, held by the caller. The control watches it for
     * null and empties the native input, so a reset form can offer the same
     * file again and still be told about it.
     */
  fileName?: string | null
}

/**
 * A file picker that matches the text inputs. The browser's control cannot be
 * styled, so the real input is taken out of the layout and keeps the field's
 * id, so the `<Field>` label still opens the picker.
 */
export function FileInput({
  action = 'Choose file',
  icon = <Paperclip size={13} aria-hidden="true" />,
  placeholder = 'No file chosen',
  fileName,
  className,
  disabled,
  ...props
}: FileInputProps) {
  const field = useFieldControl()
  const input = useRef<HTMLInputElement>(null)

  // Re-picking the file that was just cleared fires no change event unless the
  // element's own value is cleared with it.
  useEffect(() => {
    if (!fileName && input.current) input.current.value = ''
  }, [fileName])

  return (
    <span
      className={clsx(
        'file-input',
        placeholder === null && 'file-input--action',
        disabled && 'file-input--disabled',
        className,
      )}
    >
      <button
        type="button"
        className="file-input__action"
        disabled={disabled}
        onClick={() => input.current?.click()}
        // The label already names this control; the button is a second way to
        // reach the same picker, not a second thing to announce.
        tabIndex={-1}
        aria-hidden="true"
      >
        {icon}
        {action}
      </button>
      {placeholder === null ? null : (
        <span className={clsx('file-input__name', !fileName && 'file-input__name--empty')}>
          {fileName || placeholder}
        </span>
      )}
      {/* Focusable but out of the layout: a file input is drawn by the browser
          and cannot be styled to match. */}
      <input
        ref={input}
        type="file"
        className="visually-hidden"
        disabled={disabled}
        {...field}
        {...props}
      />
    </span>
  )
}
