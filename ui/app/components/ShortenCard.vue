<script setup lang="ts">
import { toast } from 'vue-sonner'

const MAX_URL_LENGTH = 2048

const { showHistory = true } = defineProps<{ showHistory?: boolean }>()

const { copy, copied, isSupported: canCopy } = useClipboard({ copiedDuring: 2000, legacy: true })
const { data: user } = useCurrentUser()
const createLink = useCreateLink()
const history = useLinkHistory()
const prefix = shortLinkPrefix(useRuntimeConfig().public.apiBase)

const longUrl = ref('')
const slug = ref('')
const customizing = ref(false)
const error = ref('')
const link = ref<CreatedLink | null>(null)

const input = useTemplateRef('input')
const slugInput = useTemplateRef('slugInput')
const copyButton = useTemplateRef('copyButton')

const displayUrl = computed(() => link.value?.short_url.replace(/^https?:\/\//, '') ?? '')
const note = computed(() => error.value || (user.value ? '' : 'No account needed for basic links.'))

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

function rejectedReason(err: unknown): string {
  const reason = apiMessage(err)
  if (reason.includes('unsafe')) return 'That link looks unsafe, so we will not shorten it.'
  if (reason.includes('another URL shortener')) return 'That is already a short link from another service.'
  if (reason.includes('already a short link')) return 'That is already a shortened.cv link.'
  if (reason.includes('too long')) return 'That URL is too long to shorten.'
  return 'That does not look like a valid URL.'
}

function showInline(message: string) {
  error.value = message
  input.value?.focus()
}

function showSlugError(message: string) {
  error.value = message
  slugInput.value?.focus()
}

async function startCustomizing() {
  customizing.value = true
  await nextTick()
  slugInput.value?.focus()
}

function stopCustomizing() {
  customizing.value = false
  slug.value = ''
  error.value = ''
  input.value?.focus()
}

function shorten() {
  if (createLink.isPending.value) return

  const raw = longUrl.value.trim()
  if (!raw) return showInline('Give us something long.')
  const target = normalize(raw)
  if (!target) return showInline('That does not look like a valid URL.')
  if (target.length > MAX_URL_LENGTH) return showInline('That URL is too long to shorten.')
  const custom = customizing.value ? slug.value.trim() : ''
  const problem = custom && slugProblem(custom)
  if (problem) return showSlugError(problem)

  error.value = ''
  createLink.mutate({ targetUrl: target, slug: custom || undefined }, {
    onSuccess: async (created) => {
      link.value = created
      slug.value = ''
      customizing.value = false
      history.remember({ short_url: created.short_url, target_url: created.target_url, created_at: created.created_at })
      toast.success('That link got a haircut.')
      await nextTick()
      copyButton.value?.$el.focus()
    },
    onError: (err) => {
      const slugIssue = custom && slugRejection(err)
      if (slugIssue) return showSlugError(slugIssue)
      const status = apiStatus(err)
      if (status === 400) return showInline(rejectedReason(err))
      if (status === 429) {
        toast.error('Too many links from your network. Log in or try again later.', {
          action: { label: 'Log in', onClick: () => navigateTo('/login') }
        })
      } else {
        toast.error('The link could not be shortened. Try again.')
      }
      input.value?.focus()
    }
  })
}

async function copyLink() {
  if (!link.value) return
  const ok = await copy(link.value.short_url).then(() => copied.value, () => false)
  if (ok) toast.success('Copied. Go be useful.')
  else toast.error('Copy did not work. Select the link and copy it.')
}

async function reset() {
  link.value = null
  longUrl.value = ''
  error.value = ''
  await nextTick()
  input.value?.focus()
}

watch([longUrl, slug], () => {
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
        class="grid gap-2 sm:grid-cols-[minmax(0,1fr)_auto]"
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
          :readonly="createLink.isPending.value"
          :aria-invalid="error && !customizing ? 'true' : undefined"
          aria-describedby="shorten-note"
          class="h-12 min-w-0 rounded-lg bg-transparent px-3 text-base text-highlighted outline-none placeholder:text-dimmed sm:col-start-1 sm:row-start-1"
        >
        <div
          v-if="customizing"
          class="flex h-12 min-w-0 items-center gap-1 rounded-lg bg-muted ps-3 pe-1 sm:col-span-2 sm:row-start-2"
        >
          <label
            for="custom-slug"
            class="shrink-0 text-base text-muted"
          >{{ prefix }}</label>
          <input
            id="custom-slug"
            ref="slugInput"
            v-model="slug"
            type="text"
            autocomplete="off"
            autocapitalize="off"
            spellcheck="false"
            placeholder="your-name"
            :maxlength="SLUG_MAX_LENGTH"
            :readonly="createLink.isPending.value"
            :aria-invalid="error ? 'true' : undefined"
            aria-describedby="shorten-note"
            class="h-full min-w-0 flex-1 bg-transparent text-base text-highlighted outline-none placeholder:text-dimmed"
          >
          <UButton
            color="neutral"
            variant="ghost"
            size="sm"
            square
            icon="i-lucide-x"
            aria-label="Remove custom name"
            @click="stopCustomizing"
          />
        </div>
        <UButton
          type="submit"
          size="xl"
          :loading="createLink.isPending.value"
          :disabled="createLink.isPending.value"
          class="h-12 justify-center sm:col-start-2 sm:row-start-1 sm:w-28"
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
            class="flex h-12 min-w-0 items-center rounded-lg bg-muted px-3 text-base font-medium text-highlighted outline-none focus-visible:ring-3 focus-visible:ring-(--ds-ink)/15 sm:flex-1"
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

    <div class="mt-3 flex min-h-8 flex-wrap items-center justify-center gap-x-4 gap-y-1 px-1 text-[13px] sm:justify-between">
      <p
        v-if="!link && (note || !user || customizing)"
        id="shorten-note"
        aria-live="polite"
        :class="error ? 'text-error' : 'text-muted'"
      >
        {{ note }}
      </p>
      <UButton
        v-else-if="!link"
        variant="link"
        size="sm"
        class="px-0 text-[13px] text-toned hover:text-highlighted"
        @click="startCustomizing"
      >
        Choose a custom name
      </UButton>
      <UButton
        v-else
        variant="link"
        size="sm"
        class="px-0 text-[13px] text-toned hover:text-highlighted"
        @click="reset"
      >
        Shorten another
      </UButton>
      <div
        v-if="showHistory && history.links.value.length"
        class="sm:ms-auto"
      >
        <LinkHistoryDrawer />
      </div>
    </div>
  </div>
</template>
