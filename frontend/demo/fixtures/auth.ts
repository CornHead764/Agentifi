export const USER_ID = '00000000-0000-4000-8000-000000000001'

export const AUTH = {
  'GET /auth/first-account': { open: false },
  'GET /auth/me': {
    id: USER_ID,
    email: 'sam@example.com',
    full_name: 'Sam Sample',
    is_active: true,
    is_superuser: true,
    is_verified: true,
    locale: 'en-US',
    theme: 'light',
    privacy_mode: false,
    swipe_left_action: 'none',
    swipe_right_action: 'none',
    animation_duration_ms: 0,
    last_login_at: '2026-06-14T09:00:00Z',
    must_change_password: false,
    has_password: true,
    has_totp: false,
    has_oidc: false,
  },
}
