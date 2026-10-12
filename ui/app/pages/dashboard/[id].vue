<script setup lang="ts">
import { toast } from 'vue-sonner'

const PERIODS = [7, 30, 90]
const BREAKDOWNS = [
  { key: 'sources', title: 'Sources' },
  { key: 'countries', title: 'Countries' },
  { key: 'devices', title: 'Devices' },
  { key: 'browsers', title: 'Browsers' },
  { key: 'operating_systems', title: 'Operating systems' },
  { key: 'referrers', title: 'Referrers' },
  { key: 'utm_campaigns', title: 'Campaigns' }
]

useSeoMeta({
  title: `Link stats · ${site.name}`,
  robots: 'noindex, nofollow'
})

const route = useRoute()
const me = useCurrentUser()
const links = useAccountLinks()
const { copy, copied } = useClipboard({ copiedDuring: 2000, legacy: true })

const id = computed(() => Number(route.params.id))
const days = ref(30)
const stats = useLinkStats(id, days)

const link = computed(() => links.data.value?.find(item => item.id === id.value))

watch(() => me.data.value, (user) => {
  if (user === null) navigateTo('/login')
}, { immediate: true })

watch([() => links.data.value, () => stats.error.value], ([data, error]) => {
  const missing = data && !link.value
  if (missing || apiStatus(error) === 404) {
    showError({ statusCode: 404, statusMessage: 'Link not found' })
  }
})

const totals = computed(() => {
  const value = stats.data.value
  return [
    { label: 'Clicks', value: value?.clicks },
    { label: 'Unique visitors', value: value?.unique_visitors },
    { label: 'Bot visits', value: value?.bot_clicks }
  ]
})

const daily = computed(() => stats.data.value?.daily ?? [])
const peak = computed(() => Math.max(1, ...daily.value.map(day => day.clicks)))

const breakdowns = computed(() => {
  const data = stats.data.value?.breakdown ?? {}
  return BREAKDOWNS
    .map(section => ({ ...section, entries: data[section.key] ?? [] }))
    .filter(section => section.entries.length || section.key !== 'utm_campaigns')
})

const regionNames = new Intl.DisplayNames(['en'], { type: 'region' })

function label(key: string, name: string) {
  if (key === 'countries' && /^[A-Z]{2}$/.test(name)) return regionNames.of(name) ?? name
  if (key === 'devices') return name.charAt(0).toUpperCase() + name.slice(1)
  return name
}

function share(entries: StatsEntry[], clicks: number) {
  const top = Math.max(1, ...entries.map(entry => entry.clicks))
  return `${Math.max(2, (clicks / top) * 100)}%`
}

async function copyLink() {
  if (!link.value) return
  const ok = await copy(link.value.short_url).then(() => copied.value, () => false)
  if (ok) toast.success('Copied.')
  else toast.error('Copy did not work. Select the link and copy it.')
}
</script>

<template>
  <section class="mx-auto w-full max-w-3xl px-5 pt-6 pb-16 sm:px-8 sm:pt-10">
    <UButton
      to="/dashboard"
      variant="link"
      size="sm"
      icon="i-lucide-arrow-left"
      class="-ms-0.5 px-0 text-[13px] text-toned hover:text-highlighted"
    >
      All links
    </UButton>

    <div class="mt-4 flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between">
      <div class="min-w-0">
        <USkeleton
          v-if="!link"
          class="h-8 w-56"
        />
        <h1
          v-else
          class="truncate text-2xl font-semibold tracking-tight text-highlighted sm:text-3xl"
        >
          {{ displayUrl(link.short_url) }}
        </h1>
        <USkeleton
          v-if="!link"
          class="mt-2 h-4 w-72 max-w-full"
        />
        <a
          v-else
          :href="link.target_url"
          target="_blank"
          rel="noopener"
          class="mt-1 block truncate rounded-sm text-[13px] text-muted underline-offset-4 outline-none hover:text-highlighted hover:underline focus-visible:ring-3 focus-visible:ring-(--ds-ink)/15"
          :title="link.target_url"
        >
          {{ link.target_url }}
        </a>
      </div>

      <div
        v-if="link"
        class="flex shrink-0 gap-2"
      >
        <UButton
          variant="outline"
          :icon="copied ? 'i-lucide-check' : 'i-lucide-copy'"
          class="flex-1 justify-center sm:flex-none"
          @click="copyLink"
        >
          {{ copied ? 'Copied' : 'Copy' }}
        </UButton>
        <UButton
          :href="link.short_url"
          target="_blank"
          variant="outline"
          icon="i-lucide-external-link"
          class="flex-1 justify-center sm:flex-none"
        >
          Open
        </UButton>
      </div>
    </div>

    <div class="mt-8 flex flex-wrap items-center justify-between gap-3">
      <h2 class="text-[15px] font-medium text-highlighted">
        Last {{ days }} days
      </h2>
      <div
        role="group"
        aria-label="Period"
        class="inline-flex rounded-lg border border-default bg-default p-0.5"
      >
        <button
          v-for="period in PERIODS"
          :key="period"
          type="button"
          :aria-pressed="days === period"
          class="rounded-md px-3 py-1 text-xs font-medium text-muted outline-none transition-colors hover:text-highlighted focus-visible:ring-3 focus-visible:ring-(--ds-ink)/15 aria-pressed:bg-accented aria-pressed:text-highlighted"
          @click="days = period"
        >
          {{ period }}d
        </button>
      </div>
    </div>

    <div
      v-if="stats.isError.value && apiStatus(stats.error.value) !== 404"
      class="mt-4 rounded-xl border border-default bg-default px-4 py-10 text-center"
    >
      <p class="text-sm text-toned">
        Stats could not be loaded.
      </p>
      <UButton
        variant="outline"
        size="sm"
        class="mt-3"
        @click="stats.refetch()"
      >
        Try again
      </UButton>
    </div>

    <template v-else>
      <dl class="mt-4 grid grid-cols-3 divide-x divide-default overflow-hidden rounded-xl border border-default bg-default">
        <div
          v-for="total in totals"
          :key="total.label"
          class="px-3 py-4 sm:px-5"
        >
          <dt class="truncate text-xs text-muted">
            {{ total.label }}
          </dt>
          <dd class="mt-1 text-xl font-semibold text-highlighted tabular-nums sm:text-2xl">
            <USkeleton
              v-if="total.value === undefined"
              class="h-7 w-12"
            />
            <template v-else>
              {{ formatNumber(total.value) }}
            </template>
          </dd>
        </div>
      </dl>

      <div
        class="mt-4 rounded-xl border border-default bg-default p-4 sm:p-5"
        :class="stats.isFetching.value ? 'opacity-60' : ''"
      >
        <USkeleton
          v-if="!daily.length"
          class="h-40 w-full"
        />
        <template v-else>
          <div
            role="img"
            :aria-label="`Daily clicks, most in one day: ${peak}`"
            class="flex h-40 items-end gap-px sm:gap-0.5"
          >
            <div
              v-for="day in daily"
              :key="day.day"
              class="group relative flex h-full flex-1 items-end"
            >
              <div
                class="w-full rounded-t-sm transition-colors"
                :class="day.clicks ? 'bg-(--ds-ink)/80 group-hover:bg-(--ds-ink)' : 'bg-accented'"
                :style="{ height: day.clicks ? `${Math.max(3, (day.clicks / peak) * 100)}%` : '2px' }"
              />
              <span class="pointer-events-none absolute bottom-full left-1/2 z-10 mb-2 hidden -translate-x-1/2 rounded-md bg-inverted px-2 py-1 text-xs whitespace-nowrap text-inverted group-hover:block">
                {{ formatDay(day.day) }} · {{ formatNumber(day.clicks) }}
              </span>
            </div>
          </div>
          <div class="mt-2 flex justify-between text-xs text-muted">
            <span>{{ formatDay(daily[0]!.day) }}</span>
            <span>{{ formatDay(daily[daily.length - 1]!.day) }}</span>
          </div>
        </template>
      </div>

      <div class="mt-4 grid gap-4 sm:grid-cols-2">
        <section
          v-for="section in breakdowns"
          :key="section.key"
          class="rounded-xl border border-default bg-default p-4 sm:p-5"
        >
          <h3 class="text-[13px] font-medium text-highlighted">
            {{ section.title }}
          </h3>
          <ul
            v-if="section.entries.length"
            class="mt-3 space-y-1"
          >
            <li
              v-for="entry in section.entries.slice(0, 6)"
              :key="entry.name"
              class="relative flex items-center justify-between gap-3 overflow-hidden rounded-md px-2 py-1.5 text-[13px]"
            >
              <span
                class="absolute inset-y-0 left-0 rounded-md bg-muted"
                :style="{ width: share(section.entries, entry.clicks) }"
              />
              <span class="relative truncate text-toned">{{ label(section.key, entry.name) }}</span>
              <span class="relative shrink-0 font-medium text-highlighted tabular-nums">{{ formatNumber(entry.clicks) }}</span>
            </li>
          </ul>
          <p
            v-else
            class="mt-3 text-[13px] text-dimmed"
          >
            {{ stats.data.value ? 'No clicks yet.' : 'Loading…' }}
          </p>
        </section>
      </div>
    </template>
  </section>
</template>
