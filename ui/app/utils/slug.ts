import { apiMessage, apiStatus } from '../composables/useApi'
import { displayUrl } from './format'

export const SLUG_MAX_LENGTH = 128

const SLUG_PATTERN = /^[A-Za-z0-9_-]+$/

export function slugProblem(slug: string): string {
  if (slug.length < 3) return 'Use at least 3 characters.'
  if (slug.length > SLUG_MAX_LENGTH) return `Keep it to ${SLUG_MAX_LENGTH} characters.`
  if (!SLUG_PATTERN.test(slug)) return 'Use letters, numbers, - or _ only.'
  return ''
}

export function slugRejection(err: unknown): string {
  const status = apiStatus(err)
  if (status === 409) return 'That name is taken. Try another.'
  if (status === 400 && apiMessage(err).startsWith('slug')) return 'That name is reserved. Try another.'
  return ''
}

export function shortLinkPrefix(base: string) {
  return `${displayUrl(base)}/`
}
