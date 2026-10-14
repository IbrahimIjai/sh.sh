import { useMutation, useQueryClient } from '@tanstack/vue-query'

export interface CreatedLink {
  short_url: string
  target_url: string
  created_at: string
  claim_token?: string
}

export function useCreateLink() {
  const api = useApi()
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: ({ targetUrl, slug }: { targetUrl: string, slug?: string }) => api<CreatedLink>('/api/v1/links', {
      method: 'POST',
      body: slug ? { target_url: targetUrl, slug } : { target_url: targetUrl }
    }),
    onSuccess: (link) => {
      rememberClaimToken(link.claim_token)
      queryClient.invalidateQueries({ queryKey: ACCOUNT_LINKS })
    }
  })
}
