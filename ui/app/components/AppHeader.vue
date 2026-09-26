<script setup lang="ts">
import type { DropdownMenuItem } from '@nuxt/ui'

const route = useRoute()
const colorMode = useColorMode()
const isDark = computed(() => colorMode.value === 'dark')
const { data: user } = useCurrentUser()
const logout = useLogout()

function toggleTheme() {
  const next = isDark.value ? 'light' : 'dark'
  const reduceMotion = window.matchMedia('(prefers-reduced-motion: reduce)').matches
  if (!document.startViewTransition || reduceMotion) {
    colorMode.preference = next
    return
  }
  document.startViewTransition(async () => {
    colorMode.preference = next
    await nextTick()
  })
}

const shortEmail = computed(() => user.value ? `${user.value.email.slice(0, 3)}…` : '')

const accountItems = computed<DropdownMenuItem[][]>(() => [
  [{ type: 'label', label: user.value?.email }],
  [{ label: 'Log out', icon: 'i-lucide-log-out', onSelect: () => logout.mutate() }]
])
</script>

<template>
  <header class="mx-auto flex h-14 w-full max-w-6xl items-center justify-between px-5 sm:px-8">
    <NuxtLink
      to="/"
      class="-mx-1 rounded-md px-1 text-[15px] font-semibold tracking-tight text-highlighted outline-none focus-visible:ring-3 focus-visible:ring-(--ds-ink)/15"
    >
      {{ site.name }}
    </NuxtLink>

    <div class="flex items-center gap-2">
      <ClientOnly>
        <UButton
          color="neutral"
          variant="ghost"
          :icon="isDark ? 'i-lucide-moon' : 'i-lucide-sun'"
          :aria-label="isDark ? 'Switch to light mode' : 'Switch to dark mode'"
          @click="toggleTheme"
        />
        <template #fallback>
          <div class="size-7" />
        </template>
      </ClientOnly>

      <template v-if="user">
        <UDropdownMenu
          :items="accountItems"
          :content="{ align: 'end' }"
        >
          <UButton
            color="neutral"
            variant="ghost"
            size="sm"
            icon="i-lucide-circle-user"
            :label="shortEmail"
            :aria-label="`Account menu for ${user.email}`"
          />
        </UDropdownMenu>
        <UButton
          to="/dashboard"
          color="neutral"
          variant="outline"
          size="sm"
        >
          Dashboard
        </UButton>
      </template>
      <UButton
        v-else-if="route.path !== '/login'"
        to="/login"
        color="neutral"
        variant="outline"
        size="sm"
      >
        Log in
      </UButton>
    </div>
  </header>
</template>
