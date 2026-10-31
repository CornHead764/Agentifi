/**
 * The audit line's opening words. The verb comes from whether the tool writes,
 * not its name, so `create_tag` is never introduced as read; `read_endpoint`
 * already leads with the verb, so it is not prefixed twice.
 */
export function toolLine(verb: string, toolName: string): string {
  const spaced = toolName.replace(/_/g, ' ')
  return spaced.startsWith(`${verb} `) ? spaced : `${verb} ${spaced}`
}
