import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import { toast } from 'vue-sonner'

export interface User {
  id: number
  email: string
}

const ME = ['me']

export function useCurrentUser() {
  const api = useApi()
  return useQuery({
    queryKey: ME,
    queryFn: async () => {
      try {
        const { user } = await api<{ user: User }>('/auth/me')
        return user
      } catch (err) {
        if (apiStatus(err) === 401) return null
        throw err
      }
    },
    enabled: import.meta.client,
    staleTime: 5 * 60_000
  })
}

export function useRequestMagicLink() {
  const api = useApi()
  return useMutation({
    mutationFn: (email: string) => {
      const origin = window.location.origin
      return api('/auth/magic-link/signin', {
        method: 'POST',
        body: {
          email,
          redirect_uri: `${origin}/?login=success`,
          error_redirect_uri: `${origin}/login`
        }
      })
    }
  })
}

export function useLogout() {
  const api = useApi()
  const queryClient = useQueryClient()
  return useMutation({
    // limen's CSRF check only lets JSON bodies through, so send an empty one.
    mutationFn: () => api('/auth/signout', { method: 'POST', body: {} }),
    onSuccess: () => {
      queryClient.setQueryData(ME, null)
      toast('Logged out.')
    },
    onError: () => {
      toast.error('Could not log out. Try again.')
    }
  })
}

export function useClaimAnonymousLinks() {
  const api = useApi()
  const { data: user } = useCurrentUser()
  const claim = useMutation({
    mutationFn: (tokens: string[]) => api<{ claimed: unknown[] }>('/api/v1/links/claim', {
      method: 'POST',
      body: { claim_tokens: tokens }
    }),
    onSuccess: ({ claimed }) => {
      clearClaimTokens()
      if (claimed.length === 1) toast.success('1 earlier link added to your account.')
      else if (claimed.length > 1) toast.success(`${claimed.length} earlier links added to your account.`)
    }
  })

  watch(user, (current) => {
    const tokens = current ? readClaimTokens() : []
    if (tokens.length && !claim.isPending.value) claim.mutate(tokens)
  }, { immediate: true })
}
