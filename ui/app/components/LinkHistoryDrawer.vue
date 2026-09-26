<script setup lang="ts">
import { toast } from 'vue-sonner'

const { links, isAccount } = useLinkHistory()
const isDesktop = useMediaQuery('(min-width: 640px)')
const { copy, copied, text: copiedText } = useClipboard({ copiedDuring: 2000, legacy: true })

function display(url: string) {
  return url.replace(/^https?:\/\//, '')
}

function isCopied(url: string) {
  return copied.value && copiedText.value === url
}

async function copyLink(url: string) {
  const ok = await copy(url).then(() => isCopied(url), () => false)
  if (ok) toast.success('Copied. Go be useful.')
  else toast.error('Copy did not work. Select the link and copy it.')
}
</script>

<template>
  <UDrawer
    :direction="isDesktop ? 'right' : 'bottom'"
    :handle="!isDesktop"
    title="Previous links"
    :description="isAccount ? 'Links in your account.' : 'Saved in this browser only.'"
    :ui="{ content: isDesktop ? 'w-full max-w-md' : '', title: 'text-base', body: 'min-h-0' }"
  >
    <UButton
      variant="link"
      size="sm"
      class="px-0 text-[13px] text-toned hover:text-highlighted"
    >
      View previous links
    </UButton>

    <template #body>
      <ul class="-mx-4 divide-y divide-default border-y border-default">
        <li
          v-for="link in links"
          :key="link.short_url"
          class="flex items-center gap-3 px-4 py-3"
        >
          <div class="min-w-0 flex-1 text-left">
            <p
              class="truncate text-[13px] text-dimmed"
              :title="link.target_url"
            >
              {{ display(link.target_url) }}
            </p>
            <a
              :href="link.short_url"
              target="_blank"
              rel="noopener"
              class="mt-0.5 block truncate rounded-sm text-sm font-medium text-highlighted underline-offset-4 outline-none hover:underline focus-visible:ring-3 focus-visible:ring-(--ds-ink)/15"
            >
              {{ display(link.short_url) }}
            </a>
          </div>
          <UButton
            color="neutral"
            variant="outline"
            size="sm"
            square
            :icon="isCopied(link.short_url) ? 'i-lucide-check' : 'i-lucide-copy'"
            :aria-label="`Copy ${display(link.short_url)}`"
            @click="copyLink(link.short_url)"
          />
        </li>
      </ul>
    </template>
  </UDrawer>
</template>
