import { createContext, useContext } from 'react'

export const PRIVACY_STORAGE_KEY = 'privacyMode'

export interface PrivacyValue {
  /** True while dollar values must not be rendered. Payees and percentages stay. */
  hidden: boolean
  setHidden: (hidden: boolean) => void
  toggle: () => void
}

const NOT_PRIVATE: PrivacyValue = {
  hidden: false,
  setHidden: () => {},
  toggle: () => {},
}

export const PrivacyContext = createContext<PrivacyValue>(NOT_PRIVATE)

/** Defaults to visible rather than throwing when no provider is mounted. */
export function usePrivacy(): PrivacyValue {
  return useContext(PrivacyContext)
}
