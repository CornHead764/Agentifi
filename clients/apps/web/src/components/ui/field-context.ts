import { createContext, useContext } from 'react'

/**
 * What a `<Field>` hands the control inside it. The wiring is generated once
 * by the field, so a label cannot point at an id that no longer exists.
 */
export interface FieldControlProps {
  id?: string
  'aria-describedby'?: string
  'aria-invalid'?: boolean
}

export const FieldContext = createContext<FieldControlProps>({})

export function useFieldControl(): FieldControlProps {
  return useContext(FieldContext)
}
