import { initial } from '@/lib/format'

/**
 * The two letters on the header avatar: first and last word of the name, one
 * letter for a one-word name, else the address. Null rather than "" when there
 * is nothing to draw, so the avatar falls back to a generic mark.
 */
export function initialsFor(fullName: string | null, email: string): string | null {
  const words = (fullName ?? '').trim().split(/\s+/).filter(Boolean)
  if (words.length === 1) return letter(words[0])
  if (words.length > 1) {
    const first = letter(words[0])
    const last = letter(words[words.length - 1])
    if (first && last) return first + last
    return first ?? last
  }
  return letter(email)
}

function letter(word: string): string | null {
  return initial(word) || null
}
