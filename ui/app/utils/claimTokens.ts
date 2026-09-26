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

function save(tokens: string[]) {
  try {
    localStorage.setItem(KEY, JSON.stringify(tokens))
    return true
  } catch {
    return false
  }
}

export function rememberClaimToken(token?: string) {
  if (token) save([...readClaimTokens(), token].slice(-MAX))
}

export function clearClaimTokens() {
  save([])
}
