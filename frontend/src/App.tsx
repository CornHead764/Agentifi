import { useEffect } from 'react'
import { BrowserRouter } from 'react-router-dom'

import { ErrorBoundary } from '@/components/ErrorBoundary'
import { MutationFailureToasts } from '@/components/MutationFailureToasts'
import { PreferenceRoaming } from '@/components/PreferenceRoaming'
import { ToastProvider } from '@/components/ui'
import { AuthProvider } from '@/contexts/AuthProvider'
import { MotionProvider } from '@/contexts/MotionProvider'
import { PrivacyProvider } from '@/contexts/PrivacyProvider'
import { ThemeProvider } from '@/contexts/ThemeProvider'
import { watchForEmptyCarets } from '@/lib/dropEmptyCaret'
import { AppRoutes } from '@/routes'

/**
 * Theme, privacy and motion sit outside the router because they write to the
 * root element; authentication is innermost so signing out keeps the theme.
 */
export default function App() {
  // On the document: Radix portals dialogs and popovers outside the app's tree.
  useEffect(watchForEmptyCarets, [])

  return (
    <ErrorBoundary>
      <ThemeProvider>
        <PrivacyProvider>
          <MotionProvider>
            <ToastProvider>
              <MutationFailureToasts />
              <BrowserRouter>
                {/* Inside the router: the guards it provides redirect, which
                    needs a router above them. */}
                <AuthProvider>
                  {/* Hands the account's stored preferences to the providers
                      above, which cannot read the session themselves. */}
                  <PreferenceRoaming />
                  <AppRoutes />
                </AuthProvider>
              </BrowserRouter>
            </ToastProvider>
          </MotionProvider>
        </PrivacyProvider>
      </ThemeProvider>
    </ErrorBoundary>
  )
}
