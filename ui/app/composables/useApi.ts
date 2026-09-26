import { FetchError } from 'ofetch'

export function useApi() {
  const { apiBase } = useRuntimeConfig().public
  return $fetch.create({ baseURL: apiBase, credentials: 'include' })
}

export function apiStatus(err: unknown): number | undefined {
  return err instanceof FetchError ? err.status : undefined
}

export function apiMessage(err: unknown): string {
  if (!(err instanceof FetchError)) return ''
  return String(err.data?.error ?? err.data?.message ?? '')
}
