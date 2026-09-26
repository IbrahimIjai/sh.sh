import { useMutation } from '@tanstack/vue-query'

export interface CreatedLink {
  short_url: string
  target_url: string
  claim_token?: string
}

export function useCreateLink() {
  const api = useApi()
  return useMutation({
    mutationFn: (targetUrl: string) => api<CreatedLink>('/api/v1/links', {
      method: 'POST',
      body: { target_url: targetUrl }
    }),
    onSuccess: link => rememberClaimToken(link.claim_token)
  })
}
