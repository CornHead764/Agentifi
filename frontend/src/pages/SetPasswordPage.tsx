import { Navigate } from 'react-router-dom'

import { BrandMark } from '@/components/BrandMark'
import { ChangePasswordForm } from '@/components/ChangePasswordForm'
import { useAuth } from '@/contexts/auth'

/**
 * The first sign-in with a password somebody else chose. Not dismissable: the
 * API refuses every other endpoint while `must_change_password` is set, so it
 * renders outside the shell.
 */
export function SetPasswordPage() {
  const { user } = useAuth()

  // Arriving here without owing a change means the change already happened, or
  // somebody typed the URL. Either way the application is available.
  if (user && !user.must_change_password) return <Navigate to="/" replace />

  return (
    <div className="auth">
      <main className="auth__card">
        <div className="auth__brand">
          <span className="auth__mark" aria-hidden="true">
            <BrandMark className="auth__mark-glyph" />
          </span>
          <h1 className="auth__title">Choose a password</h1>
        </div>
        <p className="auth__lede">
          <strong>{user?.email}</strong> still has the installer&rsquo;s password. Replace it to
          continue.
        </p>
        <ChangePasswordForm submitLabel="Set password and continue" large />
      </main>
    </div>
  )
}
