<script setup lang="ts">
import type { DropdownMenuItem } from '@nuxt/ui'
import { toast } from 'vue-sonner'

useSeoMeta({
  title: `Dashboard · ${site.name}`,
  robots: 'noindex, nofollow'
})

const me = useCurrentUser()
const links = useAccountLinks()
const deleteLink = useDeleteLink()
const { copy, copied, text: copiedText } = useClipboard({ copiedDuring: 2000, legacy: true })

const search = ref('')
const pendingDelete = ref<AccountLink | null>(null)

watch(() => me.data.value, (user) => {
  if (user === null) navigateTo('/login')
}, { immediate: true })

const all = computed(() => links.data.value ?? [])
const totalClicks = computed(() => all.value.reduce((sum, link) => sum + link.click_count, 0))
const filtered = computed(() => {
  const term = search.value.trim().toLowerCase()
  if (!term) return all.value
  return all.value.filter(link =>
    link.slug.toLowerCase().includes(term) || link.target_url.toLowerCase().includes(term)
  )
})
const isLoading = computed(() => me.isPending.value || links.isPending.value)

function isCopied(url: string) {
  return copied.value && copiedText.value === url
}

async function copyLink(url: string) {
  const ok = await copy(url).then(() => isCopied(url), () => false)
  if (ok) toast.success('Copied.')
  else toast.error('Copy did not work. Select the link and copy it.')
}

function actions(link: AccountLink): DropdownMenuItem[][] {
  return [
    [
      { label: 'View stats', icon: 'i-lucide-chart-column', to: `/dashboard/${link.id}` },
      { label: 'Open link', icon: 'i-lucide-external-link', href: link.short_url, target: '_blank' }
    ],
    [
      { label: 'Delete', icon: 'i-lucide-trash-2', color: 'error', onSelect: () => {
        pendingDelete.value = link
      } }
    ]
  ]
}

const deleteOpen = computed({
  get: () => pendingDelete.value !== null,
  set: (open) => {
    if (!open) pendingDelete.value = null
  }
})

function confirmDelete() {
  const link = pendingDelete.value
  if (!link) return
  deleteLink.mutate(link.id, {
    onSuccess: () => {
      pendingDelete.value = null
      toast.success('Link deleted.')
    },
    onError: () => toast.error('The link could not be deleted. Try again.')
  })
}
</script>

<template>
  <section class="mx-auto w-full max-w-3xl px-5 pt-8 pb-16 sm:px-8 sm:pt-12">
    <div class="flex flex-wrap items-end justify-between gap-x-6 gap-y-1">
      <h1 class="text-3xl font-semibold tracking-tight text-highlighted">
        Links
      </h1>
      <p
        v-if="links.data.value"
        class="text-[13px] text-muted"
      >
        {{ formatNumber(all.length) }} {{ all.length === 1 ? 'link' : 'links' }} ·
        {{ formatNumber(totalClicks) }} {{ totalClicks === 1 ? 'click' : 'clicks' }}
      </p>
    </div>

    <ShortenCard
      class="mt-6"
      :show-history="false"
    />

    <div class="mt-10">
      <UInput
        v-if="all.length > 5"
        v-model="search"
        icon="i-lucide-search"
        placeholder="Search links"
        size="lg"
        class="mb-3 w-full"
        :ui="{ base: 'bg-default' }"
        aria-label="Search links"
      />

      <div
        v-if="isLoading"
        class="divide-y divide-default overflow-hidden rounded-xl border border-default bg-default"
      >
        <div
          v-for="n in 4"
          :key="n"
          class="flex items-center gap-4 px-4 py-4"
        >
          <div class="flex-1 space-y-2">
            <USkeleton class="h-4 w-40" />
            <USkeleton class="h-3 w-64 max-w-full" />
          </div>
          <USkeleton class="h-4 w-10" />
        </div>
      </div>

      <div
        v-else-if="links.isError.value"
        class="rounded-xl border border-default bg-default px-4 py-10 text-center"
      >
        <p class="text-sm text-toned">
          Your links could not be loaded.
        </p>
        <UButton
          variant="outline"
          size="sm"
          class="mt-3"
          @click="links.refetch()"
        >
          Try again
        </UButton>
      </div>

      <div
        v-else-if="!all.length"
        class="rounded-xl border border-dashed border-default px-4 py-12 text-center"
      >
        <UIcon
          name="i-lucide-link-2"
          class="size-6 text-dimmed"
        />
        <p class="mt-3 text-sm font-medium text-highlighted">
          No links yet
        </p>
        <p class="mt-1 text-[13px] text-muted">
          Shorten a URL above and it will show up here.
        </p>
      </div>

      <p
        v-else-if="!filtered.length"
        class="rounded-xl border border-default bg-default px-4 py-10 text-center text-sm text-muted"
      >
        No links match “{{ search.trim() }}”.
      </p>

      <ul
        v-else
        class="divide-y divide-default overflow-hidden rounded-xl border border-default bg-default"
      >
        <li
          v-for="link in filtered"
          :key="link.id"
          class="group relative flex items-center gap-3 px-4 py-3.5 transition-colors hover:bg-muted"
        >
          <div class="min-w-0 flex-1">
            <NuxtLink
              :to="`/dashboard/${link.id}`"
              class="block truncate rounded-sm text-sm font-medium text-highlighted outline-none after:absolute after:inset-0 focus-visible:ring-3 focus-visible:ring-(--ds-ink)/15"
            >
              {{ displayUrl(link.short_url) }}
            </NuxtLink>
            <p
              class="mt-0.5 truncate text-[13px] text-dimmed"
              :title="link.target_url"
            >
              {{ displayUrl(link.target_url) }}
            </p>
            <p class="mt-1 text-xs text-muted sm:hidden">
              {{ formatNumber(link.click_count) }} {{ link.click_count === 1 ? 'click' : 'clicks' }} · {{ formatDate(link.created_at) }}
            </p>
          </div>

          <div class="hidden w-20 shrink-0 text-right sm:block">
            <p class="text-sm font-medium text-highlighted tabular-nums">
              {{ formatNumber(link.click_count) }}
            </p>
            <p class="text-xs text-muted">
              {{ link.click_count === 1 ? 'click' : 'clicks' }}
            </p>
          </div>
          <p class="hidden w-24 shrink-0 text-right text-xs text-muted md:block">
            {{ formatDate(link.created_at) }}
          </p>

          <div class="relative z-10 flex shrink-0 items-center gap-1">
            <UButton
              color="neutral"
              variant="ghost"
              size="sm"
              square
              :icon="isCopied(link.short_url) ? 'i-lucide-check' : 'i-lucide-copy'"
              :aria-label="`Copy ${displayUrl(link.short_url)}`"
              @click="copyLink(link.short_url)"
            />
            <UDropdownMenu
              :items="actions(link)"
              :content="{ align: 'end' }"
            >
              <UButton
                color="neutral"
                variant="ghost"
                size="sm"
                square
                icon="i-lucide-ellipsis"
                :aria-label="`More actions for ${displayUrl(link.short_url)}`"
              />
            </UDropdownMenu>
          </div>
        </li>
      </ul>
    </div>

    <UModal
      v-model:open="deleteOpen"
      title="Delete this link?"
      :description="pendingDelete ? `${displayUrl(pendingDelete.short_url)} will stop redirecting. This cannot be undone.` : ''"
    >
      <template #footer>
        <div class="flex w-full flex-col-reverse gap-2 sm:flex-row sm:justify-end">
          <UButton
            variant="outline"
            class="justify-center"
            @click="deleteOpen = false"
          >
            Cancel
          </UButton>
          <UButton
            color="error"
            class="justify-center"
            :loading="deleteLink.isPending.value"
            @click="confirmDelete"
          >
            Delete link
          </UButton>
        </div>
      </template>
    </UModal>
  </section>
</template>
