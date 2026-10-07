export interface ComposerKey {
  key: string
  shiftKey: boolean
  nativeEvent: { isComposing: boolean; keyCode: number }
}

/**
 * Enter sends, Shift+Enter is a new line, and an Enter that confirms an input
 * method's candidate (Japanese, Chinese, Korean) sends nothing. Safari reports
 * that Enter with `isComposing` false and keyCode 229, so both are checked.
 */
export function sendsOnEnter(event: ComposerKey): boolean {
  return (
    event.key === 'Enter' &&
    !event.shiftKey &&
    !event.nativeEvent.isComposing &&
    event.nativeEvent.keyCode !== 229
  )
}
