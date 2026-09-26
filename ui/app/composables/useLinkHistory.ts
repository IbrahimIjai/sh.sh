import { useQuery } from '@tanstack/vue-query'

export interface SavedLink {
  short_url: string
  target_url: string
  created_at: string
}

export const ACCOUNT_LINKS = ['account-links']

const KEY = 'shortened:links'
const MAX_SAVED = 50

export function useLinkHistory() {
  const api = useApi()
  const me = useCurrentUser()
  const local = useLocalStorage<SavedLink[]>(KEY, [], { initOnMounted: true })

  const isAccount = computed(() => Boolean(me.data.value))
  const account = useQuery({
    queryKey: ACCOUNT_LINKS,
    queryFn: () => api<SavedLink[]>('/api/v1/links'),
    enabled: isAccount
  })

  const links = computed<SavedLink[]>(() => {
    if (isAccount.value) return account.data.value ?? []
    const loggedOut = me.data.value === null || me.isError.value
    return loggedOut ? local.value : []
  })

  function remember(link: SavedLink) {
    if (isAccount.value) return
    const others = local.value.filter(saved => saved.short_url !== link.short_url)
    local.value = [link, ...others].slice(0, MAX_SAVED)
  }

  function clearLocal() {
    local.value = []
  }

  return { links, isAccount, remember, clearLocal }
}
