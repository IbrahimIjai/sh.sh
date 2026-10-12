import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import type { MaybeRefOrGetter } from 'vue'

export interface AccountLink {
  id: number
  slug: string
  short_url: string
  target_url: string
  title: string | null
  is_active: boolean
  click_count: number
  created_at: string
}

export interface StatsEntry {
  name: string
  clicks: number
}

export interface LinkStats {
  link_id: number
  since: string
  days: number
  clicks: number
  unique_visitors: number
  bot_clicks: number
  daily: { day: string, clicks: number, unique_visitors: number }[]
  breakdown: Record<string, StatsEntry[]>
}

export function useAccountLinks() {
  const api = useApi()
  const { data: user } = useCurrentUser()
  return useQuery({
    queryKey: ACCOUNT_LINKS,
    queryFn: () => api<AccountLink[]>('/api/v1/links'),
    enabled: computed(() => Boolean(user.value))
  })
}

export function useLinkStats(id: MaybeRefOrGetter<number>, days: MaybeRefOrGetter<number>) {
  const api = useApi()
  const { data: user } = useCurrentUser()
  return useQuery({
    queryKey: computed(() => ['link-stats', toValue(id), toValue(days)]),
    queryFn: () => api<LinkStats>(`/api/v1/links/${toValue(id)}/stats`, { query: { days: toValue(days) } }),
    enabled: computed(() => Boolean(user.value) && Number.isInteger(toValue(id))),
    placeholderData: previous => previous
  })
}

export function useDeleteLink() {
  const api = useApi()
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (id: number) => api(`/api/v1/links/${id}`, { method: 'DELETE' }),
    onSuccess: (_, id) => {
      queryClient.setQueryData<AccountLink[]>(ACCOUNT_LINKS, links => links?.filter(link => link.id !== id))
      queryClient.removeQueries({ queryKey: ['link-stats', id] })
    }
  })
}
