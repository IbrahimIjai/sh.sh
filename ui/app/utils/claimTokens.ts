const KEY = 'shortened:claim-tokens'
const MAX = 100

export function readClaimTokens(): string[] {
  try {
    const saved: unknown = JSON.parse(localStorage.getItem(KEY) ?? '[]')
    return Array.isArray(saved) ? saved.filter(t => typeof t === 'string') : []
  } catch {
    return []
  }
}

export function rememberClaimToken(token?: string) {
  if (!token) return
  try {
    localStorage.setItem(KEY, JSON.stringify([...readClaimTokens(), token].slice(-MAX)))
  } catch {
    // Storage can be full or blocked; the link itself is already created.
  }
}

export function clearClaimTokens() {
  try {
    localStorage.removeItem(KEY)
  } catch {
    // Nothing to clear when storage is blocked.
  }
}
