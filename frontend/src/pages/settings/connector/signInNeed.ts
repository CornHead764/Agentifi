/**
 * What a login that needs a sign-in is waiting on. A lapsed session with a
 * kept password signs itself in on the next update. A refused password or an
 * unanswered code pauses updates until somebody signs in, because retrying on
 * a timer would lock the account or send a code every night. With no password
 * kept, a sign-in is the only way back. Null when no sign-in is needed.
 */
export type SignInNeed = 'password-signs-in' | 'password-refused' | 'code-needed' | 'sign-in' | null

/** What a bill connection and a shop account both say about signing in. */
export interface SignInState {
  /** The row's flag, or its last run having ended at a sign-in. */
  needsSignIn: boolean
  /** Why unattended sign-ins stopped: `password_refused`, `code_needed` or empty. */
  paused: string
  hasPassword: boolean
}

/**
 * A pause outranks the flag: a run only lifts a pause by succeeding, so a
 * paused row whose last run failed some other way has the flag cleared and is
 * still skipped by the scheduler.
 */
export function signInNeed({ needsSignIn, paused, hasPassword }: SignInState): SignInNeed {
  if (paused === 'code_needed') return 'code-needed'
  if (paused === 'password_refused') return 'password-refused'
  if (!needsSignIn) return null
  return hasPassword ? 'password-signs-in' : 'sign-in'
}

/** A paused login: one only a person can start updating again. */
export function signInPaused(need: SignInNeed): boolean {
  return need === 'password-refused' || need === 'code-needed'
}

/** A login that has been signed in to, or was stopped at one, signs in again. */
export function signInLabel(connected: boolean, need: SignInNeed): string {
  return connected || signInPaused(need) ? 'Sign in again' : 'Sign in'
}

/** The authenticator key is sealed with the password, so it goes with it. */
export function forgetPasswordLabel(hasKey: boolean): string {
  return hasKey ? 'Forget password and key' : 'Forget password'
}
