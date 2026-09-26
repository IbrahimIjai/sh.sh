<script setup lang="ts">
import { FetchError } from 'ofetch'

interface CreatedLink {
  short_url: string
  target_url: string
  claim_token?: string
}

const MAX_URL_LENGTH = 2048
const CLAIM_TOKENS_KEY = 'shortened:claim-tokens'
const MAX_CLAIM_TOKENS = 100

const { apiBase } = useRuntimeConfig().public
const { copy, copied, isSupported: canCopy } = useClipboard({ copiedDuring: 2000, legacy: true })

const longUrl = ref('')
const pending = ref(false)
const error = ref('')
const link = ref<CreatedLink | null>(null)
const copyFailed = ref(false)
const everCopied = ref(false)

const input = useTemplateRef('input')
const copyButton = useTemplateRef('copyButton')

const displayUrl = computed(() => link.value?.short_url.replace(/^https?:\/\//, '') ?? '')

const note = computed(() => {
  if (error.value) return error.value
  if (!link.value) return 'No account needed for basic links.'
  if (copyFailed.value) return 'Copy did not work. Select the link and copy it.'
  if (everCopied.value) return 'Copied. Go be useful.'
  return 'That link got a haircut.'
})

function normalize(raw: string): string | null {
  const value = /^[a-z][a-z\d+.-]*:\/\//i.test(raw) ? raw : `https://${raw}`
  if (/\s/.test(value)) return null
  try {
    const url = new URL(value)
    if (url.protocol !== 'http:' && url.protocol !== 'https:') return null
    if (!url.hostname.includes('.') || url.hostname.endsWith('.')) return null
    return value
  } catch {
    return null
  }
}

function errorMessage(err: unknown): string {
  if (!(err instanceof FetchError) || !err.status || err.status >= 500) {
    return 'The link could not be shortened. Try again.'
  }
  if (err.status === 429) return 'Too many links from your network. Try again in a little while.'

  const reason = String(err.data?.error ?? '')
  if (reason.includes('unsafe')) return 'That link looks unsafe, so we will not shorten it.'
  if (reason.includes('another URL shortener')) return 'That is already a short link from another service.'
  if (reason.includes('already a short link')) return 'That is already a shortened.cv link.'
  if (reason.includes('too long')) return 'That URL is too long to shorten.'
  return 'That does not look like a valid URL.'
}

function rememberClaimToken(token?: string) {
  if (!token) return
  try {
    const saved: unknown = JSON.parse(localStorage.getItem(CLAIM_TOKENS_KEY) ?? '[]')
    const tokens = Array.isArray(saved) ? saved.filter(t => typeof t === 'string') : []
    localStorage.setItem(CLAIM_TOKENS_KEY, JSON.stringify([...tokens, token].slice(-MAX_CLAIM_TOKENS)))
  } catch {
    // Storage can be full or blocked; the link itself is already created.
  }
}

async function shorten() {
  if (pending.value) return

  const raw = longUrl.value.trim()
  if (!raw) {
    error.value = 'Give us something long.'
    input.value?.focus()
    return
  }
  const target = normalize(raw)
  if (!target) {
    error.value = 'That does not look like a valid URL.'
    input.value?.focus()
    return
  }
  if (target.length > MAX_URL_LENGTH) {
    error.value = 'That URL is too long to shorten.'
    input.value?.focus()
    return
  }

  error.value = ''
  pending.value = true
  try {
    const created = await $fetch<CreatedLink>('/api/v1/links', {
      baseURL: apiBase,
      method: 'POST',
      body: { target_url: target },
      credentials: 'include'
    })
    rememberClaimToken(created.claim_token)
    link.value = created
    await nextTick()
    copyButton.value?.$el.focus()
  } catch (err) {
    error.value = errorMessage(err)
    input.value?.focus()
  } finally {
    pending.value = false
  }
}

async function copyLink() {
  if (!link.value) return
  copyFailed.value = false
  try {
    await copy(link.value.short_url)
    everCopied.value = copied.value
    copyFailed.value = !copied.value
  } catch {
    copyFailed.value = true
  }
}

async function reset() {
  link.value = null
  longUrl.value = ''
  error.value = ''
  copyFailed.value = false
  everCopied.value = false
  await nextTick()
  input.value?.focus()
}

watch(longUrl, () => {
  error.value = ''
})
</script>

<template>
  <div>
    <div
      class="rounded-xl border border-default bg-default p-2 text-left shadow-(--ds-shadow-xs) transition-[border-color,box-shadow] duration-150"
      :class="link ? '' : 'has-[input:focus-visible]:border-accented has-[input:focus-visible]:ring-3 has-[input:focus-visible]:ring-(--ds-ink)/8'"
    >
      <form
        v-if="!link"
        class="flex flex-col gap-2 sm:flex-row"
        novalidate
        @submit.prevent="shorten"
      >
        <label
          for="long-url"
          class="sr-only"
        >Long URL</label>
        <input
          id="long-url"
          ref="input"
          v-model="longUrl"
          type="url"
          inputmode="url"
          autocomplete="off"
          autocapitalize="off"
          spellcheck="false"
          placeholder="Paste a long URL"
          :readonly="pending"
          :aria-invalid="error ? 'true' : undefined"
          aria-describedby="shorten-note"
          class="h-12 min-w-0 flex-1 rounded-lg bg-transparent px-3 text-base text-highlighted outline-none placeholder:text-dimmed"
        >
        <UButton
          type="submit"
          size="xl"
          :loading="pending"
          :disabled="pending"
          class="h-12 justify-center sm:w-28"
        >
          Shorten
        </UButton>
      </form>

      <div v-else>
        <h2 class="px-3 pt-1.5 pb-2 text-[13px] font-medium text-toned">
          Your short link is ready
        </h2>
        <div class="flex flex-col gap-2 sm:flex-row">
          <a
            :href="link.short_url"
            target="_blank"
            rel="noopener"
            class="flex h-12 min-w-0 flex-1 items-center rounded-lg bg-muted px-3 text-base font-medium text-highlighted outline-none focus-visible:ring-3 focus-visible:ring-(--ds-ink)/15"
          >
            <span class="truncate">{{ displayUrl }}</span>
          </a>
          <UButton
            ref="copyButton"
            size="xl"
            :icon="copied ? 'i-lucide-check' : 'i-lucide-copy'"
            :disabled="!canCopy"
            class="h-12 justify-center sm:w-28"
            @click="copyLink"
          >
            {{ copied ? 'Copied' : 'Copy' }}
          </UButton>
        </div>
        <p
          class="truncate px-3 pt-2 pb-1 text-[13px] text-dimmed"
          :title="link.target_url"
        >
          {{ link.target_url }}
        </p>
      </div>
    </div>

    <div class="mt-3 flex min-h-8 flex-wrap items-center justify-center gap-x-3 gap-y-1 px-1 text-[13px] sm:justify-between">
      <p
        id="shorten-note"
        aria-live="polite"
        :class="error ? 'text-error' : 'text-muted'"
      >
        {{ note }}
      </p>
      <UButton
        v-if="link"
        variant="link"
        size="sm"
        class="px-0 text-[13px] text-toned hover:text-highlighted"
        @click="reset"
      >
        Shorten another
      </UButton>
    </div>
  </div>
</template>
